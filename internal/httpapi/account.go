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
		writeJSON(w, http.StatusBadRequest, apiErr(w, "invalid_id", "api.id_invalid"))
		return 0, false
	}
	return id, true
}

// deviceJSON 是设备列表的一行；tokens 按工作空间列出仍有效的令牌。
func deviceJSON(d store.Device) map[string]any {
	tokens := make([]map[string]string, 0, len(d.Tokens))
	workspaces := []string{}
	seen := map[string]bool{}
	for _, t := range d.Tokens {
		tokens = append(tokens, map[string]string{"workspace": t.Workspace, "tool": t.Tool})
		if !seen[t.Workspace] {
			seen[t.Workspace] = true
			workspaces = append(workspaces, t.Workspace)
		}
	}
	return map[string]any{"id": d.ID, "account": d.AccountName, "name": d.Name, "created_at": d.CreatedAt,
		"revoked_at": d.RevokedAt, "last_used_at": d.LastUsedAt, "tokens": tokens, "workspaces": workspaces}
}

// accountJSON 是接口约定里的 Account 结构。
func accountJSON(a store.Account, hasPassword bool) map[string]any {
	return map[string]any{"id": a.ID, "name": a.Name, "display_name": a.DisplayName, "server_admin": a.ServerAdmin,
		"disabled": a.Disabled(), "has_password": hasPassword,
		"telegram": map[string]any{"username": a.TelegramUsername, "linked": a.TelegramUserID != nil}}
}

// chatJSON 是账号在各通讯工具上的连接（linked 表示有用户编号，能识别他在群里点的按钮）。
func chatJSON(ids []store.ChatIdentity) []map[string]any {
	out := make([]map[string]any, 0, len(ids))
	for _, c := range ids {
		out = append(out, map[string]any{"provider": c.Provider, "linked": c.UserID != "", "handle": c.Handle,
			"display_name": c.DisplayName})
	}
	return out
}

func workspaceRefs(ms []store.Membership) []map[string]string {
	out := make([]map[string]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, map[string]string{"name": m.Workspace, "display_name": m.Label, "role": m.Role})
	}
	return out
}

func botName(opts Options) string {
	if opts.BotName == nil {
		return ""
	}
	return opts.BotName()
}

// accountRoutes 注册账号自己的接口：个人信息、改密码、设备、通讯工具连接、设备授权页、接受邀请。
func accountRoutes(mux *http.ServeMux, svc *core.Service, opts Options) {
	handle(mux, "GET /api/v1/me", aSession|aToken, func(w http.ResponseWriter, r *http.Request) {
		c := caller(r)
		pw, err := svc.PasswordStatus(r.Context(), c.Account.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		ms, err := svc.Memberships(r.Context(), c.Account.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		chats, err := svc.ChatIdentities(r.Context(), []int64{c.Account.ID})
		if err != nil {
			writeError(w, err)
			return
		}
		out := map[string]any{"account": accountJSON(c.Account, pw.Hash != ""), "must_change_password": pw.MustChange,
			"login_method": loginMethod(c), "tool": "web", "workspaces": workspaceRefs(ms),
			"chat": chatJSON(chats[c.Account.ID]), "channels": enabledProviders(opts)}
		if c.Token != nil {
			out["tool"] = c.Token.Tool
			for _, m := range ms {
				if m.WorkspaceID == c.Token.WorkspaceID {
					out["workspace"] = map[string]string{"name": m.Workspace, "display_name": m.Label, "role": m.Role}
				}
			}
		}
		writeJSON(w, http.StatusOK, out)
	})

	handle(mux, "POST /api/v1/auth/password", aSession, func(w http.ResponseWriter, r *http.Request) {
		// 只能在网页上改：令牌在终端和编程工具里，不能拿它给没设过密码的账号直接设密码
		var in struct {
			Old string `json:"old_password"`
			New string `json:"new_password"`
		}
		if !decode(w, r, &in) {
			return
		}
		c := caller(r)
		if err := svc.ChangePassword(r.Context(), c.Account, c.Session, in.Old, in.New); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// cohive web：设备密钥（或 M5 配对的旧电脑上属于设备的令牌）换一次性网页登录链接
	handle(mux, "POST /api/v1/auth/web-login", aDevice|aDeviceToken, func(w http.ResponseWriter, r *http.Request) {
		code, err := svc.CreateWebLoginCode(r.Context(), caller(r).Account.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"url": origin(r) + "/auth/once/" + code})
	})

	// 设备授权页：只认密码登录的网页会话（免密会话可由本机智能体开出，不应能为新电脑点授权）
	handle(mux, "GET /api/v1/device/auth/{nonce}", aPassword, func(w http.ResponseWriter, r *http.Request) {
		v, err := svc.ViewDeviceAuth(r.Context(), caller(r).Account, r.PathValue("nonce"))
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"device_name": v.DeviceName, "tools": v.Tools, "created_at": v.CreatedAt,
			"status": v.Status, "first_device": v.FirstDevice, "workspaces": v.Workspaces})
	})
	handle(mux, "POST /api/v1/device/auth/{nonce}", aPassword, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Approve bool `json:"approve"`
		}
		if !decode(w, r, &in) {
			return
		}
		a, err := svc.AuthorizeDevice(r.Context(), caller(r).Account, r.PathValue("nonce"), in.Approve)
		if err != nil {
			writeError(w, err)
			return
		}
		if a.Status == store.AuthAwaitingAdmin {
			announce(opts, func(ctx context.Context, an Announcer) error { return an.AnnounceDevice(ctx, a) })
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": a.Status})
	})

	// 设备密钥：为本人在册的工作空间签发令牌（新加入工作空间后不必重新配对）
	handle(mux, "POST /api/v1/device/tokens", aDevice, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Workspace string   `json:"workspace"`
			Tools     []string `json:"tools"`
		}
		if !decode(w, r, &in) {
			return
		}
		c := caller(r)
		wt, err := svc.IssueWorkspaceTokens(r.Context(), *c.Device, in.Workspace, in.Tools)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"account": c.Account.Name, "workspace": wt.Workspace, "role": wt.Role,
			"tokens": wt.Tokens})
	})
	handle(mux, "GET /api/v1/device/workspaces", aDevice, func(w http.ResponseWriter, r *http.Request) {
		c := caller(r)
		ms, err := svc.Memberships(r.Context(), c.Account.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"account": c.Account.Name, "device_id": c.Device.ID, "items": workspaceRefs(ms)})
	})

	handle(mux, "GET /api/v1/me/devices", aSession, func(w http.ResponseWriter, r *http.Request) {
		list, err := svc.MyDevices(r.Context(), caller(r).Account.ID)
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
	// 网页个人中心；本机命令行重新配对后也用新设备密钥吊销本机旧设备（吊销是收紧权限的操作）
	handle(mux, "POST /api/v1/me/devices/{id}/revoke", aSession|aDevice, func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		// 个人中心只能吊销自己的设备：看不到别人的设备
		a := caller(r).Account
		d, err := svc.MyDevices(r.Context(), a.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		own := false
		for _, x := range d {
			own = own || x.ID == id
		}
		if !own {
			writeJSON(w, http.StatusNotFound, apiErr(w, "not_found", "api.device_not_found"))
			return
		}
		// 用设备密钥调用的是成员本机命令行（cohive login --force 吊销本机旧设备），其余是网页
		act := core.WebActor(a)
		if caller(r).Device != nil {
			act = core.MemberCLIActor(a)
		}
		if err := svc.RevokeDevice(r.Context(), act, 0, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	handle(mux, "POST /api/v1/me/links", aSession, func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Provider string `json:"provider"`
		}
		if !decode(w, r, &in) {
			return
		}
		name := botName(opts)
		if in.Provider == core.ProviderTelegram && name == "" {
			writeJSON(w, http.StatusServiceUnavailable, apiErr(w, "service_unavailable", "api.telegram_disabled"))
			return
		}
		if in.Provider == core.ProviderDingTalk && !providerEnabled(opts, core.ProviderDingTalk) {
			writeJSON(w, http.StatusServiceUnavailable, apiErr(w, "service_unavailable", "api.dingtalk_disabled"))
			return
		}
		l, err := svc.StartLink(r.Context(), caller(r).Account.ID, in.Provider)
		if err != nil {
			writeError(w, err)
			return
		}
		out := map[string]any{"id": l.ID, "expires_in": int(time.Until(l.ExpiresAt).Seconds())}
		if l.Provider == core.ProviderTelegram {
			out["url"] = "https://t.me/" + name + "?start=" + l.Nonce
		} else { // 钉钉：把绑定码私聊发给机器人，再回网页确认
			out["code"] = l.Nonce
		}
		writeJSON(w, http.StatusOK, out)
	})
	handle(mux, "GET /api/v1/me/links/{id}", aSession, func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		l, err := svc.LinkStatus(r.Context(), caller(r).Account.ID, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": l.Status, "detail": l.Detail, "provider": l.Provider,
			"candidate_name": l.CandidateName})
	})
	// 钉钉：机器人收到绑定码后等本人确认
	handle(mux, "POST /api/v1/me/links/{id}/confirm", aSession, func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		var in struct {
			Accept bool `json:"accept"`
		}
		if !decode(w, r, &in) {
			return
		}
		if err := svc.ConfirmLink(r.Context(), core.WebActor(caller(r).Account), id, in.Accept); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	handle(mux, "DELETE /api/v1/me/links/{provider}", aSession, func(w http.ResponseWriter, r *http.Request) {
		a := caller(r).Account
		if err := svc.Unlink(r.Context(), core.WebActor(a), a.ID, r.PathValue("provider")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// 已有账号接受邀请：只认密码登录的网页会话（会改变本人的归属，免密会话可由本机智能体开出）
	handle(mux, "POST /api/v1/invites/{code}/accept", aPassword, func(w http.ResponseWriter, r *http.Request) {
		res, err := svc.AcceptInvite(r.Context(), caller(r).Account, r.PathValue("code"))
		if err != nil {
			writeError(w, err)
			return
		}
		if res.Signup != nil {
			sg := *res.Signup
			announce(opts, func(ctx context.Context, a Announcer) error { return a.AnnounceSignup(ctx, sg) })
			writeJSON(w, http.StatusOK, map[string]any{"status": "pending", "workspace": map[string]string{"name": res.Workspace.Name,
				"display_name": res.Workspace.Label()}})
			return
		}
		m := *res.Member
		announce(opts, func(ctx context.Context, a Announcer) error { return a.AnnounceSelfJoined(ctx, m) })
		writeJSON(w, http.StatusOK, map[string]any{"status": "active",
			"workspace": map[string]string{"name": res.Workspace.Name, "display_name": res.Workspace.Label(), "role": m.Role}})
	})
}
