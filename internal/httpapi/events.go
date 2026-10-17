package httpapi

import (
	"net/http"
	"strconv"

	"github.com/cohive-ai/cohive/internal/core"
)

// eventQuery 解析查看记录的查询参数；不合法时已写好 400。
func eventQuery(w http.ResponseWriter, r *http.Request) (core.EventQuery, bool) {
	v := r.URL.Query()
	q := core.EventQuery{Workspace: v.Get("workspace"), Category: v.Get("type"), Actor: v.Get("actor"), Via: v.Get("via")}
	num := func(name string, dst *int64) bool {
		s := v.Get(name)
		if s == "" {
			return true
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n < 1 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": name + " 应为正整数"})
			return false
		}
		*dst = n
		return true
	}
	var limit int64
	if !num("before", &q.Before) || !num("limit", &limit) {
		return q, false
	}
	q.Limit = int(limit)
	return q, true
}

func eventsJSON(p core.EventPage) map[string]any {
	items := make([]map[string]any, 0, len(p.Items))
	for _, e := range p.Items {
		detail := make([]map[string]string, 0, len(e.Detail))
		for _, d := range core.EventDetail(e) {
			detail = append(detail, map[string]string{"label": d.Label, "value": d.Value})
		}
		items = append(items, map[string]any{
			"id": e.ID, "at": e.At, "via": e.Via, "via_label": core.ViaLabels[e.Via], "actor": e.ActorName,
			"workspace": e.WorkspaceName, "action": e.Action, "action_label": core.ActionLabel(e.Action),
			"category": core.ActionCategory(e.Action), "target_kind": e.TargetKind, "target": e.TargetName,
			"text": core.EventText(e), "detail": detail,
		})
	}
	var next any
	if p.NextBefore > 0 {
		next = p.NextBefore
	}
	out := eventMeta()
	out["items"], out["next_before"] = items, next
	return out
}

// eventRoutes 注册查看管理操作记录的接口：工作空间管理页只看本空间的，服务器管理页看全部。
func eventRoutes(mux *http.ServeMux, svc *core.Service) {
	handle(mux, "GET /api/v1/w/{ws}/admin/events", aPassword, func(w http.ResponseWriter, r *http.Request) {
		ws, actor, ok := adminScope(w, r, svc)
		if !ok {
			return
		}
		q, ok := eventQuery(w, r)
		if !ok {
			return
		}
		q.Workspace = ""
		p, err := svc.WorkspaceEvents(r.Context(), actor, ws.ID, q)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, eventsJSON(p))
	})
	handle(mux, "GET /api/v1/server/events", aPassword, func(w http.ResponseWriter, r *http.Request) {
		q, ok := eventQuery(w, r)
		if !ok {
			return
		}
		a := caller(r).Account
		p, err := svc.ServerEvents(r.Context(), &a, q)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, eventsJSON(p))
	})
}

// eventMeta 供网页筛选用：动作类别与入口的中文名。
func eventMeta() map[string]any {
	cats := make([]map[string]string, 0, len(core.Categories))
	for _, c := range core.Categories {
		cats = append(cats, map[string]string{"id": c.ID, "label": c.Label})
	}
	vias := make([]map[string]string, 0, len(core.ViaLabels))
	for _, v := range []string{core.ViaWeb, core.ViaTelegram, core.ViaServerCLI, core.ViaMemberCLI, core.ViaSystem, core.ViaUnknown} {
		vias = append(vias, map[string]string{"id": v, "label": core.ViaLabels[v]})
	}
	return map[string]any{"types": cats, "vias": vias}
}

