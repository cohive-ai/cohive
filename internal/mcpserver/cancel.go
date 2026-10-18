package mcpserver

import (
	"context"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/view"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// 作废与删除草稿（#12）。编程工具只能按发起人身份作废自己还没被接单的单；管理员作废在网页与服务器命令行里做。

type CancelArgs struct {
	IssueID         int64  `json:"issue_id"`
	ExpectedVersion int    `json:"expected_version"`
	Reason          string `json:"reason"`
	IdempotencyKey  string `json:"idempotency_key,omitempty"`
}

type DeleteDraftArgs struct {
	IssueID int64 `json:"issue_id"`
}

type DeleteDraftResult struct {
	Deleted int64 `json:"deleted"`
}

var (
	cancelArgDescs = map[string]string{
		"expected_version": "mcp.arg.expected_version",
		"reason":           "mcp.arg.cancel_reason",
	}
	deleteOutDescs = map[string]string{
		"deleted": "mcp.out.deleted",
	}
)

type actFunc func(ctx context.Context, req *mcp.CallToolRequest, in core.ActInput) (*mcp.CallToolResult, view.IssueView, error)

func registerCancel(s *mcp.Server, l i18n.Lang, svc *core.Service, act actFunc, fail func(error) error) {
	register(s, l, spec{name: "cohive_cancel_issue", ann: writes, desc: "mcp.tool.cancel_issue", writes: true}, cancelArgDescs, issueOutDescs,
		func(ctx context.Context, req *mcp.CallToolRequest, in CancelArgs) (*mcp.CallToolResult, view.IssueView, error) {
			return act(ctx, req, core.ActInput{IssueID: in.IssueID, Action: issue.ActCancel, ExpectedVersion: in.ExpectedVersion,
				Note: in.Reason, IdempotencyKey: in.IdempotencyKey})
		})

	register(s, l, spec{name: "cohive_delete_draft", ann: deletes, desc: "mcp.tool.delete_draft"}, nil, deleteOutDescs,
		func(ctx context.Context, req *mcp.CallToolRequest, in DeleteDraftArgs) (*mcp.CallToolResult, DeleteDraftResult, error) {
			who, err := identity(req)
			if err != nil {
				return nil, DeleteDraftResult{}, fail(err)
			}
			if err := svc.DeleteDraft(ctx, who, in.IssueID); err != nil {
				return nil, DeleteDraftResult{}, fail(err)
			}
			return nil, DeleteDraftResult{Deleted: in.IssueID}, nil
		})
}

var deletes = &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: f(true), OpenWorldHint: f(false)}
