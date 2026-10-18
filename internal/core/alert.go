package core

import (
	"context"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// 会话内提醒的种类，与群通知事件同名、同口径（收件人由 notify.go 的 fanout 统一算出）。
const (
	AlertAssigned    = EventAssigned    // 新单交给你（发出、转交、管理员改派）
	AlertDeclined    = EventDeclined    // 你发的单被退回
	AlertVerify      = EventVerify      // 请你验证
	AlertReopened    = EventReopened    // 你修的单被重开
	AlertEscalated   = EventEscalated   // 需要人介入
	AlertAccepted    = EventAccepted    // 你发的单被接单
	AlertClosed      = EventClosed      // 你修的单验证通过、已关闭
	AlertTransferred = EventTransferred // 你发的单被转交给了别人
	AlertStarted     = EventStarted     // 你发的单开始修复了
	AlertCommented   = EventCommented   // 有新留言
	AlertReassigned  = EventReassigned  // 管理员改派了你相关的单
)

const (
	// alertGrace 内写入的提醒总是一并返回：编号小的可能后提交，只按编号会漏。
	alertGrace = 10 * time.Second
	alertLimit = 20
	pendingTop = 3
)

// AlertItem 是返回给会话钩子的一条提醒。
type AlertItem struct {
	ID      int64
	IssueID int64
	Kind    string
	Actor   string // 登录名
	At      time.Time
}

// PendingItem 是待办摘要里的一张单：needs 是服务端固定文案，不含退回理由等成员写的内容。
type PendingItem struct {
	IssueID int64
	Needs   string
}

type AlertsResult struct {
	Cursor       int64
	Items        []AlertItem
	More         bool
	PendingCount int
	PendingTop   []PendingItem
}

// Alerts 返回会话钩子要的东西。after 为空（会话开始）：待办摘要、当前游标，以及最近写入、已算进摘要的提醒（只供去重）；
// 否则：编号大于 after 的提醒（另加最近写入的，调用方去重），游标为其中最大的编号。
func (s *Service) Alerts(ctx context.Context, who store.Identity, after *int64) (AlertsResult, error) {
	var out AlertsResult
	if after == nil {
		l := i18n.FromContext(ctx)
		list, err := store.ListInbox(ctx, s.Pool, who.MemberID)
		if err != nil {
			return out, err
		}
		for _, is := range list {
			if is.WorkspaceID != who.WorkspaceID || is.Status == issue.StatusDraft {
				continue
			}
			out.PendingCount++
			if len(out.PendingTop) < pendingTop {
				out.PendingTop = append(out.PendingTop, PendingItem{IssueID: is.ID, Needs: needsMe(l, who, is)})
			}
		}
		if out.Cursor, err = store.LastAlertID(ctx, s.Pool, who.MemberID); err != nil {
			return out, err
		}
		// 附上刚写入、已算进摘要的提醒，调用方记为“已提醒”：之后按编号补查最近写入的提醒时不再重复
		recent, err := store.AlertsAfter(ctx, s.Pool, who.MemberID, out.Cursor, alertGrace, alertLimit)
		if err != nil {
			return out, err
		}
		for _, a := range recent {
			if a.ID <= out.Cursor && a.WorkspaceID == who.WorkspaceID {
				out.Items = append(out.Items, AlertItem{ID: a.ID, IssueID: a.IssueID, Kind: a.Kind, Actor: alertActor(a), At: a.CreatedAt})
			}
		}
		return out, nil
	}
	list, err := store.AlertsAfter(ctx, s.Pool, who.MemberID, *after, alertGrace, alertLimit+1)
	if err != nil {
		return out, err
	}
	if len(list) > alertLimit {
		list, out.More = list[:alertLimit], true
	}
	out.Cursor = *after
	for _, a := range list {
		if a.WorkspaceID != who.WorkspaceID {
			continue
		}
		out.Items = append(out.Items, AlertItem{ID: a.ID, IssueID: a.IssueID, Kind: a.Kind, Actor: alertActor(a), At: a.CreatedAt})
		if a.ID > out.Cursor {
			out.Cursor = a.ID
		}
	}
	return out, nil
}

// alertActor 是提醒里的“谁”。超时催办是服务端发起的，记的操作人只是占位（当事人自己），不对外给出。
func alertActor(a store.Alert) string {
	if a.Kind == AlertOverdue {
		return ""
	}
	return a.ActorName
}
