package core

import (
	"context"
	"fmt"

	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// 超时催办（2026-10-18 lee 定）：待接单、待验证到了工作空间设置的时长，在群里再 @ 当事人一次（第 1 档）；
// 再过一个周期仍没有动作，同时 @ 工作空间管理员（第 2 档），之后不再发。按数据库的自然时间算；
// “修复中”不计时；状态一变、当事人一换就重新计时（store.UpdateIssue、store.SetIssueSource）。
const (
	EventOverdue      = "overdue"       // 第 1 档：再 @ 当事人
	EventOverdueAdmin = "overdue_admin" // 第 2 档：当事人与工作空间管理员
	AlertOverdue      = "overdue"       // 会话内提醒：待你处理的单超时了（两档都提醒当事人）
	remindBatch       = 100
)

// RemindParty 返回问题单在当前状态下该动手的人：待接单是负责人，待验证是发起人。
func RemindParty(st issue.Status, owner *int64, source int64) (int64, bool) {
	switch st {
	case issue.StatusOpen:
		if owner == nil {
			return 0, false
		}
		return *owner, true
	case issue.StatusReadyForVerification:
		return source, true
	}
	return 0, false
}

// QueueReminders 给到点的问题单写催办：工作空间配了群时写群通知（属于“要动手”的通知，投递前状态或负责人变了就不发），
// 同时给当事人写一条会话内提醒；记下档位，同一档不会重复发。返回这一轮处理的问题单数。
// 由投递后台循环大约每分钟调一次；多个进程同时调也不会重复（FOR UPDATE SKIP LOCKED）。
func (s *Service) QueueReminders(ctx context.Context) (int, error) {
	n := 0
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		due, err := store.DueReminders(ctx, tx, remindBatch)
		if err != nil {
			return err
		}
		n = len(due)
		for _, d := range due {
			party, ok := RemindParty(d.Status, d.OwnerID, d.SourceMemberID)
			if !ok { // 待接单却没有负责人：不会发生，记成已催完，免得每分钟都扫到
				if err := store.SetRemindLevel(ctx, tx, d.IssueID, 2); err != nil {
					return err
				}
				continue
			}
			level := d.Due
			if level <= d.Level {
				level = d.Level + 1
			}
			if level > 2 {
				level = 2
			}
			event, to := EventOverdue, []int64{party}
			var watchers []int64
			if level == 2 {
				event = EventOverdueAdmin
				admins, err := store.WorkspaceAdminMembers(ctx, tx, d.WorkspaceID)
				if err != nil {
					return err
				}
				for _, a := range admins {
					if a != party {
						to = append(to, a)
					}
				}
				// 第 2 档才告知关注者（第 1 档只是再提醒当事人一次）；已经在 @ 名单里的不重复
				ws, err := store.WatcherIDs(ctx, tx, d.IssueID)
				if err != nil {
					return err
				}
				watchers = attachWatchers(store.Identity{}, []notice{{event: event, to: to}}, event, ws, nil)[0].watchers
			}
			ws, err := store.GetWorkspace(ctx, tx, d.WorkspaceID)
			if err != nil {
				return err
			}
			if ws.Notify.Channel != "" {
				if err := store.InsertNotification(ctx, tx, store.Notification{
					WorkspaceID: d.WorkspaceID, IssueID: d.IssueID, Event: event,
					ActorMemberID: party, ActorTool: "cohive", Recipients: to, Watchers: watchers,
					IssueStatus: string(d.Status), IssueOwnerID: d.OwnerID,
				}); err != nil {
					return fmt.Errorf("queue overdue reminder notification: %w", err)
				}
			}
			if err := store.InsertAlert(ctx, tx, store.Alert{WorkspaceID: d.WorkspaceID, MemberID: party,
				IssueID: d.IssueID, Kind: AlertOverdue, ActorMemberID: party}); err != nil {
				return err
			}
			for _, w := range watchers {
				if err := store.InsertAlert(ctx, tx, store.Alert{WorkspaceID: d.WorkspaceID, MemberID: w,
					IssueID: d.IssueID, Kind: AlertWatching, About: event, ActorMemberID: party}); err != nil {
					return err
				}
			}
			if err := store.SetRemindLevel(ctx, tx, d.IssueID, level); err != nil {
				return err
			}
		}
		return nil
	})
	return n, err
}

// SetRemindHours 只改超时催办时长（命令行用；网页随工作空间设置一起保存）。为空的不改。
func (s *Service) SetRemindHours(ctx context.Context, act Actor, wsID int64, openH, verifyH *int) error {
	if err := checkRemindHours("core.err.remind_open_hours", openH); err != nil {
		return err
	}
	if err := checkRemindHours("core.err.remind_verify_hours", verifyH); err != nil {
		return err
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockWritable(ctx, tx, wsID); err != nil {
			return err
		}
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		old, err := store.LockWorkspace(ctx, tx, wsID)
		if err != nil {
			return err
		}
		r := old.Remind
		if openH != nil {
			r.OpenHours = *openH
		}
		if verifyH != nil {
			r.VerifyHours = *verifyH
		}
		if r == old.Remind {
			return nil
		}
		if err := store.SetWorkspaceRemind(ctx, tx, wsID, r); err != nil {
			return err
		}
		changes := map[string]any{}
		if r.OpenHours != old.Remind.OpenHours {
			changes["remind_open_hours"] = map[string]any{"from": old.Remind.OpenHours, "to": r.OpenHours}
		}
		if r.VerifyHours != old.Remind.VerifyHours {
			changes["remind_verify_hours"] = map[string]any{"from": old.Remind.VerifyHours, "to": r.VerifyHours}
		}
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(wsID), Action: ActWorkspaceSettings,
			TargetKind: "workspace", TargetID: idp(wsID), TargetName: old.Name, Detail: changes})
	})
}
