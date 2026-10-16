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
	DecideSignup(ctx context.Context, actor *store.Account, wsID, id int64, approve bool) (core.SignupDecision, error)
	DecideDeviceAuth(ctx context.Context, actor *store.Account, wsID, id int64, approve bool) (core.DeviceDecision, error)
	Workspace(ctx context.Context, id int64) (store.Workspace, error)
	WorkspaceByGroup(ctx context.Context, chatID int64) (store.Workspace, error)
	AccountByTelegram(ctx context.Context, tgUserID int64) (store.Account, error)
	Memberships(ctx context.Context, accountID int64) ([]store.Membership, error)
	RecordBotPost(ctx context.Context, p store.BotPost) error
	BotPosts(ctx context.Context, kind string, refID int64) ([]store.BotPost, error)
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
	if m.From.Username == "" {
		text += "\n你还没有设置 Telegram 用户名，群通知没法 @ 到你，建议在 Telegram 设置里设一个，再重新连接。"
	}
	return text, true
}

// whoamiExtra 在 /whoami 的回复后补上连接的 Cohive 账号；在群里再补上他在本群工作空间的角色。
func (b *Bot) whoamiExtra(ctx context.Context, u Update) string {
	cmd, _ := b.command(u.Message)
	if cmd != "/whoami" || u.Message.From == nil {
		return ""
	}
	acc, err := b.Core.AccountByTelegram(ctx, u.Message.From.ID)
	if err != nil {
		return ""
	}
	text := fmt.Sprintf("\n\n已连接的 Cohive 账号：%s", html.EscapeString(acc.Name))
	if u.Message.Chat.Type == "private" {
		return text
	}
	ws, err := b.Core.WorkspaceByGroup(ctx, u.Message.Chat.ID)
	if err != nil {
		return text
	}
	role := "不是成员"
	if ms, err := b.Core.Memberships(ctx, acc.ID); err == nil {
		for _, m := range ms {
			if m.WorkspaceID == ws.ID {
				role = roleLabel(m.Role)
			}
		}
	}
	return text + fmt.Sprintf("\n在本群对应的工作空间 %s 里：%s", html.EscapeString(ws.Name), role)
}

func roleLabel(role string) string {
	if role == store.RoleAdmin {
		return "管理员"
	}
	return "成员"
}

// mentionName 是审批消息里展示的登录名与显示名。
func mentionName(name, display string) string {
	s := html.EscapeString(name)
	if display != "" && display != name {
		s += "（" + html.EscapeString(display) + "）"
	}
	return s
}

// mention 是群里 @ 某人：连接了 Telegram 且有用户名时 @ 他，否则只写名字。
func mention(name, display, tgUsername string) string {
	if tgUsername != "" {
		return "@" + html.EscapeString(tgUsername) + "（" + mentionName(name, display) + "）"
	}
	return mentionName(name, display)
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

// post 发一条审批消息并记下，处理后据此把各群里的消息都改成结果。
func (b *Bot) post(ctx context.Context, kind string, wsID, chat, refID int64, text string) error {
	msgID, err := b.api().SendButtons(ctx, chat, text, approvalButtons(kind, wsID, refID))
	if err != nil {
		return err
	}
	return b.Core.RecordBotPost(ctx, store.BotPost{Kind: kind, RefID: refID, WorkspaceID: wsID, ChatID: chat, MessageID: msgID})
}

func signupText(s store.Signup, wsName string) string {
	if s.Existing {
		return fmt.Sprintf("%s（已有账号）申请加入工作空间 %s。", mentionName(s.Name, s.DisplayName), html.EscapeString(wsName))
	}
	return fmt.Sprintf("新成员 %s 申请加入工作空间 %s。", mentionName(s.Name, s.DisplayName), html.EscapeString(wsName))
}

func deviceText(a store.DeviceAuth, workspaces []string) string {
	return fmt.Sprintf("%s 申请添加新设备 %s（工具：%s）。\n批准后这台电脑可用于他所在的全部工作空间：%s。",
		mentionName(a.AccountName, a.DisplayName), html.EscapeString(a.DeviceName), html.EscapeString(strings.Join(a.Tools, "、")),
		html.EscapeString(strings.Join(workspaces, "、")))
}

// AnnounceSignup 在申请所属工作空间的群里发注册或加入申请的审批消息（没配置群时不发）。
func (b *Bot) AnnounceSignup(ctx context.Context, s store.Signup) error {
	ws, chat, err := b.groupOf(ctx, s.WorkspaceID)
	if err != nil || chat == 0 {
		return err
	}
	text := signupText(s, ws.Name) + "\n请管理员审批（也可以在网页“管理 → 待审”里处理）："
	return b.post(ctx, "su", ws.ID, chat, s.ID, text)
}

// AnnounceDevice 在申请人在册所在、配置了群的每个工作空间群里发设备审批消息。
func (b *Bot) AnnounceDevice(ctx context.Context, a store.DeviceAuth) error {
	if a.AccountID == nil {
		return nil
	}
	ms, err := b.Core.Memberships(ctx, *a.AccountID)
	if err != nil {
		return err
	}
	var names []string
	for _, m := range ms {
		names = append(names, m.Workspace)
	}
	text := deviceText(a, names) + "\n请任一工作空间的管理员审批（也可以在网页“管理 → 待审”里处理）："
	var firstErr error
	for _, m := range ms {
		_, chat, err := b.groupOf(ctx, m.WorkspaceID)
		if err == nil && chat != 0 {
			err = b.post(ctx, "da", m.WorkspaceID, chat, a.ID, text)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// finish 把某条申请在各群里的审批消息都改成结果、去掉按钮。
func (b *Bot) finish(ctx context.Context, kind string, refID int64, text string) error {
	posts, err := b.Core.BotPosts(ctx, kind, refID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, p := range posts {
		if err := b.api().EditMessage(ctx, p.ChatID, p.MessageID, text, nil); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func signupResult(d core.SignupDecision, approve bool) string {
	return signupText(d.Signup, d.Workspace.Name) + fmt.Sprintf("\n%s（审批人 %s）", verdict(approve), html.EscapeString(d.Decider))
}

func deviceResult(d core.DeviceDecision) string {
	return deviceText(d.Auth, d.Workspaces) + fmt.Sprintf("\n%s（审批人 %s）", verdict(d.Auth.Status == store.AuthApproved),
		html.EscapeString(d.Decider))
}

// AnnounceSignupDecided 在网页上处理申请后，把群里的审批消息改成结果。
func (b *Bot) AnnounceSignupDecided(ctx context.Context, d core.SignupDecision) error {
	return b.finish(ctx, "su", d.Signup.ID, signupResult(d, d.Signup.Status == store.SignupApproved))
}

// AnnounceDeviceDecided 在网页上处理设备申请后，把各群里的审批消息改成结果。
func (b *Bot) AnnounceDeviceDecided(ctx context.Context, d core.DeviceDecision) error {
	return b.finish(ctx, "da", d.Auth.ID, deviceResult(d))
}

// AnnounceJoined 在工作空间群里说一声“X 已加入”，并 @ 他（已连接 Telegram 时）。
func (b *Bot) AnnounceJoined(ctx context.Context, m store.Member) error {
	ws, chat, err := b.groupOf(ctx, m.WorkspaceID)
	if err != nil || chat == 0 {
		return err
	}
	return b.api().SendText(ctx, chat, fmt.Sprintf("%s 已加入工作空间 %s。", mention(m.Name, m.DisplayName, m.TelegramUsername),
		html.EscapeString(ws.Name)))
}

// handleCallback 处理审批按钮：su（注册与加入申请）、da（添加设备），数据形如 su:工作空间:编号:y。
// 工作空间由按钮所在的群反查（一个群只绑一个工作空间），不信按钮数据；点击者按他连接的 Telegram 账号
// 找到 Cohive 账号，业务层校验他是否能管理这个工作空间（设备申请还要求申请人在本工作空间在册）。
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
	ws, err := b.Core.WorkspaceByGroup(ctx, cb.Message.Chat.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && ws.ID != wsID) {
		answer("这条审批消息与本群绑定的工作空间不符，请在网页上处理")
		return
	}
	if err != nil {
		b.logf("识别审批群失败：%v", err)
		answer("处理失败，请稍后再试")
		return
	}
	actor, err := b.Core.AccountByTelegram(ctx, cb.From.ID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && actor.Disabled()) {
		answer("你的 Telegram 账号还没有连接到 Cohive，或没有审批权限")
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
		d, err := b.Core.DecideSignup(ctx, &actor, ws.ID, id, approve)
		if err != nil {
			b.failCallback(err, answer)
			return
		}
		text = signupResult(d, approve)
		if d.Member != nil {
			defer func() {
				if err := b.AnnounceJoined(ctx, *d.Member); err != nil {
					b.logf("发送新成员加入消息失败：%v", err)
				}
			}()
		}
	case "da":
		d, err := b.Core.DecideDeviceAuth(ctx, &actor, ws.ID, id, approve)
		if err != nil {
			b.failCallback(err, answer)
			return
		}
		text = deviceResult(d)
	}
	answer(verdict(approve))
	if err := b.finish(ctx, parts[0], id, text); err != nil {
		b.logf("更新审批消息失败：%v", err)
	}
	posts, _ := b.Core.BotPosts(ctx, parts[0], id)
	if len(posts) == 0 { // 没有登记过（例如升级前发的消息）：至少改掉被点的这条
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
	case errors.Is(err, core.ErrNotAdmin), errors.Is(err, core.ErrNotManager), errors.Is(err, core.ErrDecided),
		errors.Is(err, store.ErrAccountDisabled):
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
