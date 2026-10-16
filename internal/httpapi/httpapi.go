// Package httpapi 提供看板使用的只读 JSON 接口。
package httpapi

import (
	"encoding/json"
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

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

// Options 是接口层的可选依赖。
type Options struct {
	// BotName 返回 Telegram 机器人用户名；为 nil 或返回空时，通讯工具连接不可用。
	BotName func() string
	// Announcer 在有新的待审申请时通知管理员（例如发到工作空间群里）；可为空。
	Announcer Announcer
}

// Handler 返回 /api/v1 与 /auth/ 下的接口。网页用会话 Cookie，命令行与编程工具用令牌；
// 登录、注册、设备授权的发起与轮询不需要身份。
func Handler(svc *core.Service, opts Options) http.Handler {
	outer := http.NewServeMux()
	publicRoutes(outer, svc, opts)

	mux := http.NewServeMux()
	accountRoutes(mux, svc, opts)
	adminRoutes(mux, svc, opts)
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
	outer.Handle("/api/", authMiddleware(svc, mux))
	return outer
}

func identity(r *http.Request) store.Identity {
	return caller(r).Identity // 中间件已保证存在
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
	case errors.Is(err, core.ErrNotAdmin):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.Is(err, core.ErrDecided):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, core.ErrInviteInvalid), errors.Is(err, core.ErrAuthGone), errors.Is(err, core.ErrLinkGone):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, core.ErrNoDevice):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.As(err, &ie):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		log.Printf("接口内部错误：%v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误，请稍后重试"})
	}
}

// keyedLimiter 按键（来源地址）限制一个时间窗口内的次数。
type keyedLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	start  time.Time
	counts map[string]int
}

func (l *keyedLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now := time.Now(); l.counts == nil || now.Sub(l.start) >= l.window {
		l.start, l.counts = now, map[string]int{}
	}
	if l.counts[key] >= l.max {
		return false
	}
	l.counts[key]++
	return true
}

// clientIP 返回请求的来源地址。来自本机（例如 Cloudflare 隧道的 cloudflared）且带 CF-Connecting-IP 时，
// 用这个头里的真实地址；其余情况不信任转发头，避免被伪造来绕过限频。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		if cf := net.ParseIP(r.Header.Get("CF-Connecting-IP")); cf != nil {
			return cf.String()
		}
	}
	return host
}
