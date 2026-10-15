package notify

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
)

// HandleCommand 处理机器人收到的命令，返回要回复的内容。只认 /chatid 与 /whoami；
// 群里带 @机器人名 的命令只在机器人名匹配时处理，避免和群里其他机器人冲突。
func HandleCommand(u Update, botName string) (string, bool) {
	if u.Message == nil {
		return "", false
	}
	fields := strings.Fields(u.Message.Text)
	if len(fields) == 0 {
		return "", false
	}
	cmd, target, _ := strings.Cut(fields[0], "@")
	if target != "" && !strings.EqualFold(target, botName) {
		return "", false
	}
	m := u.Message
	switch cmd {
	case "/chatid":
		if m.Chat.Type == "private" {
			return "这是和机器人的私聊。要获取群编号，请把机器人拉进群，在群里发 /chatid。", true
		}
		id := fmt.Sprintf("%d", m.Chat.ID)
		return fmt.Sprintf("这个群（%s）的编号是 <code>%s</code>\n\n在协作服务所在的机器上执行，把工作空间名换成你的：\n<code>cohive-server workspace notify --workspace 工作空间名 --telegram-chat %s</code>",
			html.EscapeString(m.Chat.Title), id, id), true
	case "/whoami":
		if m.From == nil {
			return "", false
		}
		if m.From.Username == "" {
			return fmt.Sprintf("你的 Telegram 用户编号是 <code>%d</code>。你还没有设置 Telegram 用户名，请先在 Telegram 的设置里设置，否则群通知没法 @ 到你。\n\n请管理员执行，把工作空间名和成员名换成你的：\n<code>cohive-server member set --workspace 工作空间名 --name 成员名 --telegram-id %d</code>",
				m.From.ID, m.From.ID), true
		}
		name := html.EscapeString(m.From.Username)
		return fmt.Sprintf("你的 Telegram 用户名是 <code>%s</code>，用户编号是 <code>%d</code>\n\n请管理员执行，把工作空间名和成员名换成你的：\n<code>cohive-server member set --workspace 工作空间名 --name 成员名 --telegram %s --telegram-id %d</code>",
			name, m.From.ID, name, m.From.ID), true
	}
	return "", false
}

// LoginIssuer 签发登录码（由 core.Service 实现）。
type LoginIssuer interface {
	IssueLoginCode(ctx context.Context, telegramUserID int64, workspace string) (core.LoginCodeResult, error)
}

// HandleLogin 处理 /login：只在私聊里签发一次性登录码；群里只提示改为私聊。
func HandleLogin(ctx context.Context, u Update, botName string, logins LoginIssuer) (string, bool) {
	if u.Message == nil || u.Message.From == nil {
		return "", false
	}
	fields := strings.Fields(u.Message.Text)
	if len(fields) == 0 {
		return "", false
	}
	cmd, target, _ := strings.Cut(fields[0], "@")
	if cmd != "/login" || (target != "" && !strings.EqualFold(target, botName)) {
		return "", false
	}
	if u.Message.Chat.Type != "private" {
		return "登录码只能私聊领取。请点开我的头像，在私聊里发 /login。", true
	}
	workspace := ""
	if len(fields) > 1 {
		workspace = fields[1]
	}
	uid := u.Message.From.ID
	res, err := logins.IssueLoginCode(ctx, uid, workspace)
	var ie *core.InputError
	switch {
	case errors.Is(err, core.ErrNotBound):
		return fmt.Sprintf("你的 Telegram 账号（用户编号 <code>%d</code>）还没有绑定 Cohive 成员。请把下面这行发给管理员，绑定后再发 /login：\n<code>cohive-server member set --workspace 工作空间名 --name 成员名 --telegram-id %d</code>", uid, uid), true
	case errors.As(err, &ie):
		return html.EscapeString(err.Error()), true
	case err != nil:
		return "暂时无法签发登录码，请稍后再试。", true
	}
	if res.Code == "" {
		var b strings.Builder
		b.WriteString("你加入了多个工作空间，请发送其中一条：")
		for _, name := range res.Choices {
			b.WriteString("\n/login " + html.EscapeString(name))
		}
		return b.String(), true
	}
	server := res.Workspace.PublicURL
	if server == "" {
		server = "协作服务地址"
	}
	return fmt.Sprintf("工作空间 %s 的登录码：<code>%s</code>\n10 分钟内有效，只能用一次，不要转发给别人。\n\n在你的电脑上执行（用 Claude Code 就把 --tool 改成 claude-code）：\n<code>cohive login --server %s --code %s --tool codex</code>\n\n登录后在项目目录执行 <code>cohive init</code>。",
		html.EscapeString(res.Workspace.Name), res.Code, html.EscapeString(server), res.Code), true
}

// Bot 用长轮询接收机器人消息并回复 /chatid、/whoami、/login。
type Bot struct {
	TG     *Telegram
	Logins LoginIssuer // 为空时不处理 /login
	Logf   func(format string, args ...any)
}

func (b *Bot) logf(format string, args ...any) {
	if b.Logf != nil {
		b.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// Run 一直运行到 ctx 结束。出错时等待一会儿再继续，不会退出。
func (b *Bot) Run(ctx context.Context) {
	name, err := b.TG.GetMe(ctx)
	if err != nil {
		b.logf("Telegram 机器人信息获取失败，机器人命令暂不可用：%v", err)
	}
	var offset int64
	for ctx.Err() == nil {
		ups, err := b.TG.GetUpdates(ctx, offset, 30)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.logf("接收 Telegram 消息失败：%v", err)
			sleep(ctx, 10*time.Second)
			continue
		}
		for _, u := range ups {
			offset = u.UpdateID + 1
			reply, ok := HandleCommand(u, name)
			if !ok && b.Logins != nil {
				reply, ok = HandleLogin(ctx, u, name, b.Logins)
			}
			if !ok {
				continue
			}
			if err := b.TG.Reply(ctx, u.Message.Chat.ID, u.Message.MessageID, reply); err != nil {
				b.logf("回复 Telegram 命令失败：%v", err)
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
