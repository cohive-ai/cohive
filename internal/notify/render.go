// Package notify 负责群通知：渲染消息、通过通道投递。
// 群消息只是“敲门”的信号，问题单正文（复现脚本、证据、期望与实际结果、备注）从不出现在消息里。
package notify

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/cohive-ai/cohive/internal/core"
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
func RenderFor(f Formatter, provider, base string, ws store.Workspace, is store.Issue, n store.Notification, people map[int64]Person) Rendered {
	w := newWriter(f, provider)
	esc := w.esc
	mention := func(id int64) string {
		p, ok := people[id]
		if !ok {
			return "（未知成员）"
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
	tool := esc(toolLabel(n.ActorTool))

	var head, via string
	switch n.Event {
	case "assigned":
		head = fmt.Sprintf("#%d 新单交给 %s，请接单、退回或转交", is.ID, mentions(n.Recipients))
		if n.ActorMemberID == is.SourceMemberID {
			via = fmt.Sprintf("由 %s 用 %s 发出", actor, tool)
		} else {
			via = fmt.Sprintf("由 %s 用 %s 转交", actor, tool)
		}
	case "declined":
		head = fmt.Sprintf("#%d 被 %s 退回给 %s，请查看理由后决定下一步", is.ID, actor, mentions(n.Recipients))
	case "verify":
		head = fmt.Sprintf("#%d 已修复，请 %s 验证", is.ID, mentions(n.Recipients))
		via = fmt.Sprintf("由 %s 用 %s 提交验证", actor, tool)
	case "reopened":
		head = fmt.Sprintf("#%d 验证未通过，已重开给 %s", is.ID, mentions(n.Recipients))
	case "escalated":
		head = fmt.Sprintf("#%d 已多次重开，需要人介入：%s", is.ID, mentions(n.Recipients))
	default:
		head = fmt.Sprintf("#%d 有新动态：%s", is.ID, mentions(n.Recipients))
	}

	lines := []string{head}
	var meta []string
	if is.TargetService != "" {
		meta = append(meta, "服务 "+esc(is.TargetService))
	}
	if via != "" {
		meta = append(meta, via)
	}
	if len(meta) > 0 {
		lines = append(lines, strings.Join(meta, "，"))
	}
	if ws.Notify.ShowTitle && is.Title != "" {
		lines = append(lines, "标题："+esc(is.Title))
	}
	if ws.Notify.ShowBrief && is.Brief != "" {
		lines = append(lines, "简述："+esc(is.Brief))
	}
	lines = append(lines, fmt.Sprintf("在编程工具里用 /cohive %d（Codex 用 $cohive %d）处理", is.ID, is.ID))
	if link := core.IssueLink(base, ws.Name, is.ID); link != "" {
		lines = append(lines, esc(link))
	}
	return w.done(fmt.Sprintf("#%d %s", is.ID, eventTitle(n.Event)), lines...)
}

// eventTitle 是钉钉卡片标题与会话列表摘要里的事件名。
func eventTitle(event string) string {
	switch event {
	case "assigned":
		return "新单"
	case "declined":
		return "被退回"
	case "verify":
		return "请验证"
	case "reopened":
		return "已重开"
	case "escalated":
		return "需要人介入"
	}
	return "有新动态"
}

// TelegramFormat 是 Telegram 的 HTML 消息格式。
type TelegramFormat struct{}

func (TelegramFormat) Esc(s string) string  { return html.EscapeString(s) }
func (TelegramFormat) Code(s string) string { return "<code>" + html.EscapeString(s) + "</code>" }

// Mention 见 tgMention；只登记了用户名、没有编号的旧数据退回 @用户名。
func (TelegramFormat) Mention(label string, id *store.ChatIdentity, withLabel bool) string {
	if id == nil {
		return label
	}
	var uid *int64
	if n, err := strconv.ParseInt(id.UserID, 10, 64); err == nil {
		uid = &n
	}
	return tgMention(uid, id.Handle, label, withLabel)
}

func int64String(n int64) string { return strconv.FormatInt(n, 10) }

// tgMention 是群消息里提到一个人。连接了 Telegram 的按用户编号提到（tg://user?id= 链接，不依赖用户名，
// 对方没设或改了用户名都能通知到）；只有用户名没有编号的旧数据退回 @用户名；都没有时只写 label。
// label 必须是已转义的 HTML；withLabel 为 true 时 @用户名 后面再括注 label。
func tgMention(userID *int64, username, label string, withLabel bool) string {
	switch {
	case userID != nil:
		return fmt.Sprintf(`<a href="tg://user?id=%d">%s</a>`, *userID, label)
	case username != "" && withLabel:
		return "@" + html.EscapeString(username) + "（" + label + "）"
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
	}
	return tool
}
