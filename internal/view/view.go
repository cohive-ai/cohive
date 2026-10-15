// Package view 定义问题单与动态的对外结构，MCP 与网页接口共用。
package view

import (
	"time"

	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

type Repro struct {
	Script   string `json:"script,omitempty"`
	Env      string `json:"env,omitempty"`
	ExitCode *int   `json:"expected_exit_code,omitempty" jsonschema:"修复后复现脚本应返回的退出码"`
}

type IssueView struct {
	ID               int64     `json:"id"`
	Version          int       `json:"version" jsonschema:"当前版本号，执行任何改变状态的操作时作为 expected_version 传入"`
	Status           string    `json:"status"`
	StatusLabel      string    `json:"status_label"`
	Category         string    `json:"category"`
	Title            string    `json:"title"`
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
	YourActions      []string  `json:"your_actions" jsonschema:"你当前可以对这张问题单执行的动作"`
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
	Payload map[string]any `json:"payload,omitempty" jsonschema:"附加信息，例如转交的接收人"`
}

// Issue 把问题单转换为对外结构，your_actions 按查看者计算。
func Issue(who store.Identity, is store.Issue) IssueView {
	roles := issue.Roles(is.SourceMemberID == who.MemberID, is.OwnerID != nil && *is.OwnerID == who.MemberID)
	actions := []string{}
	for _, a := range issue.Allowed(is.Status, roles) {
		actions = append(actions, string(a))
	}
	return IssueView{
		ID: is.ID, Version: is.Version, Status: string(is.Status), StatusLabel: is.Status.Label(),
		Category: string(is.Status.Category()), Title: is.Title, Summary: is.Summary,
		Expected: is.Expected, Actual: is.Actual,
		Repro:    Repro{Script: is.ReproScript, Env: is.ReproEnv, ExitCode: is.ReproExitCode},
		Evidence: is.Evidence, Repo: is.Repo, Branch: is.Branch, Commit: is.CommitSHA,
		TargetService: is.TargetService, Severity: is.Severity, Source: is.SourceName,
		SourceTool: is.SourceTool, Owner: is.OwnerName, ReopenCount: is.ReopenCount,
		Escalated: is.EscalatedAt != nil, EscalationReason: is.EscalationReason,
		YourActions: actions, CreatedAt: is.CreatedAt, UpdatedAt: is.UpdatedAt,
	}
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
