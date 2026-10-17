package httpapi

import (
	"context"
	"net/http"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// serverRoutes 注册服务器管理的接口（前缀 /api/v1/server）：只认密码登录的网页会话，操作者须为服务器管理员。
func serverRoutes(mux *http.ServeMux, svc *core.Service, opts Options) {
	srv := func(pattern string, h func(w http.ResponseWriter, r *http.Request, actor *store.Account)) {
		handle(mux, pattern, aPassword, func(w http.ResponseWriter, r *http.Request) {
			a := caller(r).Account
			h(w, r, &a)
		})
	}
	// target 按登录名找到账号；不存在时已写好 404。
	target := func(w http.ResponseWriter, r *http.Request) (store.Account, bool) {
		a, err := svc.AccountByName(r.Context(), r.PathValue("name"))
		if err != nil {
			writeError(w, err)
			return a, false
		}
		return a, true
	}
	const p = "/api/v1/server"
	settingsRoutes(srv, svc)

	srv("GET "+p+"/workspaces", func(w http.ResponseWriter, r *http.Request, actor *store.Account) {
		list, err := svc.ServerWorkspaces(r.Context(), actor)
		if err != nil {
			writeError(w, err)
			return
		}
		ms, err := svc.Memberships(r.Context(), actor.ID)
		if err != nil {
			writeError(w, err)
			return
		}
		mine := map[int64]bool{}
		for _, m := range ms {
			mine[m.WorkspaceID] = true
		}
		items := make([]map[string]any, 0, len(list))
		for _, ws := range list {
			items = append(items, map[string]any{"name": ws.Name, "created_at": ws.CreatedAt, "members": ws.Members,
				"admins": ws.Admins, "open_issues": ws.OpenIssues, "telegram_bound": ws.TelegramBound, "i_am_member": mine[ws.ID]})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	srv("POST "+p+"/workspaces", func(w http.ResponseWriter, r *http.Request, actor *store.Account) {
		var in struct {
			Name  string `json:"name"`
			Admin string `json:"admin"`
		}
		if !decode(w, r, &in) {
			return
		}
		ws, err := svc.CreateWorkspace(r.Context(), core.WebActor(*actor), in.Name, in.Admin)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]string{"name": ws.Name})
	})
	srv("DELETE "+p+"/workspaces/{name}", func(w http.ResponseWriter, r *http.Request, actor *store.Account) {
		if err := svc.DeleteWorkspace(r.Context(), core.WebActor(*actor), r.PathValue("name")); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	srv("GET "+p+"/accounts", func(w http.ResponseWriter, r *http.Request, actor *store.Account) {
		list, err := svc.ServerAccounts(r.Context(), actor, r.URL.Query().Get("q"))
		if err != nil {
			writeError(w, err)
			return
		}
		items := make([]map[string]any, 0, len(list))
		for _, a := range list {
			wss := make([]map[string]string, 0, len(a.Memberships))
			for _, m := range a.Memberships {
				wss = append(wss, map[string]string{"name": m.Workspace, "role": m.Role, "status": m.Status})
			}
			items = append(items, map[string]any{"name": a.Name, "display_name": a.DisplayName, "server_admin": a.ServerAdmin,
				"disabled": a.Disabled(), "telegram_linked": a.TelegramUserID != nil, "devices": a.Devices, "workspaces": wss})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	srv("POST "+p+"/accounts/{name}", func(w http.ResponseWriter, r *http.Request, actor *store.Account) {
		var in struct {
			ServerAdmin *bool `json:"server_admin"`
		}
		if !decode(w, r, &in) {
			return
		}
		t, ok := target(w, r)
		if !ok {
			return
		}
		if in.ServerAdmin != nil {
			if err := svc.SetServerAdmin(r.Context(), core.WebActor(*actor), t.ID, *in.ServerAdmin); err != nil {
				writeError(w, err)
				return
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv("POST "+p+"/accounts/{name}/disable", func(w http.ResponseWriter, r *http.Request, actor *store.Account) {
		t, ok := target(w, r)
		if !ok {
			return
		}
		if err := svc.DisableAccount(r.Context(), core.WebActor(*actor), t.ID); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv("POST "+p+"/accounts/{name}/enable", func(w http.ResponseWriter, r *http.Request, actor *store.Account) {
		t, ok := target(w, r)
		if !ok {
			return
		}
		if err := svc.EnableAccount(r.Context(), core.WebActor(*actor), t.ID); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	srv("DELETE "+p+"/accounts/{name}/telegram", func(w http.ResponseWriter, r *http.Request, actor *store.Account) {
		t, ok := target(w, r)
		if !ok {
			return
		}
		if err := svc.UnlinkTelegram(r.Context(), core.WebActor(*actor), t.ID); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	srv("GET "+p+"/pending-devices", func(w http.ResponseWriter, r *http.Request, actor *store.Account) {
		devs, err := svc.PendingDeviceAuths(r.Context(), actor, 0)
		if err != nil {
			writeError(w, err)
			return
		}
		items, err := pendingDevicesJSON(r.Context(), svc, devs)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	})
	srv("POST "+p+"/device-auths/{id}", func(w http.ResponseWriter, r *http.Request, actor *store.Account) {
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
		if err := requireServerAdminHTTP(r.Context(), svc, actor); err != nil {
			writeError(w, err)
			return
		}
		d, err := svc.DecideDeviceAuth(r.Context(), core.WebActor(*actor), 0, id, in.Approve)
		if err != nil {
			writeError(w, err)
			return
		}
		announce(opts, func(ctx context.Context, a Announcer) error { return a.AnnounceDeviceDecided(ctx, d) })
		w.WriteHeader(http.StatusNoContent)
	})
}

// settingsRoutes 注册服务器级设置（对外地址）的接口。
func settingsRoutes(srv func(string, func(http.ResponseWriter, *http.Request, *store.Account)), svc *core.Service) {
	const p = "/api/v1/server/settings"
	out := func(w http.ResponseWriter, r *http.Request, ext core.ExternalURL) {
		// suggested 是本次访问用的地址：还没设时网页拿它预填，人确认后才保存
		writeJSON(w, http.StatusOK, map[string]any{"external_url": ext.URL, "locked": ext.Locked, "suggested": origin(r)})
	}
	srv("GET "+p, func(w http.ResponseWriter, r *http.Request, actor *store.Account) {
		if err := requireServerAdminHTTP(r.Context(), svc, actor); err != nil {
			writeError(w, err)
			return
		}
		ext, err := svc.ExternalURL(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		out(w, r, ext)
	})
	srv("POST "+p, func(w http.ResponseWriter, r *http.Request, actor *store.Account) {
		var in struct {
			ExternalURL string `json:"external_url"`
		}
		if !decode(w, r, &in) {
			return
		}
		ext, err := svc.SetExternalURL(r.Context(), core.WebActor(*actor), in.ExternalURL)
		if err != nil {
			writeError(w, err)
			return
		}
		out(w, r, ext)
	})
}

// requireServerAdminHTTP 在服务器管理页上额外确认操作者是服务器管理员
// （DecideDeviceAuth 在 wsID 为 0 时也接受“管得到此人”的工作空间管理员，这里只给服务器管理员用）。
func requireServerAdminHTTP(ctx context.Context, svc *core.Service, actor *store.Account) error {
	a, err := svc.Account(ctx, actor.ID)
	if err != nil {
		return err
	}
	if !a.ServerAdmin || a.Disabled() {
		return core.ErrNotServerAdmin
	}
	return nil
}
