// Package mcpserver 把业务层以远程 MCP 服务的形式提供给各编程工具。
// 本层只做鉴权和参数映射，规则都在 core 与 issue 里。
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/cohive-ai/cohive/internal/authn"
	"github.com/cohive-ai/cohive/internal/buildinfo"
	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/view"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Protected 返回带令牌鉴权的 MCP 处理器。allowedHosts 是除 localhost 外允许的 Host（经隧道转发的公网域名）。
//
// 工具说明、参数说明和报错都按语言渲染，所以每种语言各建一份 *mcp.Server。
// 第一段（现在）：每个请求用服务器缺省语言那一份，忽略编程工具发来的 Accept-Language（各家客户端不可控）。
// 第二段：改为按鉴权后的账号语言选，账号没有设语言时回退服务器缺省语言。
func Protected(svc *core.Service, allowedHosts ...string) http.Handler {
	servers := map[i18n.Lang]*mcp.Server{}
	for _, l := range i18n.Supported() {
		servers[l] = NewServerFor(svc, l)
	}
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		if s, ok := servers[i18n.Default()]; ok {
			return s
		}
		return servers[i18n.EN]
	}, &mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true})
	// 对外地址的主机名随服务器设置变化，每次请求读 svc 的内存缓存
	return hostGuard(allowedHosts, svc.ExternalHost, authn.Middleware(svc.Pool)(h))
}

// Handler 返回只挂载了 /mcp 与 /healthz 的处理器，供测试使用。
func Handler(svc *core.Service) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", Protected(svc))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	return mux
}

func identity(req *mcp.CallToolRequest) (store.Identity, error) {
	if req.Extra != nil {
		if id, ok := authn.FromTokenInfo(req.Extra.TokenInfo); ok {
			return id, nil
		}
	}
	return store.Identity{}, i18n.Errorf("mcp.err.unauthenticated")
}

// ---- 输入结构 ----

type DraftArgs struct {
	Title          string `json:"title"`
	Brief          string `json:"brief,omitempty"`
	Summary        string `json:"summary,omitempty"`
	Expected       string `json:"expected,omitempty"`
	Actual         string `json:"actual,omitempty"`
	ReproScript    string `json:"repro_script,omitempty"`
	ReproEnv       string `json:"repro_env,omitempty"`
	ReproExitCode  *int   `json:"repro_expected_exit_code,omitempty"`
	Evidence       string `json:"evidence,omitempty"`
	Repo           string `json:"repo,omitempty"`
	Branch         string `json:"branch,omitempty"`
	Commit         string `json:"commit,omitempty"`
	TargetService  string `json:"target_service,omitempty"`
	Owner          string `json:"owner,omitempty"`
	Severity       string `json:"severity,omitempty"`
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

type SubmitArgs struct {
	IssueID         int64  `json:"issue_id"`
	ExpectedVersion int    `json:"expected_version"`
	Assignee        string `json:"assignee,omitempty"`
	Note            string `json:"note,omitempty"`
	IdempotencyKey  string `json:"idempotency_key,omitempty"`
}

type AcceptArgs struct {
	IssueID         int64  `json:"issue_id"`
	ExpectedVersion int    `json:"expected_version"`
	Decision        string `json:"decision"`
	Note            string `json:"note,omitempty"`
	TransferTo      string `json:"transfer_to,omitempty"`
	IdempotencyKey  string `json:"idempotency_key,omitempty"`
}

type UpdateArgs struct {
	IssueID         int64  `json:"issue_id"`
	ExpectedVersion int    `json:"expected_version,omitempty"`
	Note            string `json:"note,omitempty"`
	StartFixing     bool   `json:"start_fixing,omitempty"`
	IdempotencyKey  string `json:"idempotency_key,omitempty"`
}

type RequestVerificationArgs struct {
	IssueID         int64  `json:"issue_id"`
	ExpectedVersion int    `json:"expected_version"`
	Note            string `json:"note,omitempty"`
	IdempotencyKey  string `json:"idempotency_key,omitempty"`
}

type CloseArgs struct {
	IssueID         int64  `json:"issue_id"`
	ExpectedVersion int    `json:"expected_version"`
	Verdict         string `json:"verdict"`
	Note            string `json:"note,omitempty"`
	IdempotencyKey  string `json:"idempotency_key,omitempty"`
}

type GetArgs struct {
	IssueID int64 `json:"issue_id"`
}

type SearchArgs struct {
	Query   string `json:"query,omitempty"`
	Service string `json:"service,omitempty"`
}

type WhoOwnsArgs struct {
	Service string `json:"service"`
}

type GetResult struct {
	Issue  view.IssueView   `json:"issue"`
	Events []view.EventView `json:"events"`
}

type InboxEntry struct {
	view.IssueView
	NeedsMe string `json:"needs_me"`
}

type InboxResult struct {
	Items []InboxEntry `json:"items"`
}

type SearchResult struct {
	Items []view.IssueView `json:"items"`
}

type OwnerResult struct {
	Service     string `json:"service"`
	Owner       string `json:"owner"`
	DisplayName string `json:"display_name,omitempty"`
}

// ---- 工具注册 ----

func f(b bool) *bool { return &b }

var (
	readOnly = &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: f(false)}
	writes   = &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: f(false), OpenWorldHint: f(false)}
)

// ---- 参数说明 ----
//
// 参数说明不写在结构体标签里（标签无法翻译）：按语言推出结构的 JSON Schema 后，再按字段路径填说明。
// 键是字段的 JSON 路径（数组元素写成 items[]），值是语言包里的文案键；路径写错或语言包缺词，
// 启动时（view.Schema）与测试会报错。参数名、动作名、取值（accept、pass 等）是接口，不翻译。

var (
	draftArgDescs = map[string]string{
		"title":                    "mcp.arg.title",
		"brief":                    "mcp.arg.brief",
		"summary":                  "mcp.arg.summary",
		"expected":                 "mcp.arg.expected",
		"actual":                   "mcp.arg.actual",
		"repro_script":             "mcp.arg.repro_script",
		"repro_env":                "mcp.arg.repro_env",
		"repro_expected_exit_code": "mcp.arg.repro_expected_exit_code",
		"evidence":                 "mcp.arg.evidence",
		"repo":                     "mcp.arg.repo",
		"commit":                   "mcp.arg.commit",
		"target_service":           "mcp.arg.target_service",
		"owner":                    "mcp.arg.owner",
		"severity":                 "mcp.arg.severity",
	}
	submitArgDescs = map[string]string{
		"expected_version": "mcp.arg.expected_version",
		"assignee":         "mcp.arg.assignee",
	}
	acceptArgDescs = map[string]string{
		"expected_version": "mcp.arg.expected_version",
		"decision":         "mcp.arg.decision",
		"transfer_to":      "mcp.arg.transfer_to",
	}
	updateArgDescs = map[string]string{
		"expected_version": "mcp.arg.update_expected_version",
		"note":             "mcp.arg.update_note",
		"start_fixing":     "mcp.arg.start_fixing",
	}
	verifyArgDescs = map[string]string{
		"expected_version": "mcp.arg.expected_version",
		"note":             "mcp.arg.verify_note",
	}
	closeArgDescs = map[string]string{
		"expected_version": "mcp.arg.expected_version",
		"verdict":          "mcp.arg.verdict",
		"note":             "mcp.arg.close_note",
	}
	searchArgDescs = map[string]string{
		"query":   "mcp.arg.query",
		"service": "mcp.arg.service",
	}

	issueOutDescs  = view.IssueDescs
	getOutDescs    = merge(view.Prefix("issue.", view.IssueDescs), view.Prefix("events[].", view.EventDescs))
	inboxOutDescs  = view.Prefix("items[].", view.IssueDescs)
	searchOutDescs = view.Prefix("items[].", view.IssueDescs)
)

func merge(ms ...map[string]string) map[string]string {
	out := map[string]string{}
	for _, m := range ms {
		for k, v := range m {
			out[k] = v
		}
	}
	return out
}

// spec 描述一个工具：名称、注解、说明的文案键，以及是否会写入正文（说明末尾附密钥扫描提示）。
type spec struct {
	name   string
	ann    *mcp.ToolAnnotations
	desc   string
	writes bool
}

// register 按语言登记一个工具：说明和参数说明取自语言包，输入输出的 Schema 由类型推出后补说明。
func register[In, Out any](s *mcp.Server, l i18n.Lang, sp spec, inDescs, outDescs map[string]string, h mcp.ToolHandlerFor[In, Out]) {
	in, err := view.Schema[In](l, inDescs)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: input schema of %s: %v", sp.name, err))
	}
	out, err := view.Schema[Out](l, outDescs)
	if err != nil {
		panic(fmt.Sprintf("mcpserver: output schema of %s: %v", sp.name, err))
	}
	desc := l.T(sp.desc)
	desc += "\n\n" + l.T("mcp.note.trust")
	if sp.writes {
		desc += "\n\n" + l.T("mcp.note.secret")
	}
	mcp.AddTool(s, &mcp.Tool{Name: sp.name, Annotations: sp.ann, Description: desc, InputSchema: in, OutputSchema: out}, h)
}

// NewServer 创建注册了全部 Cohive 工具的 MCP 服务，语言为服务器缺省语言。
func NewServer(svc *core.Service) *mcp.Server { return NewServerFor(svc, i18n.Default()) }

// NewServerFor 创建注册了全部 Cohive 工具的 MCP 服务，工具说明、参数说明和报错都用语言 l。
func NewServerFor(svc *core.Service, l i18n.Lang) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "cohive", Version: buildinfo.Version}, &mcp.ServerOptions{
		Instructions: l.T("mcp.instructions") + "\n\n" + l.T("mcp.note.trust"),
	})

	// fail 把错误按本服务的语言渲染后交给 MCP 开发包（它只取 err.Error()）
	fail := func(err error) error { return errors.New(i18n.Message(l, err)) }

	act := func(ctx context.Context, req *mcp.CallToolRequest, in core.ActInput) (*mcp.CallToolResult, view.IssueView, error) {
		who, err := identity(req)
		if err != nil {
			return nil, view.IssueView{}, fail(err)
		}
		is, err := svc.Act(ctx, who, in)
		if err != nil {
			return nil, view.IssueView{}, fail(err)
		}
		return nil, view.Issue(l, who, is), nil
	}

	register(s, l, spec{name: "cohive_draft_issue", ann: writes, desc: "mcp.tool.draft_issue", writes: true}, draftArgDescs, issueOutDescs,
		func(ctx context.Context, req *mcp.CallToolRequest, in DraftArgs) (*mcp.CallToolResult, view.IssueView, error) {
			who, err := identity(req)
			if err != nil {
				return nil, view.IssueView{}, fail(err)
			}
			is, err := svc.Draft(ctx, who, core.DraftInput{
				Title: in.Title, Brief: in.Brief, Summary: in.Summary, Expected: in.Expected, Actual: in.Actual,
				ReproScript: in.ReproScript, ReproEnv: in.ReproEnv, ReproExitCode: in.ReproExitCode,
				Evidence: in.Evidence, Repo: in.Repo, Branch: in.Branch, Commit: in.Commit,
				TargetService: in.TargetService, Owner: in.Owner, Severity: in.Severity,
				IdempotencyKey: in.IdempotencyKey,
			})
			if err != nil {
				return nil, view.IssueView{}, fail(err)
			}
			return nil, view.Issue(l, who, is), nil
		})

	register(s, l, spec{name: "cohive_submit_issue", ann: writes, desc: "mcp.tool.submit_issue", writes: true}, submitArgDescs, issueOutDescs,
		func(ctx context.Context, req *mcp.CallToolRequest, in SubmitArgs) (*mcp.CallToolResult, view.IssueView, error) {
			return act(ctx, req, core.ActInput{IssueID: in.IssueID, Action: issue.ActSubmit,
				ExpectedVersion: in.ExpectedVersion, Assignee: in.Assignee, Note: in.Note, IdempotencyKey: in.IdempotencyKey})
		})

	register(s, l, spec{name: "cohive_accept_issue", ann: writes, desc: "mcp.tool.accept_issue", writes: true}, acceptArgDescs, issueOutDescs,
		func(ctx context.Context, req *mcp.CallToolRequest, in AcceptArgs) (*mcp.CallToolResult, view.IssueView, error) {
			var a issue.Action
			switch in.Decision {
			case "accept":
				a = issue.ActAccept
			case "decline":
				a = issue.ActDecline
			case "transfer":
				a = issue.ActTransfer
			default:
				return nil, view.IssueView{}, fail(i18n.Errorf("mcp.err.decision_invalid", i18n.D{"Value": strconv.Quote(in.Decision)}))
			}
			return act(ctx, req, core.ActInput{IssueID: in.IssueID, Action: a, ExpectedVersion: in.ExpectedVersion,
				Note: in.Note, Assignee: in.TransferTo, IdempotencyKey: in.IdempotencyKey})
		})

	register(s, l, spec{name: "cohive_update_issue", ann: writes, desc: "mcp.tool.update_issue", writes: true}, updateArgDescs, issueOutDescs,
		func(ctx context.Context, req *mcp.CallToolRequest, in UpdateArgs) (*mcp.CallToolResult, view.IssueView, error) {
			a := issue.ActComment
			if in.StartFixing {
				a = issue.ActStart
			} else if in.Note == "" {
				return nil, view.IssueView{}, fail(i18n.Errorf("mcp.err.note_required"))
			}
			return act(ctx, req, core.ActInput{IssueID: in.IssueID, Action: a, ExpectedVersion: in.ExpectedVersion,
				Note: in.Note, IdempotencyKey: in.IdempotencyKey})
		})

	register(s, l, spec{name: "cohive_request_verification", ann: writes, desc: "mcp.tool.request_verification", writes: true}, verifyArgDescs, issueOutDescs,
		func(ctx context.Context, req *mcp.CallToolRequest, in RequestVerificationArgs) (*mcp.CallToolResult, view.IssueView, error) {
			return act(ctx, req, core.ActInput{IssueID: in.IssueID, Action: issue.ActRequestVerification,
				ExpectedVersion: in.ExpectedVersion, Note: in.Note, IdempotencyKey: in.IdempotencyKey})
		})

	register(s, l, spec{name: "cohive_close_issue", ann: writes, desc: "mcp.tool.close_issue", writes: true}, closeArgDescs, issueOutDescs,
		func(ctx context.Context, req *mcp.CallToolRequest, in CloseArgs) (*mcp.CallToolResult, view.IssueView, error) {
			var a issue.Action
			switch in.Verdict {
			case "pass":
				a = issue.ActClose
			case "reopen":
				a = issue.ActReopen
			default:
				return nil, view.IssueView{}, fail(i18n.Errorf("mcp.err.verdict_invalid", i18n.D{"Value": strconv.Quote(in.Verdict)}))
			}
			return act(ctx, req, core.ActInput{IssueID: in.IssueID, Action: a, ExpectedVersion: in.ExpectedVersion,
				Note: in.Note, IdempotencyKey: in.IdempotencyKey})
		})

	register(s, l, spec{name: "cohive_get_issue", ann: readOnly, desc: "mcp.tool.get_issue"}, nil, getOutDescs,
		func(ctx context.Context, req *mcp.CallToolRequest, in GetArgs) (*mcp.CallToolResult, GetResult, error) {
			who, err := identity(req)
			if err != nil {
				return nil, GetResult{}, fail(err)
			}
			is, evs, err := svc.Get(ctx, who, in.IssueID)
			if err != nil {
				return nil, GetResult{}, fail(err)
			}
			out := GetResult{Issue: view.Issue(l, who, is), Events: view.Events(evs)}
			return nil, out, nil
		})

	register(s, l, spec{name: "cohive_inbox", ann: readOnly, desc: "mcp.tool.inbox"}, nil, inboxOutDescs,
		func(ctx context.Context, req *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, InboxResult, error) {
			who, err := identity(req)
			if err != nil {
				return nil, InboxResult{}, fail(err)
			}
			items, err := svc.Inbox(ctx, who)
			if err != nil {
				return nil, InboxResult{}, fail(err)
			}
			out := InboxResult{Items: make([]InboxEntry, 0, len(items))}
			for _, it := range items {
				out.Items = append(out.Items, InboxEntry{IssueView: view.Issue(l, who, it.Issue), NeedsMe: it.NeedsMe})
			}
			return nil, out, nil
		})

	register(s, l, spec{name: "cohive_search", ann: readOnly, desc: "mcp.tool.search"}, searchArgDescs, searchOutDescs,
		func(ctx context.Context, req *mcp.CallToolRequest, in SearchArgs) (*mcp.CallToolResult, SearchResult, error) {
			who, err := identity(req)
			if err != nil {
				return nil, SearchResult{}, fail(err)
			}
			list, err := svc.Search(ctx, who, in.Query, in.Service)
			if err != nil {
				return nil, SearchResult{}, fail(err)
			}
			out := SearchResult{Items: make([]view.IssueView, 0, len(list))}
			for _, is := range list {
				out.Items = append(out.Items, view.Issue(l, who, is))
			}
			return nil, out, nil
		})

	register(s, l, spec{name: "cohive_who_owns", ann: readOnly, desc: "mcp.tool.who_owns"}, nil, nil,
		func(ctx context.Context, req *mcp.CallToolRequest, in WhoOwnsArgs) (*mcp.CallToolResult, OwnerResult, error) {
			who, err := identity(req)
			if err != nil {
				return nil, OwnerResult{}, fail(err)
			}
			m, err := svc.WhoOwns(ctx, who, in.Service)
			if err != nil {
				return nil, OwnerResult{}, fail(err)
			}
			return nil, OwnerResult{Service: in.Service, Owner: m.Name, DisplayName: m.DisplayName}, nil
		})

	return s
}
