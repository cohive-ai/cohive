package core

import (
	"context"
	"time"

	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// 会话内提醒的种类。前五种与群通知同口径；接单、关闭只在会话里提醒，群里不发。
const (
	AlertAssigned  = "assigned"  // 新单交给你（发出、转交、管理员改派）
	AlertDeclined  = "declined"  // 你发的单被退回
	AlertVerify    = "verify"    // 请你验证
	AlertReopened  = "reopened"  // 你修的单被重开
	AlertEscalated = "escalated" // 需要人介入
	AlertAccepted  = "accepted"  // 你发的单被接单
	AlertClosed    = "closed"    // 你修的单验证通过、已关闭
)

const (
	// alertGrace 内写入的提醒总是一并返回：编号小的可能后提交，只按编号会漏。
	alertGrace = 10 * time.Second
	alertLimit = 20
	pendingTop = 3
)

// queueAlerts 在状态变更的同一事务里写会话内提醒。操作者本人不收；评论、开始修复不提醒。
func queueAlerts(ctx context.Context, tx store.DBTX, who store.Identity, is store.Issue, act issue.Action, escalated bool) error {
	var owner int64
	if is.OwnerID != nil {
		owner = *is.OwnerID
	}
	type item struct {
		kind string
		to   int64
	}
	var items []item
	switch act {
	case issue.ActSubmit, issue.ActTransfer:
		items = append(items, item{AlertAssigned, owner})
	case issue.ActDecline:
		items = append(items, item{AlertDeclined, is.SourceMemberID})
	case issue.ActRequestVerification:
		items = append(items, item{AlertVerify, is.SourceMemberID})
	case issue.ActReopen:
		items = append(items, item{AlertReopened, owner})
	case issue.ActAccept:
		items = append(items, item{AlertAccepted, is.SourceMemberID})
	case issue.ActClose:
		items = append(items, item{AlertClosed, owner})
	}
	if escalated {
		items = append(items, item{AlertEscalated, is.SourceMemberID}, item{AlertEscalated, owner})
	}
	seen := map[item]bool{}
	for _, it := range items {
		if it.to == 0 || it.to == who.MemberID || seen[it] {
			continue
		}
		seen[it] = true
		if err := store.InsertAlert(ctx, tx, store.Alert{WorkspaceID: is.WorkspaceID, MemberID: it.to,
			IssueID: is.ID, Kind: it.kind, ActorMemberID: who.MemberID}); err != nil {
			return err
		}
	}
	return nil
}

// queueReassignAlerts 在管理员改派的同一事务里提醒新接手人，口径与改派的群通知相同。
func queueReassignAlerts(ctx context.Context, tx store.DBTX, who store.Identity, is store.Issue, ownerChanged, sourceChanged bool) error {
	add := func(kind string, to int64) error {
		if to == who.MemberID {
			return nil
		}
		return store.InsertAlert(ctx, tx, store.Alert{WorkspaceID: is.WorkspaceID, MemberID: to,
			IssueID: is.ID, Kind: kind, ActorMemberID: who.MemberID})
	}
	switch is.Status {
	case issue.StatusOpen, issue.StatusAccepted, issue.StatusFixing, issue.StatusReopened:
		if ownerChanged && is.OwnerID != nil {
			return add(AlertAssigned, *is.OwnerID)
		}
	case issue.StatusReadyForVerification:
		if sourceChanged {
			return add(AlertVerify, is.SourceMemberID)
		}
	}
	return nil
}

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
				out.PendingTop = append(out.PendingTop, PendingItem{IssueID: is.ID, Needs: needsMe(who, is)})
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
				out.Items = append(out.Items, AlertItem{ID: a.ID, IssueID: a.IssueID, Kind: a.Kind, Actor: a.ActorName, At: a.CreatedAt})
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
		out.Items = append(out.Items, AlertItem{ID: a.ID, IssueID: a.IssueID, Kind: a.Kind, Actor: a.ActorName, At: a.CreatedAt})
		if a.ID > out.Cursor {
			out.Cursor = a.ID
		}
	}
	return out, nil
}
