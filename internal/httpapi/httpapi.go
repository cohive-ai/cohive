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

// Handler 返回 /api/v1 下的接口。除登录（登录码换取、设备授权）外都需要令牌。
// botName 返回 Telegram 机器人用户名；为 nil 或返回空时设备授权登录不可用。
func Handler(svc *core.Service, botName func() string) http.Handler {
	outer := http.NewServeMux()
	limiter := &failureLimiter{max: 30, window: time.Minute}
	deviceRoutes(outer, svc, botName)
	outer.HandleFunc("POST /api/v1/login", func(w http.ResponseWriter, r *http.Request) {
		if !limiter.begin() {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "尝试次数过多，请一分钟后再试"})
			return
		}
		failed := false
		defer func() { limiter.end(failed) }()
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
			failed = true
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
		ws, err := store.GetWorkspace(r.Context(), svc.Pool, who.WorkspaceID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"member": who.MemberName, "tool": who.Tool, "workspace": ws.Name})
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

// failureLimiter 限制一个时间窗口内的失败换取次数，防止暴力猜登录码。
// 先占名额再查库：同时在查库的请求也算进名额，并发请求无法绕过上限。
// 登录码约 40 位熵、10 分钟有效，全局每分钟 30 次失败足以让猜中的概率可以忽略。
type failureLimiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	start    time.Time
	failures int
	inflight int
}

func (l *failureLimiter) roll(now time.Time) {
	if now.Sub(l.start) >= l.window {
		l.start, l.failures = now, 0
	}
}

// begin 占用一个名额；名额用完时返回 false。
func (l *failureLimiter) begin() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.roll(time.Now())
	if l.failures+l.inflight >= l.max {
		return false
	}
	l.inflight++
	return true
}

// end 归还名额；失败的换取计入失败次数。
func (l *failureLimiter) end(failed bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.inflight--
	l.roll(time.Now())
	if failed {
		l.failures++
	}
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

// deviceRoutes 注册设备授权登录的两个接口：终端发起、终端轮询。
func deviceRoutes(mux *http.ServeMux, svc *core.Service, botName func() string) {
	// 发起不需要身份：按来源地址限频，另有全局上限，防止被刷出大量请求
	starts := &failureLimiter{max: 120, window: time.Minute}
	perIP := &keyedLimiter{max: 10, window: time.Minute}
	mux.HandleFunc("POST /api/v1/device/start", func(w http.ResponseWriter, r *http.Request) {
		name := ""
		if botName != nil {
			name = botName()
		}
		if name == "" {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "协作服务没有启用 Telegram 机器人，无法在机器人里确认登录。请管理员签发令牌，再用 cohive login --token-stdin 保存",
			})
			return
		}
		if !perIP.allow(clientIP(r)) || !starts.begin() {
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "登录请求过多，请一分钟后再试"})
			return
		}
		defer starts.end(true)
		var in struct {
			Tools      []string `json:"tools"`
			DeviceName string   `json:"device_name"`
			Workspace  string   `json:"workspace"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式不对"})
			return
		}
		st, err := svc.StartDeviceLogin(r.Context(), in.Tools, in.DeviceName, in.Workspace)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"request_id":   st.RequestID,
			"poll_secret":  st.PollSecret,
			"link":         "https://t.me/" + name + "?start=" + st.Nonce,
			"match_number": st.MatchNumber,
			"expires_in":   int(st.ExpiresIn.Seconds()),
		})
	})
	mux.HandleFunc("POST /api/v1/device/poll", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			RequestID  int64  `json:"request_id"`
			PollSecret string `json:"poll_secret"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式不对"})
			return
		}
		p, err := svc.PollDeviceLogin(r.Context(), in.RequestID, in.PollSecret)
		if errors.Is(err, core.ErrDeviceGone) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": p.Status, "member": p.Member, "workspace": p.Workspace, "tokens": p.Tokens,
		})
	})
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
