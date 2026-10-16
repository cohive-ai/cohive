package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/cohive-ai/cohive/internal/authn"
	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

const sessionCookie = "cohive_session"

// Caller 是一次请求的操作者。网页会话时 Session 非空、Identity.Tool 为 web。
type Caller struct {
	Identity   store.Identity
	Member     store.Member
	Session    string
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

// authMiddleware 先认会话 Cookie，没有 Authorization 头时才看 Cookie；有令牌时按令牌鉴权。
func authMiddleware(svc *core.Service, next http.Handler) http.Handler {
	bearer := authn.Middleware(svc.Pool)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		who, _ := authn.FromContext(r.Context())
		m, err := store.MemberByID(r.Context(), svc.Pool, who.MemberID)
		if err != nil {
			writeError(w, err)
			return
		}
		next.ServeHTTP(w, withCaller(r, Caller{Identity: who, Member: m}))
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			bearer.ServeHTTP(w, r)
			return
		}
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "请先登录"})
			return
		}
		m, err := svc.SessionMember(r.Context(), c.Value)
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
		pw, err := svc.PasswordStatus(r.Context(), m.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		if pw.MustChange && !allowedWhileMustChange[r.Method+" "+r.URL.Path] {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": "请先修改密码", "must_change_password": true})
			return
		}
		who := store.Identity{MemberID: m.ID, WorkspaceID: m.WorkspaceID, MemberName: m.Name, Tool: "web"}
		next.ServeHTTP(w, withCaller(r, Caller{Identity: who, Member: m, Session: c.Value, MustChange: pw.MustChange}))
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式不对"})
		return false
	}
	return true
}

// sessionOnly 要求请求来自网页会话（设备授权、改密码等只能在网页上做）。
func sessionOnly(w http.ResponseWriter, r *http.Request) bool {
	if caller(r).Session == "" {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "这个操作只能在网页上进行"})
		return false
	}
	return true
}

// deviceOnly 要求请求来自已配对设备上的令牌。
func deviceOnly(w http.ResponseWriter, r *http.Request) bool {
	if caller(r).Identity.DeviceID == nil {
		writeError(w, core.ErrNoDevice)
		return false
	}
	return true
}
