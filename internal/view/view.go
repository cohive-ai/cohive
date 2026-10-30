// Package view 定义问题单与动态的对外结构，MCP 与网页接口共用。
package view

import (
	"fmt"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/google/jsonschema-go/jsonschema"
)

type Repro struct {
	Script   string `json:"script,omitempty"`
	Env      string `json:"env,omitempty"`
	ExitCode *int   `json:"expected_exit_code,omitempty"`
}

type IssueView struct {
	ID               int64     `json:"id"`
	Version          int       `json:"version"`
	Status           string    `json:"status"`
	StatusLabel      string    `json:"status_label"`
	Category         string    `json:"category"`
	Title            string    `json:"title"`
	Brief            string    `json:"brief,omitempty"`
	Summary          string    `json:"summary,omitempty"`
	Expected         string    `json:"expected,omitempty"`
	Actual           string    `json:"actual,omitempty"`
	Repro            Repro     `json:"repro"`
	Evidence         string    `json:"evidence,omitempty"`
	Repo             string    `json:"repo,omitempty"`
	Branch           string    `json:"branch,omitempty"`
	Commit           string    `json:"commit,omitempty"`
	TargetService    string    `json:"target_service,omitempty"`
	Severity         string    `json:"severity"`
	Source           string    `json:"source"`
	SourceTool       string    `json:"source_tool"`
	Owner            string    `json:"owner,omitempty"`
	ReopenCount      int       `json:"reopen_count"`
	Escalated        bool      `json:"escalated"`
	EscalationReason string    `json:"escalation_reason,omitempty"`
	YourActions      []string  `json:"your_actions"`
	Watchers         []string  `json:"watchers,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type EventView struct {
	At      time.Time      `json:"at"`
	Actor   string         `json:"actor"`
	Tool    string         `json:"tool"`
	Action  string         `json:"action"`
	From    string         `json:"from,omitempty"`
	To      string         `json:"to,omitempty"`
	Note    string         `json:"note,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

// Issue 把问题单转换为对外结构，your_actions 按查看者计算，status_label 按语言 l 渲染。
func Issue(l i18n.Lang, who store.Identity, is store.Issue) IssueView {
	roles := issue.Roles(is.SourceMemberID == who.MemberID, is.OwnerID != nil && *is.OwnerID == who.MemberID)
	actions := []string{}
	for _, a := range issue.Allowed(is.Status, roles) {
		actions = append(actions, string(a))
	}
	return IssueView{
		ID: is.ID, Version: is.Version, Status: string(is.Status), StatusLabel: issue.StatusLabelIn(l, is.Status),
		Category: string(is.Status.Category()), Title: is.Title, Brief: is.Brief, Summary: is.Summary,
		Expected: is.Expected, Actual: is.Actual,
		Repro:    Repro{Script: is.ReproScript, Env: is.ReproEnv, ExitCode: is.ReproExitCode},
		Evidence: is.Evidence, Repo: is.Repo, Branch: is.Branch, Commit: is.CommitSHA,
		TargetService: is.TargetService, Severity: is.Severity, Source: is.SourceName,
		SourceTool: is.SourceTool, Owner: is.OwnerName, ReopenCount: is.ReopenCount,
		Escalated: is.EscalatedAt != nil, EscalationReason: is.EscalationReason,
		YourActions: actions, CreatedAt: is.CreatedAt, UpdatedAt: is.UpdatedAt,
	}
}

// WithWatchers 把关注者的登录名填进问题单结构（没有关注者时保持为空，输出里省略）。
func WithWatchers(v IssueView, ms []store.Member) IssueView {
	v.Watchers = nil
	for _, m := range ms {
		v.Watchers = append(v.Watchers, m.Name)
	}
	return v
}

// Events 把事件列表转换为对外结构。
func Events(evs []store.Event) []EventView {
	out := make([]EventView, 0, len(evs))
	for _, e := range evs {
		out = append(out, EventView{At: e.CreatedAt, Actor: e.ActorName, Tool: e.ActorTool,
			Action: string(e.Action), From: string(e.FromStatus), To: string(e.ToStatus), Note: e.Note, Payload: e.Payload})
	}
	return out
}

// ---- JSON Schema 与参数说明 ----

// IssueDescs 与 EventDescs 是输出结构里各字段的说明：键是字段的 JSON 路径，值是语言包里的文案键。
// 说明不写在结构体标签里（标签无法翻译），由 Schema 按语言填入。
var (
	IssueDescs = map[string]string{
		"version":                  "mcp.out.issue.version",
		"brief":                    "mcp.out.issue.brief",
		"repro.expected_exit_code": "mcp.out.issue.repro_expected_exit_code",
		"your_actions":             "mcp.out.issue.your_actions",
		"watchers":                 "mcp.out.issue.watchers",
	}
	EventDescs = map[string]string{
		"payload": "mcp.out.event.payload",
	}
)

// Prefix 给说明表的所有路径加前缀，用于把子结构的说明挂到外层结构上，例如 Prefix("items[].", IssueDescs)。
func Prefix(prefix string, descs map[string]string) map[string]string {
	out := make(map[string]string, len(descs))
	for k, v := range descs {
		out[prefix+k] = v
	}
	return out
}

// Schema 推出 T 的 JSON Schema，再按路径填入各属性的说明（descs：JSON 路径 → 文案键，数组元素的路径写成 items[]）。
// descs 里有路径对不上属性（字段改名后忘了同步）时返回错误。
func Schema[T any](l i18n.Lang, descs map[string]string) (*jsonschema.Schema, error) {
	s, err := jsonschema.For[T](nil)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	describe(s, "", l, descs, used)
	var missing []string
	for path := range descs {
		if !used[path] {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("description paths not found in the schema: %s", strings.Join(missing, ", "))
	}
	return s, nil
}

func describe(s *jsonschema.Schema, path string, l i18n.Lang, descs map[string]string, used map[string]bool) {
	for name, p := range s.Properties {
		full := path + name
		if key, ok := descs[full]; ok {
			p.Description = l.T(key)
			used[full] = true
		}
		describe(p, full+".", l, descs, used)
		if p.Items != nil {
			describe(p.Items, full+"[].", l, descs, used)
		}
	}
}
