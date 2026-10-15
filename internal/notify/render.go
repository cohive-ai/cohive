// Package notify 负责群通知：渲染消息、通过通道投递。
// 群消息只是“敲门”的信号，问题单正文（复现脚本、证据、期望与实际结果、备注）从不出现在消息里。
package notify

import (
	"fmt"
	"html"
	"strings"

	"github.com/cohive-ai/cohive/internal/store"
)

// Render 生成一条群消息（Telegram HTML 格式，所有用户输入都已转义）。
func Render(ws store.Workspace, is store.Issue, n store.Notification, members map[int64]store.Member) string {
	esc := html.EscapeString
	mention := func(id int64) string {
		m, ok := members[id]
		if !ok {
			return "（未知成员）"
		}
		if m.TelegramUsername != "" {
			return "@" + m.TelegramUsername
		}
		return esc(m.Name)
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
	if ws.PublicURL != "" {
		lines = append(lines, esc(fmt.Sprintf("%s/issues/%d", strings.TrimRight(ws.PublicURL, "/"), is.ID)))
	}
	return strings.Join(lines, "\n")
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
