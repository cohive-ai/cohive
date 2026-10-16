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
			return fmt.Sprintf("你的 Telegram 用户编号是 <code>%d</code>。你还没有设置 Telegram 用户名，请先在 Telegram 的设置里设置，否则群通知没法 @ 到你。\n\n新成员直接在团队群里发 /join 申请加入即可。已有成员要绑定账号，请管理员执行（把工作空间名和成员名换成你的）：\n<code>cohive-server member set --workspace 工作空间名 --name 成员名 --telegram-id %d</code>",
				m.From.ID, m.From.ID), true
		}
		name := html.EscapeString(m.From.Username)
		return fmt.Sprintf("你的 Telegram 用户名是 <code>%s</code>，用户编号是 <code>%d</code>\n\n新成员直接在团队群里发 /join 申请加入即可。已有成员要绑定账号，请管理员执行（把工作空间名和成员名换成你的）：\n<code>cohive-server member set --workspace 工作空间名 --name 成员名 --telegram %s --telegram-id %d</code>",
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

// chatAPI 是机器人用到的 Telegram 操作，由 *Telegram 实现，测试时可替换。
type chatAPI interface {
	Reply(ctx context.Context, chatID, replyTo int64, text string) error
	SendText(ctx context.Context, chatID int64, text string) error
	SendButtons(ctx context.Context, chatID int64, text string, buttons [][]Button) (int64, error)
	EditMessage(ctx context.Context, chatID, messageID int64, text string, buttons [][]Button) error
	AnswerCallback(ctx context.Context, callbackID, text string) error
}

// Onboarding 是机器人用到的业务操作，由 core.Service 实现。
type Onboarding interface {
	LoginIssuer
	RequestJoin(ctx context.Context, chatID string, tgUserID int64, username, display string) (core.JoinResult, error)
	DecideJoin(ctx context.Context, requestID, deciderTgUserID int64, approve bool) (core.JoinDecision, error)
	OpenDeviceLogin(ctx context.Context, startParam string, tgUserID int64) (core.DeviceView, error)
	ChooseDeviceWorkspace(ctx context.Context, id, tgUserID, workspaceID int64) (core.DeviceView, error)
	ConfirmDeviceLogin(ctx context.Context, id, tgUserID int64) (core.DeviceView, error)
	CancelDeviceLogin(ctx context.Context, id, tgUserID int64) error
}

// Bot 用长轮询接收机器人消息，处理 /chatid、/whoami、/login、/join 与按钮点击。
type Bot struct {
	TG   *Telegram
	Core Onboarding // 为空时只处理 /chatid、/whoami
	API  chatAPI    // 为空时使用 TG
	Name string     // 机器人用户名；为空时启动时向 Telegram 查询
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

// fetchName 在没有配置用户名时向 Telegram 查询。
func (b *Bot) fetchName(ctx context.Context) {
	if b.Username() != "" {
		return
	}
	name, err := b.TG.GetMe(ctx)
	if err != nil || name == "" {
		b.logf("Telegram 机器人信息获取失败，稍后重试（带 @机器人名 的命令与机器人确认登录暂不可用）：%v", err)
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

// Run 一直运行到 ctx 结束。出错时等待一会儿再继续，不会退出。
func (b *Bot) Run(ctx context.Context) {
	if b.API == nil {
		b.API = b.TG
	}
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
		reply, ok = HandleLogin(ctx, u, b.Username(), b.Core)
	}
	if !ok && b.Core != nil {
		reply, ok = b.handleJoin(ctx, u)
	}
	if !ok && b.Core != nil {
		reply, ok = b.handleStart(ctx, u)
	}
	if !ok || reply == "" {
		return
	}
	if err := b.API.Reply(ctx, u.Message.Chat.ID, u.Message.MessageID, reply); err != nil {
		b.logf("回复 Telegram 命令失败：%v", err)
	}
}

// command 取出消息里发给本机器人的命令名（例如 /join）与参数；不是命令或发给别的机器人时返回空。
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

// mentionUser 生成提及某人的文字：有用户名时 @ 用户名，否则用显示名。
func mentionUser(username, display string) string {
	if username != "" {
		return "@" + html.EscapeString(username)
	}
	if display != "" {
		return html.EscapeString(display)
	}
	return "新成员"
}

func (b *Bot) handleJoin(ctx context.Context, u Update) (string, bool) {
	m := u.Message
	if cmd, _ := b.command(m); cmd != "/join" || m.From == nil {
		return "", false
	}
	if m.Chat.Type == "private" {
		return "请在你们团队的工作空间群里发 /join 申请加入。", true
	}
	res, err := b.Core.RequestJoin(ctx, strconv.FormatInt(m.Chat.ID, 10), m.From.ID, m.From.Username, m.From.FirstName)
	switch {
	case errors.Is(err, core.ErrNoWorkspaceForChat):
		return "这个群还没有对应的 Cohive 工作空间。请管理员在群里发 /chatid 拿到群编号，配置好群通知后再试。", true
	case err != nil:
		b.logf("处理 /join 失败：%v", err)
		return "暂时无法处理加入申请，请稍后再试。", true
	case res.AlreadyMember != nil:
		return fmt.Sprintf("你已经是工作空间 %s 的成员（成员名 %s）。在仓库里执行 cohive login 即可登录。",
			html.EscapeString(res.Workspace.Name), html.EscapeString(res.AlreadyMember.Name)), true
	case res.Member != nil:
		return fmt.Sprintf("%s 已加入工作空间 %s，成员名 %s。\n下一步：在仓库里执行 cohive login，按提示在私聊里确认即可。",
			mentionUser(m.From.Username, m.From.FirstName), html.EscapeString(res.Workspace.Name), html.EscapeString(res.Member.Name)), true
	}
	j := res.Request
	id := strconv.FormatInt(j.ID, 10)
	text := fmt.Sprintf("%s 申请加入工作空间 %s。\n请管理员审批：",
		mentionUser(j.TelegramUsername, j.DisplayName), html.EscapeString(res.Workspace.Name))
	if res.NoAdmin {
		text += fmt.Sprintf("\n\n注意：这个工作空间还没有管理员，按钮暂时没人能点。请在协作服务所在的机器上执行 <code>cohive-server member set --workspace %s --name 成员名 --role admin</code> 设置管理员，或直接 <code>cohive-server join approve --id %s</code>。",
			html.EscapeString(res.Workspace.Name), id)
	}
	if _, err := b.API.SendButtons(ctx, m.Chat.ID, text, [][]Button{{
		{Text: "批准", Data: "join:" + id + ":y"}, {Text: "拒绝", Data: "join:" + id + ":n"},
	}}); err != nil {
		b.logf("发送审批消息失败：%v", err)
		return "加入申请已记录，但审批消息发送失败，请联系管理员。", true
	}
	return "", true
}

func (b *Bot) handleCallback(ctx context.Context, cb *TGCallback) {
	answer := func(text string) {
		if err := b.API.AnswerCallback(ctx, cb.ID, text); err != nil {
			b.logf("回应按钮点击失败：%v", err)
		}
	}
	parts := strings.Split(cb.Data, ":")
	if b.Core != nil && len(parts) >= 2 && strings.HasPrefix(parts[0], "dl") {
		b.handleDeviceCallback(ctx, cb, parts, answer)
		return
	}
	if b.Core == nil || len(parts) != 3 || parts[0] != "join" {
		answer("")
		return
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || (parts[2] != "y" && parts[2] != "n") {
		answer("")
		return
	}
	d, err := b.Core.DecideJoin(ctx, id, cb.From.ID, parts[2] == "y")
	switch {
	case errors.Is(err, core.ErrNotAdmin), errors.Is(err, core.ErrJoinDecided):
		answer(err.Error())
		return
	case err != nil:
		b.logf("处理审批失败：%v", err)
		answer("处理失败，请稍后再试")
		return
	}
	who := mentionUser(d.Request.TelegramUsername, d.Request.DisplayName)
	verdict := "已拒绝"
	if d.Member != nil {
		verdict = "已批准，成员名 " + html.EscapeString(d.Member.Name)
	}
	answer(strings.SplitN(verdict, "，", 2)[0])
	if cb.Message != nil {
		text := fmt.Sprintf("%s 申请加入工作空间 %s：%s（审批人 %s）", who, html.EscapeString(d.Workspace.Name),
			verdict, html.EscapeString(d.Decider.Name))
		if err := b.API.EditMessage(ctx, cb.Message.Chat.ID, cb.Message.MessageID, text, nil); err != nil {
			b.logf("更新审批消息失败：%v", err)
		}
		if d.Member != nil {
			welcome := fmt.Sprintf("%s 欢迎加入工作空间 %s！下一步：在仓库里执行 cohive login，按提示在私聊里确认即可。",
				who, html.EscapeString(d.Workspace.Name))
			if err := b.API.SendText(ctx, cb.Message.Chat.ID, welcome); err != nil {
				b.logf("发送欢迎消息失败：%v", err)
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
