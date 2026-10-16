package httpapi

import (
	"net/http"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// admin 要求操作者是本工作空间管理员，返回操作者。
func admin(w http.ResponseWriter, r *http.Request) (*store.Member, bool) {
	m := caller(r).Member
	if !m.IsAdmin() {
		writeError(w, core.ErrNotAdmin)
		return nil, false
	}
	return &m, true
}

// adminRoutes 注册管理页的接口：待审、成员、设备、邀请、工作空间设置。
func adminRoutes(mux *http.ServeMux, svc *core.Service, opts Options) {
	mux.HandleFunc("GET /api/v1/admin/pending", func(w http.ResponseWriter, r *http.Request) {
		a, ok := admin(w, r)
		if !ok {
			return
		}
		signups, err := store.ListPendingSignups(r.Context(), svc.Pool, a.WorkspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		devs, err := store.ListAwaitingDeviceAuths(r.Context(), svc.Pool, a.WorkspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		so := make([]map[string]any, 0, len(signups))
		for _, s := range signups {
			so = append(so, map[string]any{"id": s.ID, "name": s.Name, "display_name": s.DisplayName, "created_at": s.CreatedAt})
		}
		do := make([]map[string]any, 0, len(devs))
		for _, d := range devs {
			do = append(do, map[string]any{"id": d.ID, "member": d.MemberName, "device_name": d.DeviceName, "tools": d.Tools, "created_at": d.CreatedAt})
		}
		writeJSON(w, http.StatusOK, map[string]any{"signups": so, "devices": do})
	})

	decide := func(fn func(r *http.Request, a *store.Member, id int64, approve bool) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			a, ok := admin(w, r)
			if !ok {
				return
			}
			id, ok := pathID(w, r, "id")
			if !ok {
				return
			}
			var in struct {
				Approve bool `json:"approve"`
			}
			if !decode(w, r, &in) {
				return
			}
			if err := fn(r, a, id, in.Approve); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
	mux.HandleFunc("POST /api/v1/admin/signups/{id}", decide(func(r *http.Request, a *store.Member, id int64, approve bool) error {
		_, err := svc.DecideSignup(r.Context(), a, id, approve)
		return err
	}))
	mux.HandleFunc("POST /api/v1/admin/device-auths/{id}", decide(func(r *http.Request, a *store.Member, id int64, approve bool) error {
		_, err := svc.DecideDeviceAuth(r.Context(), a, id, approve)
		return err
	}))

	mux.HandleFunc("GET /api/v1/admin/members", func(w http.ResponseWriter, r *http.Request) {
		a, ok := admin(w, r)
		if !ok {
			return
		}
		list, err := store.ListMemberSummaries(r.Context(), svc.Pool, a.WorkspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]map[string]any, 0, len(list))
		for _, m := range list {
			items = append(items, map[string]any{"id": m.ID, "name": m.Name, "display_name": m.DisplayName, "role": m.Role,
				"telegram_username": m.TelegramUsername, "telegram_linked": m.TelegramUserID != nil, "devices": m.Devices,
				"has_password": m.HasPassword})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	mux.HandleFunc("POST /api/v1/admin/members/{id}", func(w http.ResponseWriter, r *http.Request) {
		a, ok := admin(w, r)
		if !ok {
			return
		}
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		var in struct {
			Role string `json:"role"`
		}
		if !decode(w, r, &in) {
			return
		}
		if in.Role != "" {
			if err := svc.SetRole(r.Context(), a, id, in.Role); err != nil {
				writeError(w, err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /api/v1/admin/members/{id}/telegram", func(w http.ResponseWriter, r *http.Request) {
		a, ok := admin(w, r)
		if !ok {
			return
		}
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		if err := svc.UnlinkTelegram(r.Context(), *a, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/v1/admin/devices", func(w http.ResponseWriter, r *http.Request) {
		a, ok := admin(w, r)
		if !ok {
			return
		}
		list, err := store.ListDevices(r.Context(), svc.Pool, a.WorkspaceID, 0)
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
	mux.HandleFunc("POST /api/v1/admin/devices/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		a, ok := admin(w, r)
		if !ok {
			return
		}
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		if err := svc.RevokeDevice(r.Context(), *a, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/v1/admin/invites", func(w http.ResponseWriter, r *http.Request) {
		a, ok := admin(w, r)
		if !ok {
			return
		}
		list, err := store.ListInvites(r.Context(), svc.Pool, a.WorkspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]map[string]any, 0, len(list))
		for _, i := range list {
			items = append(items, map[string]any{"id": i.ID, "kind": i.Kind, "role": i.Role, "max_uses": i.MaxUses, "uses": i.Uses,
				"expires_at": i.ExpiresAt, "revoked_at": i.RevokedAt, "created_at": i.CreatedAt})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	mux.HandleFunc("POST /api/v1/admin/invites", func(w http.ResponseWriter, r *http.Request) {
		a, ok := admin(w, r)
		if !ok {
			return
		}
		var in struct {
			Kind     string `json:"kind"`
			Role     string `json:"role"`
			MaxUses  int    `json:"max_uses"`
			TTLHours int    `json:"ttl_hours"`
		}
		if !decode(w, r, &in) {
			return
		}
		inv, err := svc.CreateInvite(r.Context(), a, a.WorkspaceID, core.InviteSpec{Kind: in.Kind, Admin: in.Role == store.RoleAdmin,
			MaxUses: in.MaxUses, TTL: time.Duration(in.TTLHours) * time.Hour})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": inv.Invite.ID, "code": inv.Code, "url": origin(r) + "/i/" + inv.Code})
	})
	mux.HandleFunc("POST /api/v1/admin/invites/{id}/revoke", func(w http.ResponseWriter, r *http.Request) {
		a, ok := admin(w, r)
		if !ok {
			return
		}
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		if err := svc.RevokeInvite(r.Context(), a, a.WorkspaceID, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/v1/admin/workspace", func(w http.ResponseWriter, r *http.Request) {
		a, ok := admin(w, r)
		if !ok {
			return
		}
		ws, err := store.GetWorkspace(r.Context(), svc.Pool, a.WorkspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": ws.Name, "public_url": ws.PublicURL, "telegram_chat": ws.Notify.Target,
			"show_title": ws.Notify.ShowTitle, "show_brief": ws.Notify.ShowBrief, "bot_enabled": botName(opts) != ""})
	})
	mux.HandleFunc("POST /api/v1/admin/workspace", func(w http.ResponseWriter, r *http.Request) {
		a, ok := admin(w, r)
		if !ok {
			return
		}
		var in struct {
			PublicURL    string `json:"public_url"`
			TelegramChat string `json:"telegram_chat"`
			ShowTitle    bool   `json:"show_title"`
			ShowBrief    bool   `json:"show_brief"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := svc.UpdateWorkspace(r.Context(), a, a.WorkspaceID, core.WorkspaceSettings{PublicURL: in.PublicURL,
			TelegramChat: in.TelegramChat, ShowTitle: in.ShowTitle, ShowBrief: in.ShowBrief}); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
