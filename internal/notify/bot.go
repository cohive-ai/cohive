package notify

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"sync"
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

// Bot 用长轮询接收机器人消息：/chatid、/whoami、连接 Telegram（/start ln_xxx）与群里的审批按钮。
// 它也是 Telegram 的通知通道（实现 Channel）。与通讯工具无关的审批逻辑在 Hub 里。
type Bot struct {
	TG   *Telegram
	Core Core    // 为空时只处理 /chatid、/whoami
	API  chatAPI // 为空时使用 TG
	Name string  // 机器人用户名；为空时启动时向 Telegram 查询
	Logf func(format string, args ...any)
	// Hub 是同时服务多种通讯工具的审批逻辑；为空时按 Core 建一个只含本通道的。
	Hub *Hub

	fetched atomic.Pointer[string] // 向 Telegram 查到的用户名
	lastErr atomic.Pointer[string] // 最近一次接收消息失败的原因；成功后清空
	hubOnce sync.Once
	ownHub  *Hub
}

func (b *Bot) hub() *Hub {
	if b.Hub != nil {
		return b.Hub
	}
	b.hubOnce.Do(func() {
		b.ownHub = &Hub{Core: b.Core, Channels: map[string]Channel{store.ProviderTelegram: b}, Logf: b.Logf}
	})
	return b.ownHub
}

// Status 返回收件循环是否在线与最近一次失败的原因。可在其他协程调用。
func (b *Bot) Status() (online bool, detail string) {
	if p := b.lastErr.Load(); p != nil {
		return false, *p
	}
	return true, ""
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
			msg := err.Error()
			b.lastErr.Store(&msg)
			b.logf("接收 Telegram 消息失败：%v", err)
			sleep(ctx, 10*time.Second)
			continue
		}
		b.lastErr.Store(nil)
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
	if ok && b.Core != nil {
		reply += b.whoamiExtra(ctx, u)
	}
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
	text := fmt.Sprintf("已连接到 Cohive 账号 %s，在你所在的全部工作空间通用。回到网页即可看到。", html.EscapeString(res.Account))
	return text, true
}

// whoamiExtra 在 /whoami 的回复后补上连接的 Cohive 账号；在群里再补上他在本群工作空间的角色。
func (b *Bot) whoamiExtra(ctx context.Context, u Update) string {
	cmd, _ := b.command(u.Message)
	if cmd != "/whoami" || u.Message.From == nil {
		return ""
	}
	chat := ""
	if u.Message.Chat.Type != "private" {
		chat = int64String(u.Message.Chat.ID)
	}
	acc, wsLabel, role, ok := b.hub().Whoami(ctx, tgUser(u.Message.From), chat)
	if !ok {
		return ""
	}
	text := fmt.Sprintf("\n\n已连接的 Cohive 账号：%s", html.EscapeString(acc))
	if wsLabel == "" {
		return text
	}
	return text + fmt.Sprintf("\n在本群对应的工作空间 %s 里：%s", html.EscapeString(wsLabel), role)
}

func tgUser(u *TGUser) ChatUser {
	return ChatUser{Provider: store.ProviderTelegram, UserID: int64String(u.ID), Handle: u.Username, Name: u.FirstName}
}

func approvalButtons(kind string, wsID, id int64) [][]Button {
	key := kind + ":" + strconv.FormatInt(wsID, 10) + ":" + strconv.FormatInt(id, 10)
	return [][]Button{{{Text: "批准", Data: key + ":y"}, {Text: "拒绝", Data: key + ":n"}}}
}

// ---- Telegram 通知通道 ----

func (b *Bot) Provider() string  { return store.ProviderTelegram }
func (b *Bot) Format() Formatter { return TelegramFormat{} }

func tgChat(chat string) (int64, error) {
	id, err := strconv.ParseInt(chat, 10, 64)
	if err != nil {
		return 0, &PermanentError{Msg: fmt.Sprintf("Telegram 群编号应为数字：%q", chat)}
	}
	return id, nil
}

// Send 往群里发一条 HTML 消息。
func (b *Bot) Send(ctx context.Context, chat string, m Rendered) error {
	id, err := tgChat(chat)
	if err != nil {
		return err
	}
	return b.api().SendText(ctx, id, m.Text)
}

// SendApproval 发一条带“批准 / 拒绝”按钮的消息，返回消息编号。
func (b *Bot) SendApproval(ctx context.Context, chat string, m Rendered, a Approval) (string, error) {
	id, err := tgChat(chat)
	if err != nil {
		return "", err
	}
	msg, err := b.api().SendButtons(ctx, id, m.Text, approvalButtons(a.Kind, a.WorkspaceID, a.RefID))
	if err != nil {
		return "", err
	}
	return int64String(msg), nil
}

// FinishApproval 把审批消息改成结果、去掉按钮。
func (b *Bot) FinishApproval(ctx context.Context, chat, msgRef string, m Rendered) error {
	id, err := tgChat(chat)
	if err != nil {
		return err
	}
	msg, err := strconv.ParseInt(msgRef, 10, 64)
	if err != nil {
		return &PermanentError{Msg: fmt.Sprintf("Telegram 消息编号应为数字：%q", msgRef)}
	}
	return b.api().EditMessage(ctx, id, msg, m.Text, nil)
}

// AnnounceSignup 等五个方法实现 httpapi.Announcer，交给 Hub（只配了 Telegram 时使用）。
func (b *Bot) AnnounceSignup(ctx context.Context, s store.Signup) error {
	return b.hub().AnnounceSignup(ctx, s)
}
func (b *Bot) AnnounceDevice(ctx context.Context, a store.DeviceAuth) error {
	return b.hub().AnnounceDevice(ctx, a)
}
func (b *Bot) AnnounceSignupDecided(ctx context.Context, d core.SignupDecision) error {
	return b.hub().AnnounceSignupDecided(ctx, d)
}
func (b *Bot) AnnounceDeviceDecided(ctx context.Context, d core.DeviceDecision) error {
	return b.hub().AnnounceDeviceDecided(ctx, d)
}
func (b *Bot) AnnounceJoined(ctx context.Context, m store.Member) error {
	return b.hub().AnnounceJoined(ctx, m)
}

// handleCallback 处理审批按钮：su（注册与加入申请）、da（添加设备），数据形如 su:工作空间:编号:y。
// 审批逻辑见 Hub.Click；这里只解析按钮数据、回应点击、改被点的那条消息。
func (b *Bot) handleCallback(ctx context.Context, cb *TGCallback) {
	answer := func(text string) {
		if err := b.api().AnswerCallback(ctx, cb.ID, text); err != nil {
			b.logf("回应按钮点击失败：%v", err)
		}
	}
	parts := strings.Split(cb.Data, ":")
	if b.Core == nil || cb.Message == nil || len(parts) != 4 || (parts[0] != "su" && parts[0] != "da") ||
		(parts[3] != "y" && parts[3] != "n") {
		answer("")
		return
	}
	wsID, err1 := strconv.ParseInt(parts[1], 10, 64)
	id, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil {
		answer("")
		return
	}
	chat, msg := int64String(cb.Message.Chat.ID), int64String(cb.Message.MessageID)
	res := b.hub().Click(ctx, Click{Provider: store.ProviderTelegram, ChatID: chat, MsgRef: msg, From: tgUser(&cb.From),
		Approval: Approval{Kind: parts[0], WorkspaceID: wsID, RefID: id}, Approve: parts[3] == "y"})
	answer(res.Toast)
	if res.Result != nil {
		if err := b.FinishApproval(ctx, chat, msg, *res.Result); err != nil {
			b.logf("更新审批消息失败：%v", err)
		}
	}
	if res.After != nil {
		res.After(ctx)
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
