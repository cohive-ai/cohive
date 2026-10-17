// Package notify 负责群通知：渲染消息、通过通道投递。
// 群消息只是“敲门”的信号，问题单正文（复现脚本、证据、期望与实际结果、备注）从不出现在消息里。
package notify

import (
	"fmt"
	"html"
	"strings"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// Render 生成一条群消息（Telegram HTML 格式，所有用户输入都已转义）。base 是服务器级对外地址，
// 末尾的问题单链接为“对外地址 + /w/路径/issues/编号”；没设对外地址时不带链接。
func Render(base string, ws store.Workspace, is store.Issue, n store.Notification, members map[int64]store.Member) string {
	esc := html.EscapeString
	mention := func(id int64) string {
		m, ok := members[id]
		if !ok {
			return "（未知成员）"
		}
		return tgMention(m.TelegramUserID, m.TelegramUsername, esc(m.Name), false)
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
	actor := esc(members[n.ActorMemberID].Name)
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
	return strings.Join(lines, "\n")
}

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
