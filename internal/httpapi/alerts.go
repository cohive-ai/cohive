package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
)

type alertJSON struct {
	ID      int64     `json:"id"`
	IssueID int64     `json:"issue_id"`
	Kind    string    `json:"kind"`
	Actor   string    `json:"actor"`
	At      time.Time `json:"at"`
}

type pendingJSON struct {
	IssueID int64  `json:"issue_id"`
	Needs   string `json:"needs"`
}

type alertsResult struct {
	Cursor  int64       `json:"cursor"`
	Items   []alertJSON `json:"items"`
	More    bool        `json:"more"`
	Pending struct {
		Count int           `json:"count"`
		Top   []pendingJSON `json:"top"`
	} `json:"pending"`
}

// alertRoutes 提供编程工具会话钩子拉取“与我有关的新动态”的接口。只读；不返回标题、简述、正文、备注。
func alertRoutes(mux *http.ServeMux, svc *core.Service) {
	handle(mux, "GET /api/v1/w/{ws}/alerts", aSession|aToken, func(w http.ResponseWriter, r *http.Request) {
		who, ok := memberScope(w, r, svc)
		if !ok {
			return
		}
		var after *int64
		if v := r.URL.Query().Get("after"); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "after 应为非负整数"})
				return
			}
			after = &n
		}
		res, err := svc.Alerts(r.Context(), who, after)
		if err != nil {
			writeError(w, err)
			return
		}
		out := alertsResult{Cursor: res.Cursor, More: res.More, Items: make([]alertJSON, 0, len(res.Items))}
		for _, a := range res.Items {
			out.Items = append(out.Items, alertJSON{ID: a.ID, IssueID: a.IssueID, Kind: a.Kind, Actor: a.Actor, At: a.At})
		}
		out.Pending.Count = res.PendingCount
		out.Pending.Top = make([]pendingJSON, 0, len(res.PendingTop))
		for _, p := range res.PendingTop {
			out.Pending.Top = append(out.Pending.Top, pendingJSON{IssueID: p.IssueID, Needs: p.Needs})
		}
		writeJSON(w, http.StatusOK, out)
	})
}
