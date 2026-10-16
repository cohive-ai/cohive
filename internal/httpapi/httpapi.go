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
	Disabled    bool   `json:"disabled,omitempty"`
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
	// Announcer 在有新的待审申请、新成员加入时通知（例如发到工作空间群里）；可为空。
	Announcer Announcer
}

// Handler 返回 /api/v1 与 /auth/ 下的接口。网页用会话 Cookie，命令行与编程工具用令牌或设备密钥；
// 登录、注册、设备授权的发起与轮询不需要身份。
func Handler(svc *core.Service, opts Options) http.Handler {
	outer := http.NewServeMux()
	publicRoutes(outer, svc, opts)

	mux := http.NewServeMux()
	accountRoutes(mux, svc, opts)
	workspaceRoutes(mux, svc)
	adminRoutes(mux, svc, opts)
	serverRoutes(mux, svc, opts)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "没有这个接口"})
	})
	outer.Handle("/api/", authMiddleware(svc, mux))
	return outer
}

// errNoWorkspace 是工作空间不存在或不是它的成员时的统一提示，不区分是哪种情况。
var errNoWorkspace = map[string]string{"error": "工作空间不存在或你不是它的成员"}

// memberScope 解析 /w/{ws} 前缀：会话要求账号在该工作空间在册；令牌要求令牌就属于该工作空间。
// 返回本次请求在该工作空间里的身份；不满足时已写好 404。
func memberScope(w http.ResponseWriter, r *http.Request, svc *core.Service) (store.Identity, bool) {
	c := caller(r)
	name := r.PathValue("ws")
	ws, err := svc.WorkspaceByName(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errNoWorkspace)
		return store.Identity{}, false
	}
	if err != nil {
		writeError(w, err)
		return store.Identity{}, false
	}
	if c.Token != nil {
		if c.Token.WorkspaceID != ws.ID {
			writeJSON(w, http.StatusNotFound, errNoWorkspace)
			return store.Identity{}, false
		}
		return *c.Token, true
	}
	m, err := store.MemberOf(r.Context(), svc.Pool, c.Account.ID, ws.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && m.Status != store.MemberActive) {
		writeJSON(w, http.StatusNotFound, errNoWorkspace)
		return store.Identity{}, false
	}
	if err != nil {
		writeError(w, err)
		return store.Identity{}, false
	}
	return store.Identity{AccountID: c.Account.ID, MemberID: m.ID, WorkspaceID: ws.ID, MemberName: c.Account.Name, Tool: "web"}, true
}

// adminScope 解析管理接口的 /w/{ws} 前缀：服务器管理员或该工作空间在册成员才能看到它（其余一律 404），
// 是否为管理员由业务层判定（成员非管理员时 403）。
func adminScope(w http.ResponseWriter, r *http.Request, svc *core.Service) (store.Workspace, *store.Account, bool) {
	c := caller(r)
	ws, err := svc.WorkspaceByName(r.Context(), r.PathValue("ws"))
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusNotFound, errNoWorkspace)
		return ws, nil, false
	}
	if err != nil {
		writeError(w, err)
		return ws, nil, false
	}
	if !c.Account.ServerAdmin {
		ok, err := store.IsActiveMemberOf(r.Context(), svc.Pool, c.Account.ID, ws.ID)
		if err != nil {
			writeError(w, err)
			return ws, nil, false
		}
		if !ok {
			writeJSON(w, http.StatusNotFound, errNoWorkspace)
			return ws, nil, false
		}
	}
	a := c.Account
	return ws, &a, true
}

// workspaceRoutes 注册工作空间内的看板与问题单接口（会话或令牌，要求在册）。
func workspaceRoutes(mux *http.ServeMux, svc *core.Service) {
	handle(mux, "GET /api/v1/w/{ws}/board", aSession|aToken, func(w http.ResponseWriter, r *http.Request) {
		who, ok := memberScope(w, r, svc)
		if !ok {
			return
		}
		members, items, err := svc.Board(r.Context(), who)
		if err != nil {
			writeError(w, err)
			return
		}
		out := boardResult{Me: who.MemberName, Members: make([]MemberView, 0, len(members)), Issues: make([]BoardIssue, 0, len(items))}
		for _, m := range members {
			out.Members = append(out.Members, MemberView{Name: m.Name, DisplayName: m.DisplayName, Disabled: m.AccountDisabled})
		}
		for _, it := range items {
			out.Issues = append(out.Issues, BoardIssue{IssueView: view.Issue(who, it.Issue), Holder: it.HolderName, Needs: it.Needs})
		}
		writeJSON(w, http.StatusOK, out)
	})
	handle(mux, "GET /api/v1/w/{ws}/issues", aSession|aToken, func(w http.ResponseWriter, r *http.Request) {
		who, ok := memberScope(w, r, svc)
		if !ok {
			return
		}
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
	handle(mux, "GET /api/v1/w/{ws}/issues/{id}", aSession|aToken, func(w http.ResponseWriter, r *http.Request) {
		who, ok := memberScope(w, r, svc)
		if !ok {
			return
		}
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "问题单编号不合法"})
			return
		}
		// 问题单编号全局自增：业务层按 who.WorkspaceID 校验可见性，别的工作空间的单子一律不存在
		is, evs, err := svc.Get(r.Context(), who, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detailResult{Issue: view.Issue(who, is), Events: view.Events(evs)})
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, err error) {
	var ie *core.InputError
	var se *core.StateError
	var he *core.HandoverError
	var sec *core.SecretError
	switch {
	case errors.As(err, &sec):
		// 只含字段、行号与规则，不含密钥
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.As(err, &he):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "handover": handoverJSON(he.Handover)})
	case errors.Is(err, core.ErrNotAdmin), errors.Is(err, core.ErrNotServerAdmin), errors.Is(err, core.ErrNotManager),
		errors.Is(err, store.ErrAccountDisabled):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.As(err, &se), errors.Is(err, store.ErrAlreadyMember), errors.Is(err, core.ErrJoinPending):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, core.ErrNotMember):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, store.ErrUnauthorized):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
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
