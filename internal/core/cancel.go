package core

import (
	"context"
	"errors"
	"strings"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// 作废与删除草稿（#12）。作废后不能恢复，要重来就新发一张；已作废的单只能留言。

// CancelInput 是网页与服务器命令行作废问题单的参数。编程工具走 Act（ActCancel），只能按发起人身份作废。
type CancelInput struct {
	IssueID         int64
	ExpectedVersion int
	Reason          string
	// PartyOnly 为真时只按发起人身份判定、不走管理员作废：免密登录的网页会话不能做管理操作。
	PartyOnly bool
}

// CancelIssue 作废问题单：发起人可以作废自己发出、还没被接单的单；工作空间管理员可以作废任何进行中的单
// （已关闭、已退回已经有了结论，不能作废）。who 是写动态用的成员身份，act 是管理操作的操作人与入口：
// 网页上是登录的账号，服务器命令行上 act.Account 为空、视为最高权限，who 由调用方按 --as 给出。
// 管理员作废另记一条管理操作记录。
func (s *Service) CancelIssue(ctx context.Context, act Actor, who store.Identity, in CancelInput) (store.Issue, error) {
	if in.ExpectedVersion <= 0 {
		return store.Issue{}, inputErr("core.err.expected_version_required")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return store.Issue{}, inputErr("core.err.cancel_reason_required")
	}
	if err := checkSecrets(actFields(ActInput{Note: in.Reason})...); err != nil {
		return store.Issue{}, err
	}
	var out store.Issue
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := writable(ctx, tx, who.WorkspaceID); err != nil {
			return err
		}
		is, err := store.GetIssueForUpdate(ctx, tx, in.IssueID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !visible(who, is)) {
			return notFoundIssue(in.IssueID)
		}
		if err != nil {
			return err
		}
		if is.Version != in.ExpectedVersion {
			return &ConflictError{IssueID: is.ID, Expected: in.ExpectedVersion, Current: is.Version}
		}
		from := is.Status
		roles := issue.Roles(is.SourceMemberID == who.MemberID, is.OwnerID != nil && *is.OwnerID == who.MemberID)
		byAdmin := false
		if _, perr := issue.Apply(from, issue.ActCancel, roles); perr != nil {
			if in.PartyOnly {
				return perr
			}
			if aerr := canAdmin(ctx, tx, act.Account, is.WorkspaceID); aerr != nil {
				if errors.Is(aerr, ErrNotAdmin) {
					return perr // 不是管理员：照状态机说明发起人什么时候能作废
				}
				return aerr
			}
			if !issue.AdminCancellable(from) {
				return stateErr("core.err.cancel_status", i18n.D{"ID": is.ID, "Status": lk(statusKeyOf(from))})
			}
			byAdmin = true
		}
		is.Status = issue.StatusCancelled
		if err := store.UpdateIssue(ctx, tx, &is); err != nil {
			return err
		}
		payload := map[string]any{}
		if byAdmin {
			payload["by_admin"] = true
		}
		if err := store.InsertEvent(ctx, tx, store.Event{IssueID: is.ID, ActorMemberID: who.MemberID, ActorTool: who.Tool,
			Action: issue.ActCancel, FromStatus: from, ToStatus: is.Status, Note: in.Reason, Payload: payload}); err != nil {
			return err
		}
		watchers, err := store.WatcherIDs(ctx, tx, is.ID)
		if err != nil {
			return err
		}
		items := attachWatchers(who, fanout(who, is, issue.ActCancel, false), EventCancelled, watchers, nil)
		if err := queueNotices(ctx, tx, who, is, items); err != nil {
			return err
		}
		if byAdmin {
			detail := map[string]any{"from": string(from)}
			if act.Account == nil {
				detail["as"] = who.MemberName
			}
			if err := record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(is.WorkspaceID), Action: ActIssueCancel,
				TargetKind: "issue", TargetID: idp(is.ID), TargetName: is.Title, Detail: detail}); err != nil {
				return err
			}
		}
		out, err = store.GetIssue(ctx, tx, is.ID)
		return err
	})
	return out, err
}

// CanCancel 说明 who 现在能不能作废这张单（网页据此显示按钮）；真正作废时在事务里重新判定。
func (s *Service) CanCancel(ctx context.Context, account *store.Account, who store.Identity, is store.Issue) bool {
	roles := issue.Roles(is.SourceMemberID == who.MemberID, is.OwnerID != nil && *is.OwnerID == who.MemberID)
	if _, err := issue.Apply(is.Status, issue.ActCancel, roles); err == nil {
		return true
	}
	return account != nil && issue.AdminCancellable(is.Status) && canAdmin(ctx, s.Pool, account, is.WorkspaceID) == nil
}

// DeleteDraft 删除自己的草稿：草稿只有发起人看得见，删掉不留痕、不通知。已发出的单不能删，只能作废。
func (s *Service) DeleteDraft(ctx context.Context, who store.Identity, id int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := writable(ctx, tx, who.WorkspaceID); err != nil {
			return err
		}
		is, err := store.GetIssueForUpdate(ctx, tx, id)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !visible(who, is)) {
			return notFoundIssue(id)
		}
		if err != nil {
			return err
		}
		if is.Status != issue.StatusDraft || is.SourceMemberID != who.MemberID {
			return stateErr("core.err.delete_not_draft", i18n.D{"ID": id})
		}
		return store.DeleteIssue(ctx, tx, id)
	})
}

// statusKeyOf 是状态名的文案键，放进错误参数里按渲染语言翻译。
func statusKeyOf(s issue.Status) string { return "issue.status." + strings.ToLower(string(s)) }
