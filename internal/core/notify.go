package core

import (
	"context"

	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// 群通知事件名
const (
	EventAssigned  = "assigned"  // 新单交给某人（发出、转交）
	EventDeclined  = "declined"  // 被退回
	EventVerify    = "verify"    // 已修复，请发起人验证
	EventReopened  = "reopened"  // 验证未通过，重开给负责人
	EventEscalated = "escalated" // 需要人介入
	EventAccepted  = "accepted"  // 负责人已接单，告知发起人
	EventClosed    = "closed"    // 发起人验证通过并关闭，告知负责人
)

// InfoEvent 判断是不是“告知型”事件（接单、关单）：只是把发生过的事告诉对方，之后问题单再往前走也不算过时。
func InfoEvent(event string) bool { return event == EventAccepted || event == EventClosed }

// queueNotifications 在状态变更的同一事务里写入待发群通知。
// “轮到某人做事”的动作通知要动手的人；接单、验证通过关单告知另一方，让群里的信息链完整
// （验证不通过走重开，不另发关单）。工作空间没配置群时什么都不写。
func queueNotifications(ctx context.Context, tx store.DBTX, who store.Identity, is store.Issue, act issue.Action, escalated bool) error {
	type item struct {
		event      string
		recipients []int64
	}
	var owner int64
	if is.OwnerID != nil {
		owner = *is.OwnerID
	}
	var items []item
	switch act {
	case issue.ActSubmit, issue.ActTransfer:
		items = append(items, item{EventAssigned, []int64{owner}})
	case issue.ActDecline:
		items = append(items, item{EventDeclined, []int64{is.SourceMemberID}})
	case issue.ActRequestVerification:
		items = append(items, item{EventVerify, []int64{is.SourceMemberID}})
	case issue.ActReopen:
		items = append(items, item{EventReopened, []int64{owner}})
	case issue.ActAccept:
		items = append(items, item{EventAccepted, []int64{is.SourceMemberID}})
	case issue.ActClose:
		items = append(items, item{EventClosed, []int64{owner}})
	}
	if escalated {
		items = append(items, item{EventEscalated, []int64{is.SourceMemberID, owner}})
	}
	if len(items) == 0 {
		return nil
	}

	ws, err := store.GetWorkspace(ctx, tx, is.WorkspaceID)
	if err != nil {
		return err
	}
	if ws.Notify.Channel == "" {
		return nil
	}
	for _, it := range items {
		if err := store.InsertNotification(ctx, tx, store.Notification{
			WorkspaceID: is.WorkspaceID, IssueID: is.ID, Event: it.event,
			ActorMemberID: who.MemberID, ActorTool: who.Tool, Recipients: it.recipients,
			IssueStatus: string(is.Status), IssueOwnerID: is.OwnerID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// queueReassign 在管理员改派的同一事务里给新接手人写待发群通知：
// 新负责人在他该动手的状态下收到“新单交给你”；新发起人在待验证时收到“请验证”。
func queueReassign(ctx context.Context, tx store.DBTX, who store.Identity, is store.Issue, ownerChanged, sourceChanged bool) error {
	// 会话内提醒与有没有配群无关，先写
	if err := queueReassignAlerts(ctx, tx, who, is, ownerChanged, sourceChanged); err != nil {
		return err
	}
	ws, err := store.GetWorkspace(ctx, tx, is.WorkspaceID)
	if err != nil {
		return err
	}
	if ws.Notify.Channel == "" {
		return nil
	}
	add := func(event string, to int64) error {
		return store.InsertNotification(ctx, tx, store.Notification{
			WorkspaceID: is.WorkspaceID, IssueID: is.ID, Event: event,
			ActorMemberID: who.MemberID, ActorTool: who.Tool, Recipients: []int64{to},
			IssueStatus: string(is.Status), IssueOwnerID: is.OwnerID,
		})
	}
	switch is.Status {
	case issue.StatusOpen, issue.StatusAccepted, issue.StatusFixing, issue.StatusReopened:
		if ownerChanged && is.OwnerID != nil {
			if err := add(EventAssigned, *is.OwnerID); err != nil {
				return err
			}
		}
	case issue.StatusReadyForVerification:
		if sourceChanged {
			if err := add(EventVerify, is.SourceMemberID); err != nil {
				return err
			}
		}
	}
	return nil
}
