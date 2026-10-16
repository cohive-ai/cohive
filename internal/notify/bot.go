package notify

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
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
		return fmt.Sprintf("这个群（%s）的编号是 <code>%d</code>\n\n管理员在 Cohive 网页的“管理 → 工作空间”里填入这个编号，群通知与审批消息就会发到这里。",
			html.EscapeString(m.Chat.Title), m.Chat.ID), true
	case "/whoami":
		if m.From == nil {
			return "", false
		}
		name := "（未设置）"
		if m.From.Username != "" {
			name = "<code>" + html.EscapeString(m.From.Username) + "</code>"
		}
		return fmt.Sprintf("你的 Telegram 用户名是 %s，用户编号是 <code>%d</code>。\n\n要把这个账号连接到 Cohive，请在网页“个人中心”点“连接 Telegram”。", name, m.From.ID), true
	}
	return "", false
}

// chatAPI 是机器人用到的 Telegram 操作，由 *Telegram 实现，测试时可替换。
type chatAPI interface {
	Reply(ctx context.Context, chatID, replyTo int64, text string) error
	SendText(ctx context.Context, chatID int64, text string) error
	SendButtons(ctx context.Context, chatID int64, text string, buttons [][]Button) (int64, error)
	EditMessage(ctx context.Context, chatID, messageID int64, text string, buttons [][]Button) error
	AnswerCallback(ctx context.Context, callbackID, text string) error
}

// Core 是机器人用到的业务操作，由 core.Service 实现。
type Core interface {
	CompleteTelegramLink(ctx context.Context, startParam string, tgUserID int64, username string) (core.LinkResult, error)
	DecideSignup(ctx context.Context, actor *store.Member, id int64, approve bool) (core.SignupDecision, error)
	DecideDeviceAuth(ctx context.Context, actor *store.Member, id int64, approve bool) (core.DeviceDecision, error)
	Workspace(ctx context.Context, id int64) (store.Workspace, error)
	MemberByTelegram(ctx context.Context, wsID, tgUserID int64) (store.Member, error)
}

// Bot 用长轮询接收机器人消息：/chatid、/whoami、连接 Telegram（/start ln_xxx）与群里的审批按钮。
type Bot struct {
	TG   *Telegram
	Core Core    // 为空时只处理 /chatid、/whoami
	API  chatAPI // 为空时使用 TG
	Name string  // 机器人用户名；为空时启动时向 Telegram 查询
	Logf func(format string, args ...any)

	fetched atomic.Pointer[string] // 向 Telegram 查到的用户名
}

// Username 返回机器人用户名；还不知道时返回空。可在其他协程调用。
func (b *Bot) Username() string {
	if p := b.fetched.Load(); p != nil {
		return *p
	}
	return b.Name
}

func (b *Bot) fetchName(ctx context.Context) {
	if b.Username() != "" {
		return
	}
	name, err := b.TG.GetMe(ctx)
	if err != nil || name == "" {
		b.logf("Telegram 机器人信息获取失败，稍后重试（带 @机器人名 的命令与连接 Telegram 暂不可用）：%v", err)
		return
	}
	b.fetched.Store(&name)
}

func (b *Bot) logf(format string, args ...any) {
	if b.Logf != nil {
		b.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func (b *Bot) api() chatAPI {
	if b.API == nil {
		return b.TG
	}
	return b.API
}

// Run 一直运行到 ctx 结束。出错时等待一会儿再继续，不会退出。
func (b *Bot) Run(ctx context.Context) {
	b.fetchName(ctx)
	lastFetch := time.Now()
	var offset int64
	for ctx.Err() == nil {
		if b.Username() == "" && time.Since(lastFetch) > time.Minute {
			b.fetchName(ctx)
			lastFetch = time.Now()
		}
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
			b.Handle(ctx, u)
		}
	}
}

// Handle 处理一条更新：消息命令或按钮点击。
func (b *Bot) Handle(ctx context.Context, u Update) {
	if u.CallbackQuery != nil {
		b.handleCallback(ctx, u.CallbackQuery)
		return
	}
	if u.Message == nil {
		return
	}
	reply, ok := HandleCommand(u, b.Username())
	if !ok && b.Core != nil {
		reply, ok = b.handleStart(ctx, u)
	}
	if !ok || reply == "" {
		return
	}
	if err := b.api().Reply(ctx, u.Message.Chat.ID, u.Message.MessageID, reply); err != nil {
		b.logf("回复 Telegram 命令失败：%v", err)
	}
}

// command 取出消息里发给本机器人的命令名与参数；不是命令或发给别的机器人时返回空。
func (b *Bot) command(m *TGMessage) (string, []string) {
	fields := strings.Fields(m.Text)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return "", nil
	}
	cmd, target, _ := strings.Cut(fields[0], "@")
	if target != "" && !strings.EqualFold(target, b.Username()) {
		return "", nil
	}
	return cmd, fields[1:]
}

const startWelcome = "我是 Cohive 机器人。要把你的 Telegram 账号连接到 Cohive，请在网页“个人中心”点“连接 Telegram”。"

// handleStart 处理私聊里的 /start；带 ln_ 参数时是网页个人中心发起的连接。
func (b *Bot) handleStart(ctx context.Context, u Update) (string, bool) {
	m := u.Message
	cmd, args := b.command(m)
	if cmd != "/start" || m.From == nil || m.Chat.Type != "private" {
		return "", false
	}
	if len(args) == 0 || !strings.HasPrefix(args[0], core.LinkStartPrefix) {
		return startWelcome, true
	}
	res, err := b.Core.CompleteTelegramLink(ctx, args[0], m.From.ID, m.From.Username)
	var ie *core.InputError
	switch {
	case errors.Is(err, core.ErrLinkGone), errors.As(err, &ie):
		return html.EscapeString(err.Error()), true
	case err != nil:
		b.logf("连接 Telegram 失败：%v", err)
		return "暂时无法完成连接，请稍后在网页上重试。", true
	}
	text := fmt.Sprintf("已连接：工作空间 %s 的成员 %s。回到网页即可看到。", html.EscapeString(res.Workspace), html.EscapeString(res.Member))
	if m.From.Username == "" {
		text += "\n你还没有设置 Telegram 用户名，群通知没法 @ 到你，建议在 Telegram 设置里设一个，再重新连接。"
	}
	return text, true
}

// mentionName 是审批消息里展示的成员名与显示名。
func mentionName(name, display string) string {
	s := html.EscapeString(name)
	if display != "" && display != name {
		s += "（" + html.EscapeString(display) + "）"
	}
	return s
}

func approvalButtons(kind string, wsID, id int64) [][]Button {
	key := kind + ":" + strconv.FormatInt(wsID, 10) + ":" + strconv.FormatInt(id, 10)
	return [][]Button{{{Text: "批准", Data: key + ":y"}, {Text: "拒绝", Data: key + ":n"}}}
}

// groupOf 返回工作空间配置的 Telegram 群；没有配置时返回 0。
func (b *Bot) groupOf(ctx context.Context, wsID int64) (store.Workspace, int64, error) {
	ws, err := b.Core.Workspace(ctx, wsID)
	if err != nil {
		return ws, 0, err
	}
	if ws.Notify.Channel != "telegram" || ws.Notify.Target == "" {
		return ws, 0, nil
	}
	chat, err := strconv.ParseInt(ws.Notify.Target, 10, 64)
	return ws, chat, err
}

// AnnounceSignup 在工作空间群里发注册申请的审批消息（没配置群时不发）。
func (b *Bot) AnnounceSignup(ctx context.Context, s store.Signup) error {
	ws, chat, err := b.groupOf(ctx, s.WorkspaceID)
	if err != nil || chat == 0 {
		return err
	}
	text := fmt.Sprintf("新成员 %s 申请加入工作空间 %s。\n请管理员审批（也可以在网页“管理 → 待审”里处理）：",
		mentionName(s.Name, s.DisplayName), html.EscapeString(ws.Name))
	_, err = b.api().SendButtons(ctx, chat, text, approvalButtons("su", s.WorkspaceID, s.ID))
	return err
}

// AnnounceDevice 在工作空间群里发添加设备的审批消息（没配置群时不发）。
func (b *Bot) AnnounceDevice(ctx context.Context, a store.DeviceAuth) error {
	if a.WorkspaceID == nil {
		return nil
	}
	_, chat, err := b.groupOf(ctx, *a.WorkspaceID)
	if err != nil || chat == 0 {
		return err
	}
	text := fmt.Sprintf("成员 %s 申请添加新设备 %s（工具：%s）。\n请管理员审批（也可以在网页“管理 → 待审”里处理）：",
		html.EscapeString(a.MemberName), html.EscapeString(a.DeviceName), html.EscapeString(strings.Join(a.Tools, "、")))
	_, err = b.api().SendButtons(ctx, chat, text, approvalButtons("da", *a.WorkspaceID, a.ID))
	return err
}

// handleCallback 处理审批按钮：su（注册申请）、da（添加设备），数据形如 su:工作空间:编号:y。
// 点击者按其连接的 Telegram 账号找到成员，业务层校验是否为该申请所属工作空间的管理员。
func (b *Bot) handleCallback(ctx context.Context, cb *TGCallback) {
	answer := func(text string) {
		if err := b.api().AnswerCallback(ctx, cb.ID, text); err != nil {
			b.logf("回应按钮点击失败：%v", err)
		}
	}
	parts := strings.Split(cb.Data, ":")
	if b.Core == nil || len(parts) != 4 || (parts[0] != "su" && parts[0] != "da") || (parts[3] != "y" && parts[3] != "n") {
		answer("")
		return
	}
	wsID, err1 := strconv.ParseInt(parts[1], 10, 64)
	id, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil {
		answer("")
		return
	}
	actor, err := b.Core.MemberByTelegram(ctx, wsID, cb.From.ID)
	if errors.Is(err, store.ErrNotFound) {
		answer("你的 Telegram 账号还没有连接到这个工作空间的管理员")
		return
	}
	if err != nil {
		b.logf("识别审批人失败：%v", err)
		answer("处理失败，请稍后再试")
		return
	}
	approve := parts[3] == "y"
	var text string
	switch parts[0] {
	case "su":
		d, err := b.Core.DecideSignup(ctx, &actor, id, approve)
		if err != nil {
			b.failCallback(err, answer)
			return
		}
		text = fmt.Sprintf("新成员 %s 申请加入工作空间 %s：%s（审批人 %s）", mentionName(d.Signup.Name, d.Signup.DisplayName),
			html.EscapeString(d.Workspace.Name), verdict(approve), html.EscapeString(d.Decider))
	case "da":
		d, err := b.Core.DecideDeviceAuth(ctx, &actor, id, approve)
		if err != nil {
			b.failCallback(err, answer)
			return
		}
		text = fmt.Sprintf("成员 %s 申请添加新设备 %s：%s（审批人 %s）", html.EscapeString(d.Auth.MemberName),
			html.EscapeString(d.Auth.DeviceName), verdict(approve), html.EscapeString(d.Decider))
	}
	answer(verdict(approve))
	if cb.Message != nil {
		if err := b.api().EditMessage(ctx, cb.Message.Chat.ID, cb.Message.MessageID, text, nil); err != nil {
			b.logf("更新审批消息失败：%v", err)
		}
	}
}

func verdict(approve bool) string {
	if approve {
		return "已批准"
	}
	return "已拒绝"
}

func (b *Bot) failCallback(err error, answer func(string)) {
	switch {
	case errors.Is(err, core.ErrNotAdmin), errors.Is(err, core.ErrDecided):
		answer(err.Error())
	case errors.Is(err, store.ErrNotFound):
		answer("这条申请不存在")
	default:
		b.logf("处理审批失败：%v", err)
		answer("处理失败，请稍后再试")
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
