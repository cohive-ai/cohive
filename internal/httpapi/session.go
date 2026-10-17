package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
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
	deps := auditDeps{svc: svc, limiter: &keyedLimiter{max: 30, window: 10 * time.Minute}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = r.WithContext(context.WithValue(r.Context(), auditKey{}, deps))
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
		if kinds == aDevice {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "这个接口只认设备密钥（cohive login 配对后保存在本机），成员令牌不能用"})
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
	register(pattern)
	// 只认密码会话的接口就是管理接口（含授权设备、接受邀请）：已认证的身份被拒要记下来
	audited := kinds&aSession == aPassword
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		c := caller(r)
		if !allowed(w, c, kinds) {
			if audited {
				recordDenied(r, c, pattern, core.DenyNeedPassword)
			}
			return
		}
		if !audited {
			h(w, r)
			return
		}
		sw := &statusWriter{ResponseWriter: w}
		h(sw, r)
		if sw.status == http.StatusForbidden {
			recordDenied(r, c, pattern, core.DenyNoPermission)
		}
	})
}

// statusWriter 记下响应的状态码。
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

// auditDeps 由 authMiddleware 放进请求：记录被拒请求要用的业务层与限频。
type auditDeps struct {
	svc     *core.Service
	limiter *keyedLimiter
}

type auditKey struct{}

// recordDenied 记下已认证的身份调管理接口被拒。记录失败只写日志，不影响给调用方的响应；
// 同一账号 10 分钟内最多记 30 条，防止陷入循环的智能体把记录刷满。
func recordDenied(r *http.Request, c Caller, pattern, reason string) {
	deps, ok := r.Context().Value(auditKey{}).(auditDeps)
	if !ok || c.Account.ID == 0 {
		return
	}
	if !deps.limiter.allow(strconv.FormatInt(c.Account.ID, 10)) {
		return
	}
	act, cred := core.WebActor(c.Account), core.CredPassword
	switch {
	case c.Token != nil:
		act, cred = core.AgentActor(c.Account), core.CredToken
	case c.Device != nil:
		act, cred = core.MemberCLIActor(c.Account), core.CredDevice
	case c.SessionVia == store.SessionViaDevice:
		cred = core.CredDeviceWeb
	}
	d := core.DeniedAttempt{Workspace: r.PathValue("ws"), Route: pattern, Reason: reason, Credential: cred}
	if err := deps.svc.RecordDenied(r.Context(), act, d); err != nil {
		log.Printf("记录被拒的管理请求失败：%v", err)
	}
}

// handlePublic 注册不需要身份的接口（登录、注册、设备发起与轮询等）。
func handlePublic(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	register(pattern)
	mux.HandleFunc(pattern, h)
}

var (
	routesMu sync.Mutex
	routes   = map[string]bool{}
)

func register(pattern string) {
	routesMu.Lock()
	routes[pattern] = true
	routesMu.Unlock()
}

// Routes 列出已注册的全部接口（“方法 路径”），供测试检查每个写接口都归了类（管理操作是否留记录）。
func Routes() []string {
	routesMu.Lock()
	defer routesMu.Unlock()
	out := make([]string, 0, len(routes))
	for p := range routes {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
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

// Origin 返回用户访问本站用的地址，用于生成授权页、登录链接与安装脚本。
func Origin(r *http.Request) string { return origin(r) }

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
