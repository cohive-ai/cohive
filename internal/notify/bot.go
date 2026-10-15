package notify

import (
	"context"
	"fmt"
	"html"
	"log"
	"strings"
	"time"
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
		if m.From == nil || m.From.Username == "" {
			return "你还没有设置 Telegram 用户名。请先在 Telegram 的设置里设置用户名，否则群通知没法 @ 到你。", true
		}
		name := html.EscapeString(m.From.Username)
		return fmt.Sprintf("你的 Telegram 用户名是 <code>%s</code>\n\n请管理员执行，把工作空间名和成员名换成你的：\n<code>cohive-server member set --workspace 工作空间名 --name 成员名 --telegram %s</code>",
			name, name), true
	}
	return "", false
}

// Bot 用长轮询接收机器人消息并回复 /chatid、/whoami。
type Bot struct {
	TG   *Telegram
	Logf func(format string, args ...any)
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
