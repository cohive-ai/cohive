// Package httpapi 提供看板使用的只读 JSON 接口。
package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/cohive-ai/cohive/internal/authn"
	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/view"
)

type MemberView struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
}

type BoardIssue struct {
	view.IssueView
	Holder string `json:"holder,omitempty"`
	Needs  string `json:"needs"`
}

type boardResult struct {
	Me      string       `json:"me"`
	Members []MemberView `json:"members"`
	Issues  []BoardIssue `json:"issues"`
}

type listResult struct {
	Items []view.IssueView `json:"items"`
}

type detailResult struct {
	Issue  view.IssueView   `json:"issue"`
	Events []view.EventView `json:"events"`
}

// Handler 返回 /api/v1 下的接口。除登录码换取外都需要令牌。
func Handler(svc *core.Service) http.Handler {
	outer := http.NewServeMux()
	limiter := &failureLimiter{max: 30, window: time.Minute}
	outer.HandleFunc("POST /api/v1/login", func(w http.ResponseWriter, r *http.Request) {
		if limiter.blocked() {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "尝试次数过多，请一分钟后再试"})
			return
		}
		var in struct {
			Code string `json:"code"`
			Tool string `json:"tool"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式不对"})
			return
		}
		token, who, err := svc.ExchangeLoginCode(r.Context(), in.Code, in.Tool)
		if errors.Is(err, core.ErrBadLoginCode) {
			limiter.fail()
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error() + "，请私聊机器人重新发 /login 领取"})
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		ws, err := store.GetWorkspace(r.Context(), svc.Pool, who.WorkspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{
			"token": token, "member": who.MemberName, "workspace": ws.Name, "tool": who.Tool,
		})
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/me", func(w http.ResponseWriter, r *http.Request) {
		who := identity(r)
		writeJSON(w, http.StatusOK, map[string]string{"member": who.MemberName, "tool": who.Tool})
	})
	mux.HandleFunc("GET /api/v1/board", func(w http.ResponseWriter, r *http.Request) {
		who := identity(r)
		members, items, err := svc.Board(r.Context(), who)
		if err != nil {
			writeError(w, err)
			return
		}
		out := boardResult{Me: who.MemberName, Members: make([]MemberView, 0, len(members)), Issues: make([]BoardIssue, 0, len(items))}
		for _, m := range members {
			out.Members = append(out.Members, MemberView{Name: m.Name, DisplayName: m.DisplayName})
		}
		for _, it := range items {
			out.Issues = append(out.Issues, BoardIssue{IssueView: view.Issue(who, it.Issue), Holder: it.HolderName, Needs: it.Needs})
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /api/v1/issues", func(w http.ResponseWriter, r *http.Request) {
		who := identity(r)
		q := r.URL.Query()
		list, err := svc.List(r.Context(), who, q.Get("q"), q.Get("service"))
		if err != nil {
			writeError(w, err)
			return
		}
		out := listResult{Items: []view.IssueView{}}
		status := q.Get("status")
		for _, is := range list {
			if status == "" || string(is.Status) == status {
				out.Items = append(out.Items, view.Issue(who, is))
			}
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /api/v1/issues/{id}", func(w http.ResponseWriter, r *http.Request) {
		who := identity(r)
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "问题单编号不合法"})
			return
		}
		is, evs, err := svc.Get(r.Context(), who, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detailResult{Issue: view.Issue(who, is), Events: view.Events(evs)})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "没有这个接口"})
	})
	outer.Handle("/api/", authn.Middleware(svc.Pool)(mux))
	return outer
}

// failureLimiter 统计一个时间窗口内的失败次数，超过上限后拒绝所有换取请求，防止暴力猜登录码。
// 登录码约 40 位熵、10 分钟有效，全局每分钟 30 次失败足以让猜中的概率可以忽略。
type failureLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	start    time.Time
	failures int
}

func (l *failureLimiter) roll(now time.Time) {
	if now.Sub(l.start) >= l.window {
		l.start, l.failures = now, 0
	}
}

func (l *failureLimiter) blocked() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roll(time.Now())
	return l.failures >= l.max
}

func (l *failureLimiter) fail() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roll(time.Now())
	l.failures++
}

func identity(r *http.Request) store.Identity {
	who, _ := authn.FromContext(r.Context()) // 中间件已保证存在
	return who
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var ie *core.InputError
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.As(err, &ie):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		log.Printf("接口内部错误：%v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误，请稍后重试"})
	}
}
