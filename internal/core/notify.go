package core

import (
	"context"

	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// 群通知事件名。会话内提醒的种类与之同名（见 alert.go）。
const (
	EventAssigned    = "assigned"    // 新单交给某人（发出、转交、改派）
	EventDeclined    = "declined"    // 被退回
	EventVerify      = "verify"      // 已修复，请发起人验证
	EventReopened    = "reopened"    // 验证未通过，重开给负责人
	EventEscalated   = "escalated"   // 需要人介入
	EventAccepted    = "accepted"    // 负责人已接单，告知发起人
	EventClosed      = "closed"      // 发起人验证通过并关闭，告知负责人
	EventTransferred = "transferred" // 转交给了别人，告知发起人
	EventStarted     = "started"     // 负责人开始修复，告知发起人
	EventCommented   = "commented"   // 有新留言，告知另一方（正文不进群）
	EventReassigned  = "reassigned"  // 管理员改派，告知被换下的人、另一方与不用动手的新接手人
	EventCancelled   = "cancelled"   // 作废（发起人撤回或管理员作废），告知另一方（理由不进群）
)

// SystemEvent 判断是不是服务端自己发起的事件（超时催办）：没有操作者，通知里记的操作人只是占位。
func SystemEvent(event string) bool { return event == EventOverdue || event == EventOverdueAdmin }

// InfoEvent 判断是不是“告知型”事件：只是把发生过的事告诉对方，之后问题单再往前走也不算过时。
func InfoEvent(event string) bool {
	switch event {
	case EventAccepted, EventClosed, EventTransferred, EventStarted, EventCommented, EventReassigned, EventCancelled:
		return true
	}
	return false
}

// notice 是一次变化要通知的一组人：群通知与会话内提醒共用这份清单（2026-10-18 lee 定：信息链完整）。
type notice struct {
	event    string
	to       []int64        // 原收件人：要动手的人或直接相关方
	watchers []int64        // 关注者：告知性质，由机器人私聊（不进群消息）
	mentions []int64        // 留言里被 @ 到的人（只就这一条留言），同样私聊
	noAlert  map[int64]bool // 只在群里 @、不写会话内提醒的人（改派时被换下、已经用不了编程工具的人）
}

// parties 去重、去掉 0；others 再去掉操作者本人。
func parties(ids ...int64) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, id := range ids {
		if id != 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func others(who store.Identity, ids ...int64) []int64 {
	var out []int64
	for _, id := range parties(ids...) {
		if id != who.MemberID {
			out = append(out, id)
		}
	}
	return out
}

// fanout 算出问题单动作要通知谁。动作本身的通知（发出、接单、开始修复……）在对方就是操作者本人时照常发，
// 群里只写名字（N-3）；附带的告知（转交告知发起人、留言）没有别人可告知时不发。
func fanout(who store.Identity, is store.Issue, act issue.Action, escalated bool) []notice {
	var owner int64
	if is.OwnerID != nil {
		owner = *is.OwnerID
	}
	var out []notice
	primary := func(event string, ids ...int64) {
		to := others(who, ids...)
		if len(to) == 0 {
			to = parties(ids...)
		}
		if len(to) > 0 {
			out = append(out, notice{event: event, to: to})
		}
	}
	secondary := func(event string, ids ...int64) { // 已经收到本次主通知的人不重复告知（例如转交给了发起人本人）
		got := map[int64]bool{}
		for _, n := range out {
			for _, id := range n.to {
				got[id] = true
			}
		}
		var to []int64
		for _, id := range others(who, ids...) {
			if !got[id] {
				to = append(to, id)
			}
		}
		if len(to) > 0 {
			out = append(out, notice{event: event, to: to})
		}
	}
	switch act {
	case issue.ActSubmit:
		primary(EventAssigned, owner)
	case issue.ActTransfer:
		primary(EventAssigned, owner)
		secondary(EventTransferred, is.SourceMemberID)
	case issue.ActAccept:
		primary(EventAccepted, is.SourceMemberID)
	case issue.ActDecline:
		primary(EventDeclined, is.SourceMemberID)
	case issue.ActStart:
		primary(EventStarted, is.SourceMemberID)
	case issue.ActRequestVerification:
		primary(EventVerify, is.SourceMemberID)
	case issue.ActClose:
		primary(EventClosed, owner)
	case issue.ActReopen:
		primary(EventReopened, owner)
	case issue.ActCancel: // 发起人作废告知负责人，管理员作废告知双方
		primary(EventCancelled, is.SourceMemberID, owner)
	case issue.ActComment:
		if is.Status != issue.StatusDraft { // 草稿只有发起人自己看得见
			secondary(EventCommented, is.SourceMemberID, owner)
		}
	}
	if escalated { // 需要人介入：双方都列出（操作者只写名字）
		out = append(out, notice{event: EventEscalated, to: parties(is.SourceMemberID, owner)})
	}
	return out
}

// reassignFanout 算出管理员改派要通知谁：新接手人在要他动手的状态下收到“新单交给你”“请验证”；
// 被换下的人、没换的另一方，以及当前状态下不用动手的新接手人收到“已改派”的告知。被换下的人只在群里 @。
func reassignFanout(who store.Identity, is store.Issue, oldOwner *int64, oldSource int64, ownerChanged, sourceChanged bool) []notice {
	var out []notice
	ownerActs, sourceActs := false, false
	switch is.Status {
	case issue.StatusOpen, issue.StatusAccepted, issue.StatusFixing, issue.StatusReopened:
		if ownerChanged && is.OwnerID != nil {
			out = append(out, notice{event: EventAssigned, to: parties(*is.OwnerID)})
			ownerActs = true
		}
	case issue.StatusReadyForVerification:
		if sourceChanged {
			out = append(out, notice{event: EventVerify, to: parties(is.SourceMemberID)})
			sourceActs = true
		}
	}
	var info []int64
	gone := map[int64]bool{}
	if ownerChanged {
		if oldOwner != nil {
			info = append(info, *oldOwner)
			gone[*oldOwner] = true
		}
		if is.OwnerID != nil && !ownerActs {
			info = append(info, *is.OwnerID)
		}
	} else if is.OwnerID != nil {
		info = append(info, *is.OwnerID)
	}
	if sourceChanged {
		info = append(info, oldSource)
		gone[oldSource] = true
		if !sourceActs {
			info = append(info, is.SourceMemberID)
		}
	} else {
		info = append(info, is.SourceMemberID)
	}
	acted := map[int64]bool{} // 已经收到“要动手”通知的人不再重复告知
	for _, n := range out {
		for _, id := range n.to {
			acted[id] = true
		}
	}
	var to []int64
	for _, id := range others(who, info...) {
		if !acted[id] {
			to = append(to, id)
		}
	}
	if len(to) > 0 {
		out = append(out, notice{event: EventReassigned, to: to, noAlert: gone})
	}
	return out
}

// attachWatchers 把关注者与被 @ 的人挂到本次动作的主通知（items[0]）上，由 queueNotices 为每个人单独写一条私聊
// （不进群消息）；没有任何原收件人（items 为空）时，新建一条只有关注者 / 被 @ 的人的通知，event 是本次动作的主事件。
// 操作者本人不收；已经是原收件人的不重复；既被 @ 又是关注者的人只按“被 @”收。
func attachWatchers(who store.Identity, items []notice, event string, watchers, mentions []int64) []notice {
	got := map[int64]bool{who.MemberID: true}
	for _, n := range items {
		for _, id := range n.to {
			got[id] = true
		}
	}
	var ms, ws []int64
	for _, id := range parties(mentions...) {
		if !got[id] {
			ms = append(ms, id)
		}
	}
	for _, id := range ms {
		got[id] = true
	}
	for _, id := range parties(watchers...) {
		if !got[id] {
			ws = append(ws, id)
		}
	}
	if len(ws) == 0 && len(ms) == 0 {
		return items
	}
	if len(items) == 0 {
		return []notice{{event: event, watchers: ws, mentions: ms}}
	}
	items[0].watchers, items[0].mentions = ws, ms
	return items
}

// actEvent 是动作的主事件名：群里没有任何原收件人时，关注者按它收通知。
func actEvent(act issue.Action) string {
	switch act {
	case issue.ActSubmit, issue.ActTransfer:
		return EventAssigned
	case issue.ActAccept:
		return EventAccepted
	case issue.ActDecline:
		return EventDeclined
	case issue.ActStart:
		return EventStarted
	case issue.ActRequestVerification:
		return EventVerify
	case issue.ActClose:
		return EventClosed
	case issue.ActReopen:
		return EventReopened
	case issue.ActCancel:
		return EventCancelled
	case issue.ActComment:
		return EventCommented
	}
	return string(act)
}

// queueNotices 在状态变更的同一事务里写会话内提醒（与有没有配群无关，操作者本人不收）、待发私聊（关注者与被 @ 的人，一人一条）与待发群通知（工作空间配了群时，只含原收件人）。
func queueNotices(ctx context.Context, tx store.DBTX, who store.Identity, is store.Issue, items []notice) error {
	if len(items) == 0 {
		return nil
	}
	for _, it := range items {
		for _, to := range it.to {
			if to == who.MemberID || it.noAlert[to] {
				continue
			}
			if err := store.InsertAlert(ctx, tx, store.Alert{WorkspaceID: is.WorkspaceID, MemberID: to,
				IssueID: is.ID, Kind: it.event, ActorMemberID: who.MemberID}); err != nil {
				return err
			}
		}
		for _, g := range []struct {
			kind string
			ids  []int64
		}{{AlertMentioned, it.mentions}, {AlertWatching, it.watchers}} {
			for _, to := range g.ids {
				if to == who.MemberID {
					continue
				}
				if err := store.InsertAlert(ctx, tx, store.Alert{WorkspaceID: is.WorkspaceID, MemberID: to,
					IssueID: is.ID, Kind: g.kind, About: it.event, ActorMemberID: who.MemberID}); err != nil {
					return err
				}
			}
		}
	}
	// 私聊：关注者与被 @ 的人各一条，与工作空间有没有绑群无关；有没有连接通讯账号在投递时判断。
	// 会话提醒已在上面写好，与私聊成败无关。
	for _, it := range items {
		for _, g := range []struct {
			mentioned bool
			ids       []int64
		}{{true, it.mentions}, {false, it.watchers}} {
			for _, to := range g.ids {
				if to == who.MemberID {
					continue
				}
				if err := queueDirect(ctx, tx, is, it.event, to, who.MemberID, who.Tool, g.mentioned); err != nil {
					return err
				}
			}
		}
	}
	ws, err := store.GetWorkspace(ctx, tx, is.WorkspaceID)
	if err != nil {
		return err
	}
	if ws.Notify.Channel == "" {
		return nil
	}
	for _, it := range items {
		if len(it.to) == 0 { // 只有关注者 / 被 @ 的人：只私聊，不进群
			continue
		}
		if err := store.InsertNotification(ctx, tx, store.Notification{
			WorkspaceID: is.WorkspaceID, IssueID: is.ID, Event: it.event,
			ActorMemberID: who.MemberID, ActorTool: who.Tool, Recipients: it.to,
			IssueStatus: string(is.Status), IssueOwnerID: is.OwnerID,
		}); err != nil {
			return err
		}
	}
	return nil
}

// queueDirect 写一条发给 to 的私聊通知（关注者或留言里被 @ 的人）。与工作空间有没有绑群无关；
// 有没有连接通讯账号、能不能发出在投递时判断。mentioned 为真原因是被 @，否则是关注。
func queueDirect(ctx context.Context, tx store.DBTX, is store.Issue, event string, to, actor int64, tool string, mentioned bool) error {
	n := store.Notification{WorkspaceID: is.WorkspaceID, IssueID: is.ID, Event: event,
		ActorMemberID: actor, ActorTool: tool, DirectMemberID: &to,
		IssueStatus: string(is.Status), IssueOwnerID: is.OwnerID}
	if mentioned {
		n.Mentions = []int64{to}
	} else {
		n.Watchers = []int64{to}
	}
	return store.InsertNotification(ctx, tx, n)
}
