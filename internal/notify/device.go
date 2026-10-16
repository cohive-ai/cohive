package notify

import (
	"context"
	"errors"
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/cohive-ai/cohive/internal/core"
)

const startWelcome = "我是 Cohive 机器人。\n· 加入团队：在团队的工作空间群里发 /join\n· 登录：在仓库里执行 <code>cohive login</code>，点终端给出的链接回到这里确认"

// handleStart 处理私聊里的 /start；带 dl_ 参数时是终端发起的设备授权登录。
func (b *Bot) handleStart(ctx context.Context, u Update) (string, bool) {
	m := u.Message
	cmd, args := b.command(m)
	if cmd != "/start" || m.From == nil || m.Chat.Type != "private" {
		return "", false
	}
	if len(args) == 0 || !strings.HasPrefix(args[0], core.DeviceStartPrefix) {
		return startWelcome, true
	}
	v, err := b.Core.OpenDeviceLogin(ctx, args[0], m.From.ID)
	if err != nil {
		return b.deviceErrText(err), true
	}
	text, buttons := deviceMessage(v)
	if _, err := b.API.SendButtons(ctx, m.Chat.ID, text, buttons); err != nil {
		b.logf("发送登录确认消息失败：%v", err)
		return "暂时无法发送登录确认，请稍后在终端重新执行 cohive login。", true
	}
	return "", true
}

func (b *Bot) deviceErrText(err error) string {
	var ie *core.InputError
	switch {
	case errors.Is(err, core.ErrNotBound):
		return "你的 Telegram 账号还没有加入任何 Cohive 工作空间。请先在团队群里发 /join 申请加入，批准后回到终端重新执行 cohive login。"
	case errors.Is(err, core.ErrDeviceGone), errors.Is(err, core.ErrDeviceTaken), errors.Is(err, core.ErrDeviceWrongNumber):
		return err.Error()
	case errors.As(err, &ie):
		return html.EscapeString(err.Error())
	}
	b.logf("处理登录请求失败：%v", err)
	return "暂时无法处理登录请求，请稍后再试。"
}

// deviceMessage 生成登录确认消息：先选工作空间，或在三个数字里选终端显示的那个。
func deviceMessage(v core.DeviceView) (string, [][]Button) {
	id := strconv.FormatInt(v.ID, 10)
	var t strings.Builder
	fmt.Fprintf(&t, "<b>确认登录 Cohive</b>\n设备：%s\n工具：%s", html.EscapeString(v.DeviceName), html.EscapeString(strings.Join(v.Tools, "、")))
	cancel := []Button{{Text: "取消", Data: "dlx:" + id}}
	if len(v.Numbers) == 0 {
		t.WriteString("\n\n你加入了多个工作空间，请先选择本次登录的工作空间：")
		var rows [][]Button
		for _, c := range v.Choices {
			rows = append(rows, []Button{{Text: c.WorkspaceName + "（" + c.Name + "）",
				Data: "dlw:" + id + ":" + strconv.FormatInt(c.WorkspaceID, 10)}})
		}
		return t.String(), append(rows, cancel)
	}
	fmt.Fprintf(&t, "\n工作空间：%s（成员名 %s）\n\n请点选<b>终端上显示的数字</b>，选错会作废本次登录。\n只确认你自己刚刚在终端发起的登录；如果链接是别人发给你的，请点“取消”。",
		html.EscapeString(v.Workspace), html.EscapeString(v.MemberName))
	row := make([]Button, len(v.Numbers))
	for i, n := range v.Numbers {
		row[i] = Button{Text: strconv.Itoa(n), Data: "dl:" + id + ":" + strconv.Itoa(n)}
	}
	return t.String(), [][]Button{row, cancel}
}

// handleDeviceCallback 处理登录确认消息上的按钮：dlw 选工作空间、dl 选数字、dlx 取消。
func (b *Bot) handleDeviceCallback(ctx context.Context, cb *TGCallback, parts []string, answer func(string)) {
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		answer("")
		return
	}
	edit := func(text string, buttons [][]Button) {
		if cb.Message == nil {
			return
		}
		if err := b.API.EditMessage(ctx, cb.Message.Chat.ID, cb.Message.MessageID, text, buttons); err != nil {
			b.logf("更新登录确认消息失败：%v", err)
		}
	}
	fail := func(err error) {
		text := b.deviceErrText(err)
		answer(strings.SplitN(text, "，", 2)[0])
		edit(text, nil)
	}
	uid := cb.From.ID
	switch {
	case parts[0] == "dlx" && len(parts) == 2:
		if err := b.Core.CancelDeviceLogin(ctx, id, uid); err != nil {
			fail(err)
			return
		}
		answer("已取消")
		edit("已取消本次登录，终端会显示登录被拒绝。", nil)
	case parts[0] == "dlw" && len(parts) == 3:
		wsID, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			answer("")
			return
		}
		v, err := b.Core.ChooseDeviceWorkspace(ctx, id, uid, wsID)
		if err != nil {
			fail(err)
			return
		}
		answer("")
		edit(deviceMessage(v))
	case parts[0] == "dl" && len(parts) == 3:
		n, err := strconv.Atoi(parts[2])
		if err != nil {
			answer("")
			return
		}
		v, err := b.Core.ConfirmDeviceLogin(ctx, id, uid, n)
		if err != nil {
			fail(err)
			return
		}
		answer("已确认")
		edit(fmt.Sprintf("已确认登录：设备 %s，工作空间 %s（成员名 %s）。\n回到终端即可，令牌会自动保存。",
			html.EscapeString(v.DeviceName), html.EscapeString(v.Workspace), html.EscapeString(v.MemberName)), nil)
	default:
		answer("")
	}
}
