package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

func handoverJSON(h core.Handover) map[string]any {
	issues := make([]map[string]any, 0, len(h.Issues))
	for _, it := range h.Issues {
		issues = append(issues, map[string]any{"id": it.IssueID, "title": it.Title, "status": string(it.Status),
			"status_label": it.Status.Label(), "as": it.As, "version": it.Version})
	}
	services := make([]map[string]string, 0, len(h.Services))
	for _, n := range h.Services {
		services = append(services, map[string]string{"name": n})
	}
	return map[string]any{"member_id": h.Member.ID, "account": h.Member.Name, "issues": issues, "services": services}
}

// adminRoutes 注册工作空间管理页的接口（前缀 /api/v1/w/{ws}/admin）：只认密码登录的网页会话，
// 操作者须为该工作空间管理员或服务器管理员（业务层判定）。
func adminRoutes(mux *http.ServeMux, svc *core.Service, opts Options) {
	adm := func(pattern string, h func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account)) {
		handle(mux, pattern, aPassword, func(w http.ResponseWriter, r *http.Request) {
			ws, actor, ok := adminScope(w, r, svc)
			if !ok {
				return
			}
			h(w, r, ws, actor)
		})
	}
	const p = "/api/v1/w/{ws}/admin"

	adm("GET "+p+"/pending", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		signups, err := svc.PendingSignups(r.Context(), actor, ws.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		devs, err := svc.PendingDeviceAuths(r.Context(), actor, ws.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		so := make([]map[string]any, 0, len(signups))
		for _, s := range signups {
			kind := "new"
			if s.Existing {
				kind = "existing"
			}
			so = append(so, map[string]any{"id": s.ID, "kind": kind, "name": s.Name, "display_name": s.DisplayName, "created_at": s.CreatedAt})
		}
		do, err := pendingDevicesJSON(r.Context(), svc, devs)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"signups": so, "devices": do})
	})

	decide := func(fn func(r *http.Request, ws store.Workspace, actor *store.Account, id int64, approve bool) error) func(http.ResponseWriter, *http.Request, store.Workspace, *store.Account) {
		return func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
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
			if err := fn(r, ws, actor, id, in.Approve); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
	adm("POST "+p+"/signups/{id}", decide(func(r *http.Request, ws store.Workspace, actor *store.Account, id int64, approve bool) error {
		d, err := svc.DecideSignup(r.Context(), core.WebActor(*actor), ws.ID, id, approve)
		if err == nil {
			announce(opts, func(ctx context.Context, a Announcer) error {
				if d.Member != nil {
					if err := a.AnnounceJoined(ctx, *d.Member); err != nil {
						return err
					}
				}
				return a.AnnounceSignupDecided(ctx, d)
			})
		}
		return err
	}))
	adm("POST "+p+"/device-auths/{id}", decide(func(r *http.Request, ws store.Workspace, actor *store.Account, id int64, approve bool) error {
		d, err := svc.DecideDeviceAuth(r.Context(), core.WebActor(*actor), ws.ID, id, approve)
		if err == nil {
			announce(opts, func(ctx context.Context, a Announcer) error { return a.AnnounceDeviceDecided(ctx, d) })
		}
		return err
	}))

	adm("GET "+p+"/members", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		list, err := svc.AdminMembers(r.Context(), actor, ws.ID, r.URL.Query().Get("status"))
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]map[string]any, 0, len(list))
		for _, m := range list {
			items = append(items, map[string]any{"member_id": m.ID, "account": m.Name, "display_name": m.DisplayName,
				"role": m.Role, "status": m.Status, "disabled": m.AccountDisabled,
				"telegram_username": m.TelegramUsername, "telegram_linked": m.TelegramUserID != nil, "devices": m.Devices,
				"has_password": m.HasPassword, "pending_issues": m.PendingIssues, "owned_services": m.OwnedServices})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	adm("GET "+p+"/accounts/{name}", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		l, err := svc.LookupAccount(r.Context(), actor, ws.ID, r.PathValue("name"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": l.Account.Name, "display_name": l.Account.DisplayName,
			"member_status": l.MemberStatus, "disabled": l.Account.Disabled()})
	})
	adm("POST "+p+"/members", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		var in struct {
			Account string `json:"account"`
			Role    string `json:"role"`
		}
		if !decode(w, r, &in) {
			return
		}
		m, err := svc.AddMember(r.Context(), core.WebActor(*actor), ws.ID, in.Account, in.Role)
		if err != nil {
			writeError(w, err)
			return
		}
		announce(opts, func(ctx context.Context, a Announcer) error { return a.AnnounceJoined(ctx, m) })
		writeJSON(w, http.StatusCreated, map[string]any{"member_id": m.ID})
	})
	adm("POST "+p+"/members/{id}", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
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
			if err := svc.SetRole(r.Context(), core.WebActor(*actor), ws.ID, id, in.Role); err != nil {
				writeError(w, err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	adm("GET "+p+"/members/{id}/handover", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		h, err := svc.GetHandover(r.Context(), actor, ws.ID, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, handoverJSON(h))
	})
	applyHandover := func(remove bool) func(http.ResponseWriter, *http.Request, store.Workspace, *store.Account) {
		return func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
			id, ok := pathID(w, r, "id")
			if !ok {
				return
			}
			var in struct {
				Issues []struct {
					ID              int64  `json:"id"`
					As              string `json:"as"`
					ToMemberID      int64  `json:"to_member_id"`
					ExpectedVersion int    `json:"expected_version"`
				} `json:"issues"`
				Services []struct {
					Name       string `json:"name"`
					ToMemberID int64  `json:"to_member_id"`
				} `json:"services"`
			}
			if !decodeLimit(w, r, &in, 256<<10) {
				return
			}
			var plan core.HandoverPlan
			for _, it := range in.Issues {
				plan.Issues = append(plan.Issues, core.PlanIssue{IssueID: it.ID, As: it.As, ToMemberID: it.ToMemberID, ExpectedVersion: it.ExpectedVersion})
			}
			for _, sv := range in.Services {
				plan.Services = append(plan.Services, core.PlanService{Name: sv.Name, ToMemberID: sv.ToMemberID})
			}
			if err := svc.ApplyHandover(r.Context(), core.WebActor(*actor), ws.ID, id, plan, remove); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
	adm("POST "+p+"/members/{id}/handover", applyHandover(false))
	adm("POST "+p+"/members/{id}/remove", applyHandover(true))

	adm("GET "+p+"/devices", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		list, err := svc.WorkspaceDevices(r.Context(), actor, ws.ID)
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
	adm("POST "+p+"/devices/{id}/revoke", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		if err := svc.RevokeDevice(r.Context(), core.WebActor(*actor), ws.ID, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	adm("GET "+p+"/invites", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		list, err := svc.ListInvites(r.Context(), actor, ws.ID)
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
	adm("POST "+p+"/invites", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		var in struct {
			Kind     string `json:"kind"`
			Role     string `json:"role"`
			MaxUses  int    `json:"max_uses"`
			TTLHours int    `json:"ttl_hours"`
		}
		if !decode(w, r, &in) {
			return
		}
		inv, err := svc.CreateInvite(r.Context(), core.WebActor(*actor), ws.ID, core.InviteSpec{Kind: in.Kind, Admin: in.Role == store.RoleAdmin,
			MaxUses: in.MaxUses, TTL: time.Duration(in.TTLHours) * time.Hour})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": inv.Invite.ID, "code": inv.Code, "url": origin(r) + "/i/" + inv.Code})
	})
	adm("POST "+p+"/invites/{id}/revoke", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		if err := svc.RevokeInvite(r.Context(), core.WebActor(*actor), ws.ID, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// 群绑定码（钉钉）：生成后在群里 @机器人 发“绑定群 绑定码”
	adm("POST "+p+"/chat-binds", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		var in struct {
			Provider string `json:"provider"`
		}
		if !decode(w, r, &in) {
			return
		}
		if !providerEnabled(opts, in.Provider) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "协作服务没有启用这种通讯工具"})
			return
		}
		b, err := svc.StartChatBind(r.Context(), core.WebActor(*actor), ws.ID, in.Provider)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": b.ID, "code": b.Code, "expires_in": int(time.Until(b.ExpiresAt).Seconds())})
	})
	adm("GET "+p+"/chat-binds/{id}", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		b, err := svc.ChatBindStatus(r.Context(), core.WebActor(*actor), ws.ID, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": b.Status, "detail": b.Detail})
	})
	adm("DELETE "+p+"/chats/{provider}", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		if err := svc.UnbindChat(r.Context(), core.WebActor(*actor), ws.ID, r.PathValue("provider")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	adm("GET "+p+"/workspace", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		ws, err := svc.AdminWorkspace(r.Context(), actor, ws.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		ext, err := svc.ExternalURL(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		// 群消息里问题单链接的样子：对外地址是服务器级的，这里只读显示；没设时为空，页面提示去服务器管理设置
		example := ""
		if ext.URL != "" {
			example = core.WorkspaceLink(ext.URL, ws.Name) + "/issues/编号"
		}
		writeJSON(w, http.StatusOK, map[string]any{"name": ws.Name, "path": ws.Name, "display_name": ws.Label(),
			"telegram_chat": telegramChat(ws), "show_title": ws.Notify.ShowTitle, "show_brief": ws.Notify.ShowBrief,
			"bot_enabled": botName(opts) != "", "external_url_set": ext.URL != "", "issue_link_example": example,
			"chat": workspaceChatJSON(r.Context(), svc, ws), "channels": enabledProviders(opts)})
	})
	adm("POST "+p+"/workspace", func(w http.ResponseWriter, r *http.Request, ws store.Workspace, actor *store.Account) {
		// 旧页面还会带 public_url：对外地址已改为服务器级，忽略它
		var in struct {
			TelegramChat string  `json:"telegram_chat"`
			ShowTitle    bool    `json:"show_title"`
			ShowBrief    bool    `json:"show_brief"`
			DisplayName  *string `json:"display_name"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := svc.UpdateWorkspace(r.Context(), core.WebActor(*actor), ws.ID, core.WorkspaceSettings{
			TelegramChat: in.TelegramChat, ShowTitle: in.ShowTitle, ShowBrief: in.ShowBrief, DisplayName: in.DisplayName}); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// telegramChat 是工作空间绑定的 Telegram 群编号；绑的是别的通讯工具的群时为空。
func telegramChat(ws store.Workspace) string {
	if ws.Notify.Channel != store.ProviderTelegram {
		return ""
	}
	return ws.Notify.Target
}

// workspaceChatJSON 是工作空间绑定的群（第一期最多一个）；没绑时为 nil。
func workspaceChatJSON(ctx context.Context, svc *core.Service, ws store.Workspace) map[string]string {
	chats, err := svc.WorkspaceChats(ctx, ws.ID)
	if err != nil || len(chats) == 0 {
		return nil
	}
	c := chats[0]
	return map[string]string{"provider": c.Provider, "chat_id": c.ChatID, "title": c.Title}
}

// pendingDevicesJSON 是待批设备列表，每条列出申请人全部在册工作空间（批准后可用于这些）。
func pendingDevicesJSON(ctx context.Context, svc *core.Service, devs []store.DeviceAuth) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(devs))
	for _, d := range devs {
		var wss []string
		if d.AccountID != nil {
			ms, err := svc.Memberships(ctx, *d.AccountID)
			if err != nil {
				return nil, err
			}
			for _, m := range ms {
				wss = append(wss, m.Label)
			}
		}
		if wss == nil {
			wss = []string{}
		}
		out = append(out, map[string]any{"id": d.ID, "account": d.AccountName, "display_name": d.DisplayName,
			"device_name": d.DeviceName, "tools": d.Tools, "created_at": d.CreatedAt, "workspaces": wss})
	}
	return out, nil
}
