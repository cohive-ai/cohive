package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "编号不合法"})
		return 0, false
	}
	return id, true
}

func deviceJSON(d store.Device) map[string]any {
	tools := d.Tools
	if tools == nil {
		tools = []string{}
	}
	return map[string]any{"id": d.ID, "member": d.MemberName, "name": d.Name, "created_at": d.CreatedAt,
		"revoked_at": d.RevokedAt, "tools": tools}
}

func botName(opts Options) string {
	if opts.BotName == nil {
		return ""
	}
	return opts.BotName()
}

// accountRoutes 注册登录后成员自己的接口：个人信息、改密码、设备、通讯工具连接、设备授权页。
func accountRoutes(mux *http.ServeMux, svc *core.Service, opts Options) {
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		c := caller(r)
		ws, err := store.GetWorkspace(r.Context(), svc.Pool, c.Member.WorkspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		pw, err := svc.PasswordStatus(r.Context(), c.Member.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"member": c.Member.Name, "display_name": c.Member.DisplayName, "workspace": ws.Name, "role": c.Member.Role,
			"tool": c.Identity.Tool, "must_change_password": pw.MustChange, "has_password": pw.Hash != "",
			"telegram": map[string]any{"username": c.Member.TelegramUsername, "linked": c.Member.TelegramUserID != nil},
		})
	})

	mux.HandleFunc("POST /api/v1/auth/password", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Old string `json:"old_password"`
			New string `json:"new_password"`
		}
		if !decode(w, r, &in) {
			return
		}
		c := caller(r)
		if err := svc.ChangePassword(r.Context(), c.Member, c.Session, in.Old, in.New); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// cohive web：已配对设备上的令牌换一次性网页登录链接
	mux.HandleFunc("POST /api/v1/auth/web-login", func(w http.ResponseWriter, r *http.Request) {
		if !deviceOnly(w, r) {
			return
		}
		code, err := svc.CreateWebLoginCode(r.Context(), caller(r).Identity)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"url": origin(r) + "/auth/once/" + code})
	})

	mux.HandleFunc("GET /api/v1/device/auth/{nonce}", func(w http.ResponseWriter, r *http.Request) {
		if !sessionOnly(w, r) {
			return
		}
		v, err := svc.ViewDeviceAuth(r.Context(), caller(r).Member, r.PathValue("nonce"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"device_name": v.DeviceName, "tools": v.Tools, "created_at": v.CreatedAt,
			"status": v.Status, "first_device": v.FirstDevice})
	})
	mux.HandleFunc("POST /api/v1/device/auth/{nonce}", func(w http.ResponseWriter, r *http.Request) {
		if !sessionOnly(w, r) {
			return
		}
		var in struct {
			Approve bool `json:"approve"`
		}
		if !decode(w, r, &in) {
			return
		}
		a, err := svc.AuthorizeDevice(r.Context(), caller(r).Member, r.PathValue("nonce"), in.Approve)
		if err != nil {
			writeError(w, err)
			return
		}
		if a.Status == store.AuthAwaitingAdmin {
			announce(opts, func(ctx context.Context, an Announcer) error { return an.AnnounceDevice(ctx, a) })
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": a.Status})
	})

	// 已配对的设备补签其他工具的令牌
	mux.HandleFunc("POST /api/v1/devices/self/tools", func(w http.ResponseWriter, r *http.Request) {
		if !deviceOnly(w, r) {
			return
		}
		var in struct {
			Tools []string `json:"tools"`
		}
		if !decode(w, r, &in) {
			return
		}
		p, err := svc.AddDeviceTools(r.Context(), caller(r).Identity, in.Tools)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, deviceTokensJSON(p))
	})

	mux.HandleFunc("GET /api/v1/me/devices", func(w http.ResponseWriter, r *http.Request) {
		c := caller(r)
		list, err := store.ListDevices(r.Context(), svc.Pool, c.Member.WorkspaceID, c.Member.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]map[string]any, 0, len(list))
		for _, d := range list {
			items = append(items, deviceJSON(d))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	mux.HandleFunc("POST /api/v1/me/devices/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		m := caller(r).Member
		m.Role = store.RoleMember // 个人中心只能吊销自己的设备
		if err := svc.RevokeDevice(r.Context(), m, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/v1/me/links", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Provider string `json:"provider"`
		}
		if !decode(w, r, &in) {
			return
		}
		name := botName(opts)
		if in.Provider == core.ProviderTelegram && name == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "协作服务没有启用 Telegram 机器人"})
			return
		}
		l, err := svc.StartLink(r.Context(), caller(r).Identity, in.Provider)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": l.ID, "url": "https://t.me/" + name + "?start=" + l.Nonce,
			"expires_in": int(time.Until(l.ExpiresAt).Seconds())})
	})
	mux.HandleFunc("GET /api/v1/me/links/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		l, err := svc.LinkStatus(r.Context(), caller(r).Identity, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": l.Status, "detail": l.Detail})
	})
	mux.HandleFunc("DELETE /api/v1/me/links/telegram", func(w http.ResponseWriter, r *http.Request) {
		m := caller(r).Member
		if err := svc.UnlinkTelegram(r.Context(), m, m.ID); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
