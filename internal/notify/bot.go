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
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// tgCaps：Telegram 用带参数的机器人链接连接账号（见 handleStart），群绑定在网页填群编号，只回应认得的命令。
var tgCaps = InboxCaps{}

// tgInbound 把发给本机器人的 /chatid、/whoami 翻译成统一的收件消息；别的消息、发给别的机器人的命令返回假。
// 群里带 @机器人名 的命令只在机器人名匹配时处理，避免和群里其他机器人冲突。
func tgInbound(m *TGMessage, botName string) (Inbound, bool) {
	if m == nil {
		return Inbound{}, false
	}
	fields := strings.Fields(m.Text)
	if len(fields) == 0 {
		return Inbound{}, false
	}
	cmd, target, _ := strings.Cut(fields[0], "@")
	if target != "" && !strings.EqualFold(target, botName) {
		return Inbound{}, false
	}
	if cmd != "/chatid" && cmd != "/whoami" {
		return Inbound{}, false
	}
	in := Inbound{Provider: store.ProviderTelegram, ChatID: int64String(m.Chat.ID), ChatTitle: m.Chat.Title,
		Private: m.Chat.Type == "private", Cmd: strings.TrimPrefix(cmd, "/"), First: fields[0], Args: fields[1:]}
	if m.From != nil {
		in.From = tgUser(m.From)
	}
	return in, true
}

// tgReplyText 按 Telegram 的文案与 HTML 写出回复。
func tgReplyText(r Reply, in Inbound) string {
	l := r.Lang
	switch r.Kind {
	case ReplyChatID:
		if r.Private {
			return l.T("notify.tg.chatid_private")
		}
		return l.T("notify.tg.chatid_group", i18n.D{"Title": html.EscapeString(in.ChatTitle), "ID": in.ChatID})
	case ReplyWhoami:
		if in.From.UserID == "" {
			return ""
		}
		name := l.T("notify.tg.whoami_no_username")
		if in.From.Handle != "" {
			name = "<code>" + html.EscapeString(in.From.Handle) + "</code>"
		}
		text := l.T("notify.tg.whoami", i18n.D{"Name": name, "ID": in.From.UserID})
		if !r.Linked { // 没连接或没有业务层：只说 Telegram 这边的身份
			return text
		}
		text += l.T("notify.tg.whoami_account", i18n.D{"Account": html.EscapeString(r.Account)})
		if r.Workspace != "" {
			text += l.T("notify.tg.whoami_role", i18n.D{"Workspace": html.EscapeString(r.Workspace), "Role": r.Role})
		}
		return text
	}
	return ""
}

// HandleCommand 处理机器人收到的 /chatid 与 /whoami（不查业务层），返回要回复的内容。lang 是回复的语言。
func HandleCommand(u Update, botName string, lang i18n.Lang) (string, bool) {
	in, ok := tgInbound(u.Message, botName)
	if !ok {
		return "", false
	}
	text := tgReplyText(Inbox{Caps: tgCaps, Lang: lang}.Handle(context.Background(), in), in)
	return text, text != ""
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
		b.logf("failed to fetch Telegram bot info, will retry (commands with @botname and Telegram linking are unavailable meanwhile): %v", err)
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
			b.logf("failed to receive Telegram updates: %v", err)
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
	var reply string
	if in, ok := tgInbound(u.Message, b.Username()); ok {
		x := Inbox{Caps: tgCaps, Logf: b.logf}
		if b.Core != nil {
			x.Hub = b.hub()
		}
		reply = tgReplyText(x.Handle(ctx, in), in)
	} else if b.Core != nil {
		reply, _ = b.handleStart(ctx, u, b.replyLang(ctx, u.Message))
	}
	if reply == "" {
		return
	}
	if err := b.api().Reply(ctx, u.Message.Chat.ID, u.Message.MessageID, reply); err != nil {
		b.logf("failed to reply to Telegram command: %v", err)
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

// replyLang 是机器人回复这条消息用的语言：群里用群所属工作空间的语言，私聊用对方连接的 Cohive 账号的语言；
// 群还没绑定、对方还没连接账号、没有业务层时用服务器缺省语言。
func (b *Bot) replyLang(ctx context.Context, m *TGMessage) i18n.Lang {
	if b.Core == nil || m == nil {
		return i18n.Default()
	}
	if m.Chat.Type != "private" {
		return b.hub().chatLang(ctx, store.ProviderTelegram, int64String(m.Chat.ID))
	}
	if m.From == nil {
		return i18n.Default()
	}
	return b.hub().accountLang(ctx, store.ProviderTelegram, "", int64String(m.From.ID))
}

// handleStart 处理私聊里的 /start；带 ln_ 参数时是网页个人中心发起的连接。
func (b *Bot) handleStart(ctx context.Context, u Update, lang i18n.Lang) (string, bool) {
	m := u.Message
	cmd, args := b.command(m)
	if cmd != "/start" || m.From == nil || m.Chat.Type != "private" {
		return "", false
	}
	if len(args) == 0 || !strings.HasPrefix(args[0], core.LinkStartPrefix) {
		return lang.T("notify.tg.start_welcome"), true
	}
	res, err := b.Core.CompleteTelegramLink(ctx, args[0], m.From.ID, m.From.Username)
	var ie *core.InputError
	switch {
	case errors.Is(err, core.ErrLinkGone), errors.As(err, &ie):
		return html.EscapeString(i18n.Message(lang, err)), true
	case err != nil:
		b.logf("failed to link Telegram: %v", err)
		return lang.T("notify.tg.link_failed"), true
	}
	// 刚连接上：之后的话发给这个账号，按账号的语言
	lang = b.hub().accountLang(ctx, store.ProviderTelegram, "", int64String(m.From.ID))
	return lang.T("notify.tg.linked", i18n.D{"Account": html.EscapeString(res.Account)}), true
}

func tgUser(u *TGUser) ChatUser {
	return ChatUser{Provider: store.ProviderTelegram, UserID: int64String(u.ID), Handle: u.Username, Name: u.FirstName}
}

func approvalButtons(l i18n.Lang, kind string, wsID, id int64) [][]Button {
	key := kind + ":" + strconv.FormatInt(wsID, 10) + ":" + strconv.FormatInt(id, 10)
	return [][]Button{{{Text: l.T("notify.button.approve"), Data: key + ":y"}, {Text: l.T("notify.button.reject"), Data: key + ":n"}}}
}

// ---- Telegram 通知通道 ----

func (b *Bot) Provider() string  { return store.ProviderTelegram }
func (b *Bot) Format() Formatter { return TelegramFormat{} }

func tgChat(chat string) (int64, error) {
	id, err := strconv.ParseInt(chat, 10, 64)
	if err != nil {
		return 0, &PermanentError{Msg: fmt.Sprintf("Telegram chat ID must be a number: %q", chat)}
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

// SendDirect 机器人私聊一个人：Telegram 里私聊的 chat_id 就是用户编号。对方屏蔽了机器人、
// 从没和机器人开始过对话时 Telegram 返回 400/403，call 已归为 PermanentError。
func (b *Bot) SendDirect(ctx context.Context, to store.ChatIdentity, m Rendered) error {
	id, err := strconv.ParseInt(to.UserID, 10, 64)
	if err != nil {
		return &PermanentError{Msg: fmt.Sprintf("Telegram user ID must be a number: %q", to.UserID)}
	}
	return b.api().SendText(ctx, id, m.Text)
}

// SendApproval 发一条带“批准 / 拒绝”按钮的消息，返回消息编号。
func (b *Bot) SendApproval(ctx context.Context, chat string, m Rendered, a Approval) (string, error) {
	id, err := tgChat(chat)
	if err != nil {
		return "", err
	}
	msg, err := b.api().SendButtons(ctx, id, m.Text, approvalButtons(m.lang(), a.Kind, a.WorkspaceID, a.RefID))
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
		return &PermanentError{Msg: fmt.Sprintf("Telegram message ID must be a number: %q", msgRef)}
	}
	return b.api().EditMessage(ctx, id, msg, m.Text, nil)
}

// AnnounceSignup 等方法实现 httpapi.Announcer，交给 Hub（只配了 Telegram 时使用）。
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
func (b *Bot) AnnounceSelfJoined(ctx context.Context, m store.Member) error {
	return b.hub().AnnounceSelfJoined(ctx, m)
}
func (b *Bot) AnnounceArchived(ctx context.Context, wsID int64, archived bool, actorName string) error {
	return b.hub().AnnounceArchived(ctx, wsID, archived, actorName)
}

// handleCallback 处理审批按钮：su（注册与加入申请）、da（添加设备），数据形如 su:工作空间:编号:y。
// 审批逻辑见 Hub.Click；这里只解析按钮数据、回应点击、改被点的那条消息。
func (b *Bot) handleCallback(ctx context.Context, cb *TGCallback) {
	answer := func(text string) {
		if err := b.api().AnswerCallback(ctx, cb.ID, text); err != nil {
			b.logf("failed to answer button click: %v", err)
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
			b.logf("failed to update approval message: %v", err)
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
