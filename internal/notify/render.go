// Package notify 负责群通知：渲染消息、通过通道投递。
// 群消息只是“敲门”的信号，问题单正文（复现脚本、证据、期望与实际结果、备注）从不出现在消息里。
package notify

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// Render 生成一条 Telegram 群消息（HTML 格式，所有用户输入都已转义），见 RenderFor。
func Render(base string, ws store.Workspace, is store.Issue, n store.Notification, members map[int64]store.Member) string {
	people := make(map[int64]Person, len(members))
	for id, m := range members {
		people[id] = personOf(m, nil)
	}
	return RenderFor(TelegramFormat{}, store.ProviderTelegram, base, ws, is, n, people).Text
}

// RenderFor 按某种通讯工具的格式生成一条问题单群消息，所有用户输入都已转义。base 是服务器级对外地址，
// 末尾的问题单链接为“对外地址 + /w/路径/issues/编号”；没设对外地址时不带链接。people 按成员资格编号给出。
// 消息发到工作空间的群里，语言跟随工作空间（没设时用服务器缺省语言）。
func RenderFor(f Formatter, provider, base string, ws store.Workspace, is store.Issue, n store.Notification, people map[int64]Person) Rendered {
	return renderMessage(f, provider, base, ws, is, n, people, i18n.Pick(ws.Language), false)
}

// RenderDirect 生成发给一个人的私聊消息（关注者、留言里被 @ 的人）：语言用收件人的 l，所有人都只写名字、不 @，
// 第一行是“为什么通知你”，后面是服务 / 经由行、标题与简述（照工作空间开关）、查看指引与问题单链接。留言正文从不发。
// 群消息的首行写的是“谁该动手”，私聊没有原收件人，不再重复那一行（事件名已在原因行与 Title 里）。
func RenderDirect(f Formatter, provider, base string, ws store.Workspace, is store.Issue, n store.Notification, people map[int64]Person, l i18n.Lang) Rendered {
	return renderMessage(f, provider, base, ws, is, n, people, l, true)
}

func renderMessage(f Formatter, provider, base string, ws store.Workspace, is store.Issue, n store.Notification, people map[int64]Person, l i18n.Lang, direct bool) Rendered {
	w := newWriter(f, provider, l)
	esc := w.esc
	// 不 @ 操作者本人，只写名字（2026-10-18 lee 定）：自己发单给自己、发起人与负责人是同一人等情况。
	// 超时催办没有操作者，照常 @。
	self := n.ActorMemberID
	if core.SystemEvent(n.Event) {
		self = 0
	}
	mention := func(id int64) string {
		p, ok := people[id]
		if !ok {
			return l.T("notify.unknown_member")
		}
		if id == self || direct {
			return esc(p.Name)
		}
		return w.mention(p, esc(p.Name), false)
	}
	mentions := func(ids []int64) string {
		parts := make([]string, 0, len(ids))
		seen := map[int64]bool{}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				parts = append(parts, mention(id))
			}
		}
		return strings.Join(parts, " ")
	}
	actor := esc(people[n.ActorMemberID].Name)
	// nameOf 是问题单当前的负责人、发起人的名字（只写名字；要 @ 的人在“告知”里）
	nameOf := func(id *int64) string {
		if id == nil {
			return l.T("notify.unknown_member")
		}
		p, ok := people[*id]
		if !ok {
			return l.T("notify.unknown_member")
		}
		return esc(p.Name)
	}
	tool := esc(toolLabel(n.ActorTool))
	d := func(extra i18n.D) i18n.D {
		out := i18n.D{"ID": is.ID, "Actor": actor, "Tool": tool, "Who": mentions(n.Recipients)}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	var head, via string
	switch n.Event {
	case "assigned":
		head = l.T("notify.head.assigned", d(nil))
		if n.ActorMemberID == is.SourceMemberID {
			via = l.T("notify.via.assigned_sent", d(nil))
		} else {
			via = l.T("notify.via.assigned_transferred", d(nil))
		}
	case "declined":
		head = l.T("notify.head.declined", d(nil))
	case "verify":
		head = l.T("notify.head.verify", d(nil))
		via = l.T("notify.via.verify", d(nil))
	case "reopened":
		head = l.T("notify.head.reopened", d(nil))
	case "escalated":
		head = l.T("notify.head.escalated", d(nil))
	case "overdue", "overdue_admin":
		head = overdueHead(l, ws, is.ID, n, mention, mentions)
	case "accepted":
		head = l.T("notify.head.accepted", d(nil))
		via = l.T("notify.via.accepted", d(nil))
	case "closed":
		head = l.T("notify.head.closed", d(nil))
		via = l.T("notify.via.closed", d(nil))
	case "transferred":
		head = l.T("notify.head.transferred", d(i18n.D{"Owner": nameOf(is.OwnerID)}))
		via = l.T("notify.via.transferred", d(nil))
	case "started":
		head = l.T("notify.head.started", d(nil))
		via = l.T("notify.via.started", d(nil))
	case "commented": // 留言正文不进群，只说有新留言、来自谁
		head = l.T("notify.head.commented", d(nil))
	case "reassigned":
		src := is.SourceMemberID
		head = l.T("notify.head.reassigned", d(i18n.D{"Owner": nameOf(is.OwnerID), "Source": nameOf(&src)}))
	case "cancelled": // 作废理由不进群，和退回理由、留言正文一样
		head = l.T("notify.head.cancelled", d(nil))
	default:
		head = l.T("notify.head.default", d(nil))
	}

	lines := []string{head}
	if direct { // 原因行写明哪张单、发生了什么、谁做的（超时催办是系统发起的，不写操作者）；经由行换成发起人与负责人
		key := "notify.direct.watching"
		switch {
		case len(n.Mentions) > 0:
			key = "notify.direct.mentioned"
		case core.SystemEvent(n.Event):
			key = "notify.direct.watching_system"
		}
		lines = []string{l.T(key, i18n.D{"ID": is.ID, "Actor": actor, "Event": esc(eventTitle(l, n.Event))})}
		src := is.SourceMemberID
		via = l.T("notify.direct.parties", i18n.D{"Source": nameOf(&src), "Owner": nameOf(is.OwnerID)})
	}
	var meta []string
	if is.TargetService != "" {
		meta = append(meta, l.T("notify.meta.service", i18n.D{"Value": esc(is.TargetService)}))
	}
	if via != "" {
		meta = append(meta, via)
	}
	if len(meta) > 0 {
		lines = append(lines, strings.Join(meta, l.T("notify.meta_sep")))
	}
	if ws.Notify.ShowTitle && is.Title != "" {
		lines = append(lines, l.T("notify.line.title", i18n.D{"Value": esc(is.Title)}))
	}
	if ws.Notify.ShowBrief && is.Brief != "" {
		lines = append(lines, l.T("notify.line.brief", i18n.D{"Value": esc(is.Brief)}))
	}
	if direct || core.InfoEvent(n.Event) { // 私聊一律是告知性质；群里告知型：对方不必动手，只是可以去看
		lines = append(lines, l.T("notify.action.view", i18n.D{"ID": is.ID}))
	} else {
		lines = append(lines, l.T("notify.action.handle", i18n.D{"ID": is.ID}))
	}
	if link := core.IssueLink(base, ws.Name, is.ID); link != "" {
		lines = append(lines, f.Link(l, link))
	}
	return w.done(fmt.Sprintf("#%d %s", is.ID, eventTitle(l, n.Event)), lines...)
}

// overdueHead 是超时催办的首行。收件人第一个是当事人（待接单的负责人、待验证的发起人），其余是工作空间管理员。
// 时长取工作空间当前的设置；投递前改小或关掉了，就不写具体小时数。
func overdueHead(l i18n.Lang, ws store.Workspace, id int64, n store.Notification, mention func(int64) string, mentions func([]int64) string) string {
	waiting, todo, hours := l.T("notify.overdue.waiting_accept"), l.T("notify.overdue.todo_accept"), ws.Remind.OpenHours
	if n.IssueStatus == string(issue.StatusReadyForVerification) {
		waiting, todo, hours = l.T("notify.overdue.waiting_verify"), l.T("notify.overdue.todo_verify"), ws.Remind.VerifyHours
	}
	if len(n.Recipients) == 0 {
		return l.T("notify.overdue.no_recipient", i18n.D{"ID": id, "Waiting": waiting})
	}
	party, rest := n.Recipients[0], n.Recipients[1:]
	long := func(times int) string {
		if hours <= 0 {
			return l.T("notify.overdue.long_unknown", i18n.D{"Waiting": waiting})
		}
		return l.T("notify.overdue.long_hours", i18n.D{"Waiting": waiting, "Hours": hours * times})
	}
	if n.Event == "overdue" {
		return l.T("notify.overdue.first", i18n.D{"ID": id, "Long": long(1), "Who": mention(party), "Todo": todo})
	}
	if len(rest) == 0 {
		return l.T("notify.overdue.second", i18n.D{"ID": id, "Long": long(2), "Who": mention(party), "Todo": todo})
	}
	return l.T("notify.overdue.second_admins", i18n.D{"ID": id, "Long": long(2), "Who": mention(party), "Todo": todo,
		"Admins": mentions(rest)})
}

// eventTitle 是钉钉卡片标题与会话列表摘要里的事件名。
func eventTitle(l i18n.Lang, event string) string {
	switch event {
	case "assigned":
		return l.T("notify.event.assigned")
	case "declined":
		return l.T("notify.event.declined")
	case "verify":
		return l.T("notify.event.verify")
	case "reopened":
		return l.T("notify.event.reopened")
	case "escalated":
		return l.T("notify.event.escalated")
	case "overdue":
		return l.T("notify.event.overdue")
	case "overdue_admin":
		return l.T("notify.event.overdue_admin")
	case "accepted":
		return l.T("notify.event.accepted")
	case "closed":
		return l.T("notify.event.closed")
	case "transferred":
		return l.T("notify.event.transferred")
	case "started":
		return l.T("notify.event.started")
	case "commented":
		return l.T("notify.event.commented")
	case "reassigned":
		return l.T("notify.event.reassigned")
	case "cancelled":
		return l.T("notify.event.cancelled")
	}
	return l.T("notify.event.default")
}

// TelegramFormat 是 Telegram 的 HTML 消息格式。
type TelegramFormat struct{}

func (TelegramFormat) Esc(s string) string               { return html.EscapeString(s) }
func (TelegramFormat) Code(s string) string              { return "<code>" + html.EscapeString(s) + "</code>" }
func (TelegramFormat) Link(_ i18n.Lang, u string) string { return html.EscapeString(u) }

// Mention 见 tgMention；只登记了用户名、没有编号的旧数据退回 @用户名。
func (TelegramFormat) Mention(l i18n.Lang, label string, id *store.ChatIdentity, withLabel bool) string {
	if id == nil {
		return label
	}
	var uid *int64
	if n, err := strconv.ParseInt(id.UserID, 10, 64); err == nil {
		uid = &n
	}
	return tgMention(l, uid, id.Handle, label, withLabel)
}

func int64String(n int64) string { return strconv.FormatInt(n, 10) }

// tgMention 是群消息里提到一个人。连接了 Telegram 的按用户编号提到（tg://user?id= 链接，不依赖用户名，
// 对方没设或改了用户名都能通知到）；只有用户名没有编号的旧数据退回 @用户名；都没有时只写 label。
// label 必须是已转义的 HTML；withLabel 为 true 时 @用户名 后面再括注 label。
func tgMention(l i18n.Lang, userID *int64, username, label string, withLabel bool) string {
	switch {
	case userID != nil:
		return fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, *userID, label)
	case username != "" && withLabel:
		return "@" + html.EscapeString(username) + parens(l, label)
	case username != "":
		return "@" + html.EscapeString(username)
	}
	return label
}

func toolLabel(tool string) string {
	switch tool {
	case "claude-code":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "opencode":
		return "OpenCode"
	}
	return tool
}
