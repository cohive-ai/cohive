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
)

// queueNotifications 在状态变更的同一事务里写入待发群通知。
// 只有“轮到某人做事”的动作才通知；工作空间没配置群时什么都不写。
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
