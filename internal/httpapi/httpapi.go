// Package httpapi 提供看板使用的只读 JSON 接口。
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
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
	// CanCancel、CanDelete 供网页决定是否显示“作废”“删除草稿”按钮；真正操作时业务层重新判定。
	CanCancel bool `json:"can_cancel"`
	CanDelete bool `json:"can_delete"`
}

// Options 是接口层的可选依赖。
type Options struct {
	// BotName 返回 Telegram 机器人用户名；为 nil 或返回空时，通讯工具连接不可用。
	BotName func() string
	// Channels 列出服务器启用的通讯工具及其状态；为 nil 时按 BotName 判断是否启用了 Telegram。
	Channels func() []ChannelInfo
	// Announcer 在有新的待审申请、新成员加入时通知（例如发到工作空间群里）；可为空。
	Announcer Announcer
	// TrustedProxies 是除本机外可信任转发头的来源地址段，例如 Docker 网络里的隧道或反向代理。
	TrustedProxies []*net.IPNet
}

// ChannelInfo 是服务器启用的一种通讯工具。
type ChannelInfo struct {
	Provider string `json:"provider"`           // telegram、dingtalk、slack
	BotName  string `json:"bot_name,omitempty"` // Telegram 机器人用户名
	AppID    string `json:"app_id,omitempty"`   // 钉钉应用 Client ID 的尾号（核对用的是哪个应用）
	Online   bool   `json:"online"`             // 收件循环是否在线
	Detail   string `json:"detail,omitempty"`   // 不在线的原因、配置提示等，给服务器管理员看
}

// channels 返回服务器启用的通讯工具。
func channels(opts Options) []ChannelInfo {
	if opts.Channels != nil {
		return opts.Channels()
	}
	if name := botName(opts); name != "" {
		return []ChannelInfo{{Provider: core.ProviderTelegram, BotName: name, Online: true}}
	}
	return []ChannelInfo{}
}

// providerEnabled 判断服务器是否启用了某种通讯工具。
func providerEnabled(opts Options, provider string) bool {
	for _, c := range channels(opts) {
		if c.Provider == provider {
			return true
		}
	}
	return false
}

// enabledProviders 是给成员看的启用列表（不含状态细节）。
func enabledProviders(opts Options) []map[string]string {
	out := []map[string]string{}
	for _, c := range channels(opts) {
		out = append(out, map[string]string{"provider": c.Provider})
	}
	return out
}

// Handler 返回 /api/v1 与 /auth/ 下的接口。网页用会话 Cookie，命令行与编程工具用令牌或设备密钥；
// 登录、注册、设备授权的发起与轮询不需要身份。
func Handler(svc *core.Service, opts Options) http.Handler {
	outer := http.NewServeMux()
	publicRoutes(outer, svc, opts)

	mux := http.NewServeMux()
	accountRoutes(mux, svc, opts)
	workspaceRoutes(mux, svc)
	alertRoutes(mux, svc)
	adminRoutes(mux, svc, opts)
	serverRoutes(mux, svc, opts)
	eventRoutes(mux, svc)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, apiErr(w, "route_not_found", "api.route_not_found"))
	})
	outer.Handle("/api/", authMiddleware(svc, mux))
	// 每个请求按 Accept-Language 定语言，并在响应头 Content-Language 回告；报错与提示文字都按它渲染
	return i18n.Middleware(outer)
}

// noWorkspace 写工作空间不存在或不是它的成员时的统一 404，不区分是哪种情况。
func noWorkspace(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, apiErr(w, "no_workspace", "api.no_workspace"))
}

// memberScope 解析 /w/{ws} 前缀：会话要求账号在该工作空间在册；令牌要求令牌就属于该工作空间。
// 返回本次请求在该工作空间里的身份；不满足时已写好 404。
func memberScope(w http.ResponseWriter, r *http.Request, svc *core.Service) (store.Identity, bool) {
	c := caller(r)
	name := r.PathValue("ws")
	ws, err := svc.WorkspaceByName(r.Context(), name)
	if errors.Is(err, store.ErrNotFound) {
		noWorkspace(w)
		return store.Identity{}, false
	}
	if err != nil {
		writeError(w, err)
		return store.Identity{}, false
	}
	if c.Token != nil {
		if c.Token.WorkspaceID != ws.ID {
			noWorkspace(w)
			return store.Identity{}, false
		}
		return *c.Token, true
	}
	m, err := store.MemberOf(r.Context(), svc.Pool, c.Account.ID, ws.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && m.Status != store.MemberActive) {
		noWorkspace(w)
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
		noWorkspace(w)
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
			noWorkspace(w)
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
			out.Issues = append(out.Issues, BoardIssue{IssueView: view.Issue(i18n.FromContext(r.Context()), who, it.Issue), Holder: it.HolderName, Needs: it.Needs})
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
			// 默认（全部状态）不含已作废，筛选“已作废”时才列出（#12）
			if (status == "" && is.Status != issue.StatusCancelled) || string(is.Status) == status {
				out.Items = append(out.Items, view.Issue(i18n.FromContext(r.Context()), who, is))
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
			writeJSON(w, http.StatusBadRequest, apiErr(w, "invalid_id", "api.issue_id_invalid"))
			return
		}
		// 问题单编号全局自增：业务层按 who.WorkspaceID 校验可见性，别的工作空间的单子一律不存在
		is, evs, err := svc.Get(r.Context(), who, id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, detailResult{Issue: view.Issue(i18n.FromContext(r.Context()), who, is), Events: view.Events(evs),
			CanCancel: svc.CanCancel(r.Context(), webAdmin(r), who, is),
			CanDelete: is.Status == issue.StatusDraft && is.SourceMemberID == who.MemberID})
	})
	// 作废（#12）：发起人作废自己还没被接单的单；管理员作废任何进行中的单（只认账号密码登录的会话）。
	handle(mux, "POST /api/v1/w/{ws}/issues/{id}/cancel", aSession, func(w http.ResponseWriter, r *http.Request) {
		who, ok := memberScope(w, r, svc)
		if !ok {
			return
		}
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		var body struct {
			ExpectedVersion int    `json:"expected_version"`
			Reason          string `json:"reason"`
		}
		if !decode(w, r, &body) {
			return
		}
		is, err := svc.CancelIssue(r.Context(), core.WebActor(caller(r).Account), who, core.CancelInput{
			IssueID: id, ExpectedVersion: body.ExpectedVersion, Reason: body.Reason, PartyOnly: webAdmin(r) == nil})
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, view.Issue(i18n.FromContext(r.Context()), who, is))
	})
	// 删除自己的草稿：草稿只有本人看得见，删掉不留痕。
	handle(mux, "DELETE /api/v1/w/{ws}/issues/{id}", aSession, func(w http.ResponseWriter, r *http.Request) {
		who, ok := memberScope(w, r, svc)
		if !ok {
			return
		}
		id, ok := pathID(w, r, "id")
		if !ok {
			return
		}
		if err := svc.DeleteDraft(r.Context(), who, id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

// webAdmin 返回可以做管理操作的网页账号：只有账号密码登录的会话才算，免密会话与令牌返回空。
func webAdmin(r *http.Request) *store.Account {
	c := caller(r)
	if c.Token != nil || c.SessionVia != store.SessionViaPassword {
		return nil
	}
	a := c.Account
	return &a
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// 接口错误响应的形状是 {"error": "<按请求语言渲染的文字>", "code": "<稳定的错误码>"}，部分响应带附加字段。
// error 只给人看，会随语言变化，调用方（网页、命令行）不要按文字判断；要判断请用 code。code 是接口约定，取值：
//
//	invalid_input、invalid_id、invalid_request、invalid_param  请求内容不合法（400）
//	member_nickname、member_ambiguous  指派用的名字不是确定的登录名（只是昵称，或登录名与别人的昵称相同），附候选（400）
//	secret_detected      内容里含疑似密钥（400，只含字段、行号与规则，不含密钥）
//	no_device            这个操作需要设备配对过的凭据（400）
//	unauthorized、token_format、token_invalid、device_key_invalid、device_key_not_allowed、device_key_only、
//	login_required、session_expired、bad_login               没有或无效的凭据（401）
//	forbidden、account_disabled、signup_pending、cross_site、must_change_password、need_password_login、web_only、not_for_web  无权限（403）
//	not_found、route_not_found、no_workspace、not_member、link_invalid  找不到（404）
//	handover_required    需要先交接（409，附 handover）
//	version_conflict     问题单版本已变化或事务被数据库并发中止，需重读后重试（409）
//	conflict、already_member、join_pending、decided          当前状态不允许该操作（409）
//	too_many_requests、login_locked                          限频或登录退避（429）
//	service_unavailable  协作服务没有启用该功能（503）
//	internal             内部错误（500）
func apiErr(w http.ResponseWriter, code, key string, data ...i18n.D) map[string]any {
	return map[string]any{"error": i18n.FromResponse(w).T(key, data...), "code": code}
}

// errBody 把业务层返回的错误按请求语言渲染成响应体。
func errBody(w http.ResponseWriter, code string, err error) map[string]any {
	return map[string]any{"error": i18n.Message(i18n.FromResponse(w), err), "code": code}
}

func writeError(w http.ResponseWriter, err error) {
	var ie *core.InputError
	var se *core.StateError
	var he *core.HandoverError
	var sec *core.SecretError
	var te *issue.TransitionError
	var mce *core.MemberChoiceError
	switch {
	case errors.As(err, &sec):
		// 只含字段、行号与规则，不含密钥
		writeJSON(w, http.StatusBadRequest, errBody(w, "secret_detected", err))
	case errors.As(err, &he):
		body := errBody(w, "handover_required", err)
		body["handover"] = handoverJSON(i18n.FromResponse(w), he.Handover)
		writeJSON(w, http.StatusConflict, body)
	case errors.Is(err, store.ErrAccountDisabled):
		writeJSON(w, http.StatusForbidden, errBody(w, "account_disabled", err))
	case errors.Is(err, core.ErrNotAdmin), errors.Is(err, core.ErrNotServerAdmin), errors.Is(err, core.ErrNotManager):
		writeJSON(w, http.StatusForbidden, errBody(w, "forbidden", err))
	case errors.Is(err, store.ErrVersionConflict): // 含版本冲突与事务被数据库并发中止（core.RetryError）
		writeJSON(w, http.StatusConflict, errBody(w, "version_conflict", err))
	case errors.As(err, &se), errors.As(err, &te): // te：当前状态不允许该动作
		writeJSON(w, http.StatusConflict, errBody(w, "conflict", err))
	case errors.Is(err, store.ErrAlreadyMember):
		writeJSON(w, http.StatusConflict, errBody(w, "already_member", err))
	case errors.Is(err, core.ErrJoinPending):
		writeJSON(w, http.StatusConflict, errBody(w, "join_pending", err))
	case errors.Is(err, core.ErrNotMember):
		writeJSON(w, http.StatusNotFound, errBody(w, "not_member", err))
	case errors.Is(err, store.ErrUnauthorized):
		writeJSON(w, http.StatusUnauthorized, errBody(w, "unauthorized", err))
	case errors.Is(err, core.ErrDecided):
		writeJSON(w, http.StatusConflict, errBody(w, "decided", err))
	case errors.Is(err, core.ErrInviteInvalid), errors.Is(err, core.ErrAuthGone), errors.Is(err, core.ErrLinkGone):
		writeJSON(w, http.StatusNotFound, errBody(w, "link_invalid", err))
	case errors.Is(err, core.ErrNoDevice):
		writeJSON(w, http.StatusBadRequest, errBody(w, "no_device", err))
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody(w, "not_found", err))
	case errors.As(err, &mce):
		writeJSON(w, http.StatusBadRequest, errBody(w, mce.ErrorCode(), err))
	case errors.As(err, &ie):
		writeJSON(w, http.StatusBadRequest, errBody(w, "invalid_input", err))
	default:
		log.Printf("api internal error: %v", err)
		writeJSON(w, http.StatusInternalServerError, apiErr(w, "internal", "api.internal"))
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

// clientIP 返回请求的来源地址。来自本机或 TrustedProxies 里的地址（例如 Cloudflare 隧道的 cloudflared、
// 反向代理）时，取 CF-Connecting-IP，没有则取 X-Forwarded-For 的最后一项；其余情况不信任转发头，
// 避免被伪造来绕过限频。
func (o Options) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil || !(ip.IsLoopback() || o.trusted(ip)) {
		return host
	}
	if cf := net.ParseIP(strings.TrimSpace(r.Header.Get("CF-Connecting-IP"))); cf != nil {
		return cf.String()
	}
	if xff := r.Header.Values("X-Forwarded-For"); len(xff) > 0 {
		parts := strings.Split(xff[len(xff)-1], ",")
		if fwd := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); fwd != nil {
			return fwd.String()
		}
	}
	return host
}

func (o Options) trusted(ip net.IP) bool {
	for _, n := range o.TrustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ParseTrustedProxies 解析逗号分隔的地址段（如 172.16.0.0/12）或单个地址。
func ParseTrustedProxies(s string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, item := range strings.Split(s, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			ip := net.ParseIP(item)
			if ip == nil {
				return nil, fmt.Errorf("%q is not an address or address range, for example 172.16.0.0/12", item)
			}
			bits := 128
			if ip.To4() != nil {
				ip, bits = ip.To4(), 32
			}
			out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, n, err := net.ParseCIDR(item)
		if err != nil {
			return nil, fmt.Errorf("%q is not an address or address range, for example 172.16.0.0/12", item)
		}
		out = append(out, n)
	}
	return out, nil
}
