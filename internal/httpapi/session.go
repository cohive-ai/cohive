package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

const sessionCookie = "cohive_session"

// Caller 是一次请求的操作者。四类鉴权（设计稿第 4 节）：
// 网页会话（Session 非空；SessionVia 为 password 或 device）、成员令牌（Token 非空）、设备密钥（Device 非空）。
type Caller struct {
	Account    store.Account
	Session    string
	SessionVia string
	Token      *store.Identity // 成员令牌：属于某个成员资格，即某个工作空间
	Device     *store.Device   // 设备密钥
	MustChange bool
}

type callerKey struct{}

func withCaller(r *http.Request, c Caller) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), callerKey{}, c))
}

func caller(r *http.Request) Caller {
	c, _ := r.Context().Value(callerKey{}).(Caller)
	return c
}

// 必须先改密码时，只放行这几个接口。
var allowedWhileMustChange = map[string]bool{
	"GET /api/v1/me":             true,
	"POST /api/v1/auth/password": true,
}

// authMiddleware 有 Authorization 头时按令牌或设备密钥鉴权，否则认会话 Cookie。
// 每类凭据能调哪些接口由 handle 注册时声明，在 allowed 里统一判定。
func authMiddleware(svc *core.Service, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := r.Header.Get("Authorization"); h != "" {
			plain, ok := strings.CutPrefix(h, "Bearer ")
			if !ok || plain == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "令牌格式不对"})
				return
			}
			if strings.HasPrefix(plain, core.DeviceSecretPrefix) {
				d, a, err := svc.AuthenticateDevice(r.Context(), plain)
				if errors.Is(err, store.ErrUnauthorized) {
					writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "设备密钥无效、设备已吊销或账号已停用，请重新执行 cohive login"})
					return
				}
				if err != nil {
					writeError(w, err)
					return
				}
				next.ServeHTTP(w, withCaller(r, Caller{Account: a, Device: &d}))
				return
			}
			who, err := store.LookupToken(r.Context(), svc.Pool, plain)
			if errors.Is(err, store.ErrUnauthorized) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "令牌无效或已吊销"})
				return
			}
			if err != nil {
				writeError(w, err)
				return
			}
			a, err := svc.Account(r.Context(), who.AccountID)
			if err != nil {
				writeError(w, err)
				return
			}
			next.ServeHTTP(w, withCaller(r, Caller{Account: a, Token: &who}))
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请先登录"})
			return
		}
		a, via, err := svc.SessionAccount(r.Context(), c.Value)
		if errors.Is(err, store.ErrUnauthorized) {
			clearSessionCookie(w, r)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "登录已过期，请重新登录"})
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "跨站请求已拒绝"})
			return
		}
		pw, err := svc.PasswordStatus(r.Context(), a.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		if pw.MustChange && !allowedWhileMustChange[r.Method+" "+r.URL.Path] {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "请先修改密码", "must_change_password": true})
			return
		}
		next.ServeHTTP(w, withCaller(r, Caller{Account: a, Session: c.Value, SessionVia: via, MustChange: pw.MustChange}))
	})
}

// 接口接受的凭据种类
type allow int

const (
	aPassword    allow = 1 << iota // 账号密码登录的网页会话
	aDeviceWeb                     // cohive web 免密会话
	aToken                         // 成员令牌
	aDeviceToken                   // 属于某台设备的成员令牌（cohive web 兼容 M5 配对的旧电脑）
	aDevice                        // 设备密钥

	aSession = aPassword | aDeviceWeb
)

// errNeedPassword 是需要密码登录的网页会话时的提示。
const errNeedPassword = "管理操作请在网页上用账号密码登录后进行"

// allowed 判定这次请求的凭据能否调用该接口；不能时已写好响应。
func allowed(w http.ResponseWriter, c Caller, kinds allow) bool {
	switch {
	case c.Device != nil:
		if kinds&aDevice != 0 {
			return true
		}
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "设备密钥不能用于这个接口"})
		return false
	case c.Token != nil:
		if kinds&aToken != 0 || (kinds&aDeviceToken != 0 && c.Token.DeviceID != nil) {
			return true
		}
		if kinds&aDeviceToken != 0 {
			writeError(w, core.ErrNoDevice)
			return false
		}
		if kinds&aSession == aPassword {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": errNeedPassword, "need_password_login": true})
			return false
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "这个操作只能在网页上进行"})
		return false
	default:
		if (c.SessionVia == store.SessionViaPassword && kinds&aPassword != 0) ||
			(c.SessionVia == store.SessionViaDevice && kinds&aDeviceWeb != 0) {
			return true
		}
		if kinds&aPassword != 0 {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": errNeedPassword, "need_password_login": true})
			return false
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "这个接口不能在网页上使用"})
		return false
	}
}

// handle 注册一个需要身份的接口，并声明它接受的凭据种类。
func handle(mux *http.ServeMux, pattern string, kinds allow, h http.HandlerFunc) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		if !allowed(w, caller(r), kinds) {
			return
		}
		h(w, r)
	})
}

// sameOrigin 要求浏览器带的 Origin 与本站一致，防止别的网站借用户的 Cookie 发请求。
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// isHTTPS 判断用户看到的是否为 HTTPS：直连 TLS，或本机反向代理（如 cloudflared）转发时带的协议头。
func isHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-Proto") == "https"
}

// origin 返回用户访问本站用的地址，用于生成授权页、登录链接。
func origin(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, id string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, Secure: isHTTPS(r),
		SameSite: http.SameSiteLaxMode, MaxAge: int(core.SessionTTL / time.Second)})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: isHTTPS(r),
		SameSite: http.SameSiteLaxMode, MaxAge: -1})
}

// decode 读取不超过 8KB 的 JSON 请求体；失败时已写好错误响应。
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	return decodeLimit(w, r, v, 8192)
}

func decodeLimit(w http.ResponseWriter, r *http.Request, v any, limit int64) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式不对"})
		return false
	}
	return true
}

// loginMethod 返回这次请求的登录方式：password（账号密码）、device（cohive web 免密）、token（编程工具令牌）。
func loginMethod(c Caller) string {
	if c.Session == "" {
		return "token"
	}
	return c.SessionVia
}
