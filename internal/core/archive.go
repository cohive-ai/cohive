package core

import (
	"context"
	"strings"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// 工作空间归档与彻底删除（WS-1）。
// 归档后工作空间只读：能看，不能改；不再催办，不再发群通知；取消归档即恢复。可撤销，所以允许带着未结束的单子归档，
// 但要带上确认数，免得确认时与实际数量对不上（确认之后又有人发了单）。
// 彻底删除不可撤销，只有服务器管理员能做：删掉问题单与全部动态，管理操作记录（admin_events）保留。

// 归档相关的稳定错误码，是接口约定。
const (
	CodeWorkspaceArchived     = "workspace_archived"      // 工作空间已归档，写操作被拒（409）
	CodeOpenIssuesUnconfirmed = "open_issues_unconfirmed" // 归档时带的未结束数与实际不符（409，附 open_issues）
	CodePurgeConfirmMismatch  = "purge_confirm_mismatch"  // 彻底删除时确认的名字与路径不一致（400）
)

// ArchivedError 表示工作空间已归档，写操作被拒绝，接口返回 409。
type ArchivedError struct{ Name string }

func (e *ArchivedError) Error() string { return e.Localize(i18n.Default()) }

// Localize 按语言渲染。
func (e *ArchivedError) Localize(l i18n.Lang) string {
	return l.T("core.err.workspace_archived", i18n.D{"Name": e.Name})
}

// ErrorCode 返回稳定的错误码；MCP 的报错也带上它，编程工具据此知道这不是临时故障。
func (e *ArchivedError) ErrorCode() string { return CodeWorkspaceArchived }

// PurgeConfirmError 表示彻底删除时确认的名字与工作空间路径不一致，接口返回 400。
type PurgeConfirmError struct{ Name string }

func (e *PurgeConfirmError) Error() string { return e.Localize(i18n.Default()) }

// Localize 按语言渲染。
func (e *PurgeConfirmError) Localize(l i18n.Lang) string {
	return l.T("core.err.purge_confirm_mismatch", i18n.D{"Name": e.Name})
}

// ErrorCode 返回稳定的错误码。
func (e *PurgeConfirmError) ErrorCode() string { return CodePurgeConfirmMismatch }

// OpenIssuesError 表示归档时带的未结束问题单数与实际不符，Count 是当前的实际数量，接口返回 409。
type OpenIssuesError struct{ Count int }

func (e *OpenIssuesError) Error() string { return e.Localize(i18n.Default()) }

// Localize 按语言渲染。
func (e *OpenIssuesError) Localize(l i18n.Lang) string {
	return l.T("core.err.open_issues_unconfirmed", i18n.D{"Count": e.Count})
}

// ErrorCode 返回稳定的错误码。
func (e *OpenIssuesError) ErrorCode() string { return CodeOpenIssuesUnconfirmed }

// writable 是所有写操作的只读守卫：工作空间已归档就拒绝。它必须是事务里最早的加锁语句——
// 以共享锁（FOR SHARE）读工作空间行，与归档、彻底删除的排他锁互斥，写操作之间互不阻塞；
// 先于锁问题单行、成员行，否则与彻底删除（先锁工作空间、再删问题单与成员）加锁顺序相反，会互相等待成死锁。
// 吊销类操作（吊销令牌、设备、邀请）不经过它：收权永远要能做。
func writable(ctx context.Context, q store.DBTX, wsID int64) error {
	ws, err := store.WorkspaceForShare(ctx, q, wsID)
	if err != nil {
		return err
	}
	if ws.Archived() {
		return &ArchivedError{Name: ws.Label()}
	}
	return nil
}

// lockWritable 是 writable 的排他版：后面还要 LockWorkspace 的写操作（改设置、绑定与解除群）用它。
// 两个都持有共享锁的事务再各自升级成排他锁会互相等待，所以直接一步用排他锁；同样是事务里最早的加锁语句。
func lockWritable(ctx context.Context, q store.DBTX, wsID int64) (store.Workspace, error) {
	ws, err := store.LockWorkspace(ctx, q, wsID)
	if err != nil {
		return ws, err
	}
	if ws.Archived() {
		return ws, &ArchivedError{Name: ws.Label()}
	}
	return ws, nil
}

// workspaceArchived 返回工作空间是否已归档（不加锁的读）。
func (s *Service) workspaceArchived(ctx context.Context, wsID int64) (bool, error) {
	ws, err := store.GetWorkspace(ctx, s.Pool, wsID)
	return ws.Archived(), err
}

// ArchiveWorkspace 由工作空间管理员（或服务器管理员）归档工作空间。confirmOpen 必须等于当前未结束的问题单数
// （非草稿，且不是已关闭、已退回、已作废），否则返回 OpenIssuesError 附实际数量。
// 同一事务里把该工作空间待发的群通知标为不再发送。
func (s *Service) ArchiveWorkspace(ctx context.Context, act Actor, wsID int64, confirmOpen int) (store.Workspace, error) {
	if act.Account == nil { // 命令行不归档（命令行进程不带机器人，无法在群里告知）；归档必须有操作人
		return store.Workspace{}, inputErr("core.err.archive_needs_actor")
	}
	var out store.Workspace
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		ws, err := store.LockWorkspace(ctx, tx, wsID)
		if err != nil {
			return err
		}
		if ws.Archived() {
			return stateErr("core.err.workspace_already_archived", i18n.D{"Name": ws.Label()})
		}
		open, err := store.CountUnfinishedIssues(ctx, tx, wsID)
		if err != nil {
			return err
		}
		if open != confirmOpen {
			return &OpenIssuesError{Count: open}
		}
		if err := store.SetWorkspaceArchived(ctx, tx, wsID, &act.Account.ID); err != nil {
			return err
		}
		// 归档前已排队、还没发出的群通知不再发：归档后群里不再有这个工作空间的通知
		if _, err := store.SkipPendingNotifications(ctx, tx, wsID, i18n.T("core.archive.skip_reason")); err != nil {
			return err
		}
		if err := record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(wsID), WorkspaceName: ws.Name, Action: ActWorkspaceArchive,
			TargetKind: "workspace", TargetID: idp(wsID), TargetName: ws.Name,
			Detail: map[string]any{"open_issues": open}}); err != nil {
			return err
		}
		out, err = store.GetWorkspace(ctx, tx, wsID)
		return err
	})
	return out, err
}

// UnarchiveWorkspace 取消归档，工作空间恢复读写、催办与群通知。已被跳过的通知不补发。
func (s *Service) UnarchiveWorkspace(ctx context.Context, act Actor, wsID int64) (store.Workspace, error) {
	var out store.Workspace
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		ws, err := store.LockWorkspace(ctx, tx, wsID)
		if err != nil {
			return err
		}
		if !ws.Archived() {
			return stateErr("core.err.workspace_not_archived", i18n.D{"Name": ws.Label()})
		}
		if err := store.SetWorkspaceArchived(ctx, tx, wsID, nil); err != nil {
			return err
		}
		// 归档期间问题单没有变化，计时从取消归档这一刻重新开始：免得一恢复，群里一下子冒出一批催办
		if err := store.RestartRemindClock(ctx, tx, wsID); err != nil {
			return err
		}
		if err := record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(wsID), WorkspaceName: ws.Name, Action: ActWorkspaceUnarchive,
			TargetKind: "workspace", TargetID: idp(wsID), TargetName: ws.Name}); err != nil {
			return err
		}
		out, err = store.GetWorkspace(ctx, tx, wsID)
		return err
	})
	return out, err
}

// PurgeResult 是彻底删除的结果：删掉的问题单数（不含草稿）与草稿数。
type PurgeResult struct {
	Issues int
	Drafts int
}

// PurgeWorkspace 由服务器管理员彻底删除工作空间及其问题单（含草稿）、动态、成员资格、令牌、邀请、群绑定等一切数据，不可恢复。
// confirm 必须与工作空间路径一致；还有未结束的问题单（非草稿，且不是已关闭、已退回、已作废）时拒绝。
// 管理操作记录（admin_events）只追加，不删除；本次删除另记一条，附问题单数与草稿数。
func (s *Service) PurgeWorkspace(ctx context.Context, act Actor, name, confirm string) (PurgeResult, error) {
	var res PurgeResult
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := requireServerAdmin(ctx, tx, act.Account); err != nil {
			return err
		}
		if strings.TrimSpace(confirm) != name {
			return &PurgeConfirmError{Name: name}
		}
		ws, err := store.WorkspaceByName(ctx, tx, name)
		if err != nil {
			return err
		}
		// 先锁工作空间：所有写操作都先以共享锁读它（writable），这里排他锁住之后，进行中的写操作已提交或回滚，新的进不来
		if _, err := store.LockWorkspace(ctx, tx, ws.ID); err != nil {
			return err
		}
		open, err := store.CountUnfinishedIssues(ctx, tx, ws.ID)
		if err != nil {
			return err
		}
		if open > 0 {
			return stateErr("core.err.workspace_has_unfinished", i18n.D{"Name": ws.Label(), "Count": open})
		}
		total, err := store.CountIssues(ctx, tx, ws.ID)
		if err != nil {
			return err
		}
		drafts, err := store.CountDraftIssues(ctx, tx, ws.ID)
		if err != nil {
			return err
		}
		res = PurgeResult{Issues: total - drafts, Drafts: drafts}
		if err := store.DeleteWorkspaceIssues(ctx, tx, ws.ID); err != nil {
			return err
		}
		if err := store.DeleteEmptyWorkspace(ctx, tx, ws.ID); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(ws.ID), WorkspaceName: ws.Name, Action: ActWorkspacePurge,
			TargetKind: "workspace", TargetID: idp(ws.ID), TargetName: ws.Name,
			Detail: map[string]any{"issues": res.Issues, "drafts": res.Drafts}})
	})
	return res, err
}
