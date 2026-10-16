package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// Announcer 在有新的待审申请时通知管理员。
type Announcer interface {
	AnnounceSignup(ctx context.Context, s store.Signup) error
	AnnounceDevice(ctx context.Context, a store.DeviceAuth) error
}

// announce 在后台通知，不拖慢请求；失败只记日志（管理员仍可在网页上看到待审申请）。
func announce(opts Options, fn func(ctx context.Context, a Announcer) error) {
	if opts.Announcer == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := fn(ctx, opts.Announcer); err != nil {
			log.Printf("通知管理员审批失败：%v", err)
		}
	}()
}

func tooMany(w http.ResponseWriter) {
	writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "请求过于频繁，请一分钟后再试"})
}

// publicRoutes 注册不需要身份的接口，均按来源地址限频。
func publicRoutes(mux *http.ServeMux, svc *core.Service, opts Options) {
	logins := &keyedLimiter{max: 20, window: time.Minute}
	registers := &keyedLimiter{max: 10, window: time.Minute}
	lookups := &keyedLimiter{max: 30, window: time.Minute}
	starts := &keyedLimiter{max: 10, window: time.Minute}
	polls := &keyedLimiter{max: 120, window: time.Minute}
	guard := newLoginGuard()

	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		if !logins.allow(limitKey(clientIP(r))) {
			tooMany(w)
			return
		}
		var in struct {
			Name      string `json:"name"`
			Password  string `json:"password"`
			Workspace string `json:"workspace"`
		}
		if !decode(w, r, &in) {
			return
		}
		if d := guard.locked(in.Name); d > 0 {
			svc.DummyPasswordCheck(r.Context(), in.Password) // 锁定时也耗同样的时间，不暴露成员名是否存在
			writeJSON(w, http.StatusTooManyRequests, map[string]string{
				"error": fmt.Sprintf("这个成员名连续登录失败次数过多，请 %d 秒后再试", int(d.Seconds())+1)})
			return
		}
		sess, err := svc.Login(r.Context(), in.Name, in.Password, in.Workspace)
		var amb *core.AmbiguousLoginError
		switch {
		case errors.As(err, &amb):
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "workspaces": amb.Workspaces})
			return
		case errors.Is(err, core.ErrBadLogin):
			guard.failed(in.Name)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
			return
		case errors.Is(err, core.ErrSignupPending):
			writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			return
		case err != nil:
			writeError(w, err)
			return
		}
		guard.succeeded(in.Name)
		setSessionCookie(w, r, sess.ID)
		pw, err := svc.PasswordStatus(r.Context(), sess.Member.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		ws, err := store.GetWorkspace(r.Context(), svc.Pool, sess.Member.WorkspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"member": sess.Member.Name, "workspace": ws.Name,
			"role": sess.Member.Role, "must_change_password": pw.MustChange})
	})

	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
			if !sameOrigin(r) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "跨站请求已拒绝"})
				return
			}
			if err := svc.Logout(r.Context(), c.Value); err != nil {
				writeError(w, err)
				return
			}
		}
		clearSessionCookie(w, r)
		w.WriteHeader(http.StatusNoContent)
	})

	// cohive web 生成的一次性链接：建会话后跳到看板
	mux.HandleFunc("GET /auth/once/{code}", func(w http.ResponseWriter, r *http.Request) {
		sess, err := svc.UseWebLoginCode(r.Context(), r.PathValue("code"))
		if errors.Is(err, core.ErrWebLoginCode) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8"><title>Cohive</title><p>` + err.Error() + `</p><p><a href="/login">用账号密码登录</a></p>`))
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		setSessionCookie(w, r, sess.ID)
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})

	mux.HandleFunc("GET /api/v1/invites/{code}", func(w http.ResponseWriter, r *http.Request) {
		if !lookups.allow(limitKey(clientIP(r))) {
			tooMany(w)
			return
		}
		info, err := svc.LookupInvite(r.Context(), r.PathValue("code"))
		if errors.Is(err, core.ErrInviteInvalid) {
			writeJSON(w, http.StatusOK, map[string]any{"valid": false})
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspace": info.Workspace, "kind": info.Kind, "valid": info.Valid})
	})

	mux.HandleFunc("POST /api/v1/register", func(w http.ResponseWriter, r *http.Request) {
		if !registers.allow(limitKey(clientIP(r))) {
			tooMany(w)
			return
		}
		var in struct {
			Code        string `json:"code"`
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
			Password    string `json:"password"`
		}
		if !decode(w, r, &in) {
			return
		}
		res, err := svc.Register(r.Context(), core.RegisterInput{Code: in.Code, Name: in.Name, DisplayName: in.DisplayName, Password: in.Password})
		if err != nil {
			writeError(w, err)
			return
		}
		if res.Signup != nil {
			sg := *res.Signup
			announce(opts, func(ctx context.Context, a Announcer) error { return a.AnnounceSignup(ctx, sg) })
			writeJSON(w, http.StatusOK, map[string]string{"status": "pending"})
			return
		}
		sess, err := svc.SessionForRegistered(r.Context(), *res.Member)
		if err != nil {
			writeError(w, err)
			return
		}
		setSessionCookie(w, r, sess.ID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "active", "member": res.Member.Name})
	})

	mux.HandleFunc("POST /api/v1/device/start", func(w http.ResponseWriter, r *http.Request) {
		if !starts.allow(limitKey(clientIP(r))) {
			tooMany(w)
			return
		}
		var in struct {
			Tools      []string `json:"tools"`
			DeviceName string   `json:"device_name"`
		}
		if !decode(w, r, &in) {
			return
		}
		st, err := svc.StartDeviceAuth(r.Context(), in.Tools, in.DeviceName)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"request_id": st.RequestID, "poll_secret": st.PollSecret,
			"url": origin(r) + "/device/" + st.Nonce, "expires_in": int(st.ExpiresIn.Seconds())})
	})

	mux.HandleFunc("POST /api/v1/device/poll", func(w http.ResponseWriter, r *http.Request) {
		if !polls.allow(limitKey(clientIP(r))) {
			tooMany(w)
			return
		}
		var in struct {
			RequestID  int64  `json:"request_id"`
			PollSecret string `json:"poll_secret"`
		}
		if !decode(w, r, &in) {
			return
		}
		p, err := svc.PollDeviceAuth(r.Context(), in.RequestID, in.PollSecret)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, deviceTokensJSON(p))
	})
}

func deviceTokensJSON(p core.DeviceTokens) map[string]any {
	out := map[string]any{"status": p.Status}
	if p.Tokens != nil {
		out["member"], out["workspace"], out["device_id"], out["tokens"] = p.Member, p.Workspace, p.DeviceID, p.Tokens
	}
	return out
}
