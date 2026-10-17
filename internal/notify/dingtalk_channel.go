package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// DingTalkCore 是钉钉机器人在 Hub 之外用到的业务操作，由 core.Service 实现。
type DingTalkCore interface {
	SubmitLinkCode(ctx context.Context, provider, code string, who core.ChatSender, private bool) (core.LinkSubmit, error)
	CompleteChatBind(ctx context.Context, provider, chatID, title, code string) (string, error)
}

// DingTalkChannel 是钉钉的通知通道：群消息用互动卡片发（才能 @ 人、带回调按钮），
// 没配卡片模板或 Stream 不在线时降级：通知改发不能 @ 的 Markdown，审批消息不带按钮、请到网页处理。
// 机器人消息与卡片回调经 Stream 长连接收，服务端只需出站访问。
type DingTalkChannel struct {
	API              *DingTalk
	NoticeTemplate   string // 通知卡片模板编号（变量 title、content）
	ApprovalTemplate string // 审批卡片模板编号（变量 content、status、pending，私有变量 tip；按钮回传 decision）
	Core             DingTalkCore
	Hub              *Hub
	Logf             func(format string, args ...any)
	// ClickWait 是卡片回调里等审批结果的时间，超过就先回“处理中”，结果随后改到卡片上；为 0 时 1.8 秒
	ClickWait time.Duration

	stream *dingStream
}

func (d *DingTalkChannel) logf(format string, args ...any) {
	if d.Logf != nil {
		d.Logf(format, args...)
		return
	}
	if d.Hub != nil {
		d.Hub.logf(format, args...)
	}
}

func (d *DingTalkChannel) Provider() string  { return store.ProviderDingTalk }
func (d *DingTalkChannel) Format() Formatter { return DingTalkFormat{} }

func (d *DingTalkChannel) streamOf() *dingStream {
	if d.stream == nil {
		d.stream = &dingStream{api: d.API, ua: "cohive", logf: d.logf, onBot: d.onBot, onCard: d.onCard}
	}
	return d.stream
}

// Run 维持 Stream 长连接，直到 ctx 结束。
func (d *DingTalkChannel) Run(ctx context.Context) { d.streamOf().Run(ctx) }

// Status 返回 Stream 是否在线，以及不在线的原因或配置提示。
func (d *DingTalkChannel) Status() (bool, string) {
	online, why := d.streamOf().status()
	var notes []string
	if why != "" {
		notes = append(notes, why)
	}
	if d.NoticeTemplate == "" {
		notes = append(notes, "未配通知卡片模板（COHIVE_DINGTALK_NOTICE_TEMPLATE），群通知不会 @ 到人")
	}
	if d.ApprovalTemplate == "" {
		notes = append(notes, "未配审批卡片模板（COHIVE_DINGTALK_APPROVAL_TEMPLATE），审批消息不带按钮")
	}
	return online, strings.Join(notes, "；")
}

// dtText 把按行拼好的正文换成钉钉 Markdown 的换行（行末两个空格）。
func dtText(s string) string { return strings.ReplaceAll(s, "\n", "  \n") }

func randomRef() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func atUsers(ids []store.ChatIdentity) map[string]string {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[string]string, len(ids))
	for _, id := range ids {
		out[id.UserID] = id.DisplayName
	}
	return out
}

// Send 往群里发一条通知卡片（没配模板时发不能 @ 的 Markdown）。
func (d *DingTalkChannel) Send(ctx context.Context, chat string, m Rendered) error {
	text := dtText(m.Text)
	if d.NoticeTemplate == "" {
		return d.API.GroupMarkdown(ctx, chat, m.Title, text)
	}
	ref := "cohive-" + m.Key
	if m.Key == "" {
		ref = "cohive-" + randomRef()
	}
	return d.API.DeliverCard(ctx, CardDelivery{ChatID: chat, TemplateID: d.NoticeTemplate, OutTrackID: ref,
		Params: map[string]string{"title": m.Title, "content": text}, AtUserIDs: atUsers(m.Mentions), Alert: m.Title})
}

// SendApproval 发审批卡片，返回卡片编号；没配审批模板或 Stream 不在线（点了按钮也收不到）时
// 发不带按钮的文字、返回空编号。
func (d *DingTalkChannel) SendApproval(ctx context.Context, chat string, m Rendered, a Approval) (string, error) {
	online, _ := d.streamOf().status()
	if d.ApprovalTemplate == "" || !online {
		text := dtText(m.Text + "\n（群里暂时不能直接点按钮审批，请到 Cohive 网页“管理 → 待审”里处理）")
		return "", d.API.GroupMarkdown(ctx, chat, m.Title, text)
	}
	ref := fmt.Sprintf("cohive-%s-%d-%d-%s", a.Kind, a.RefID, a.WorkspaceID, randomRef())
	err := d.API.DeliverCard(ctx, CardDelivery{ChatID: chat, TemplateID: d.ApprovalTemplate, OutTrackID: ref, Callback: true,
		Params: map[string]string{"content": dtText(m.Text), "status": "", "pending": "true"}, Alert: m.Title})
	if err != nil {
		return "", err
	}
	return ref, nil
}

// resultParams 是审批结果卡片的变量：最后一行（“已批准（审批人 X）”）放进 status，其余是 content。
func resultParams(m Rendered) map[string]string {
	content, status := m.Text, ""
	if i := strings.LastIndex(m.Text, "\n"); i >= 0 {
		content, status = m.Text[:i], m.Text[i+1:]
	}
	return map[string]string{"content": dtText(content), "status": status, "pending": "false"}
}

// FinishApproval 把审批卡片改成结果、去掉按钮。
func (d *DingTalkChannel) FinishApproval(ctx context.Context, _ string, msgRef string, m Rendered) error {
	if msgRef == "" {
		return nil
	}
	return d.API.UpdateCard(ctx, msgRef, resultParams(m))
}

// ---- 收件 ----

// cardCallback 是卡片回调；Content 是 JSON 字符串，里面是按钮的回传参数。
type cardCallback struct {
	Content    string `json:"content"`
	CorpID     string `json:"corpId"`
	OutTrackID string `json:"outTrackId"`
	UserID     string `json:"userId"`
	Type       string `json:"type"`
}

type cardData struct {
	CardParamMap map[string]string `json:"cardParamMap"`
}

type cardResponse struct {
	CardData          *cardData       `json:"cardData,omitempty"`
	UserPrivateData   *cardData       `json:"userPrivateData,omitempty"`
	CardUpdateOptions map[string]bool `json:"cardUpdateOptions,omitempty"`
}

func tipOnly(tip string) cardResponse {
	return cardResponse{UserPrivateData: &cardData{CardParamMap: map[string]string{"tip": tip}},
		CardUpdateOptions: map[string]bool{"updatePrivateDataByKey": true}}
}

// clickFromCard 把卡片回调翻译成 Click；不是审批按钮时 ok 为假。身份只取平台填写的 userId、corpId。
func clickFromCard(data []byte) (Click, bool) {
	var cb cardCallback
	if json.Unmarshal(data, &cb) != nil || cb.OutTrackID == "" {
		return Click{}, false
	}
	var priv struct {
		CardPrivateData struct {
			Params map[string]any `json:"params"`
		} `json:"cardPrivateData"`
	}
	_ = json.Unmarshal([]byte(cb.Content), &priv)
	decision, _ := priv.CardPrivateData.Params["decision"].(string)
	if decision != "approve" && decision != "reject" {
		return Click{}, false
	}
	return Click{Provider: store.ProviderDingTalk, MsgRef: cb.OutTrackID, Approve: decision == "approve",
		From: ChatUser{Provider: store.ProviderDingTalk, Tenant: cb.CorpID, UserID: cb.UserID}}, true
}

// onCard 处理卡片按钮：最多等 ClickWait，拿到结果就作为回调响应改卡；超时先回“处理中”，结果随后改到卡片上。
// 其他群里的同一条审批消息、加入通知在回应之后异步处理（钉钉要求回调过程中不要调更新卡片接口）。
func (d *DingTalkChannel) onCard(_ context.Context, data []byte) any {
	c, ok := clickFromCard(data)
	if !ok || d.Hub == nil {
		return cardResponse{}
	}
	wait := d.ClickWait
	if wait <= 0 {
		wait = 1800 * time.Millisecond
	}
	done := make(chan ClickResult, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		done <- d.Hub.Click(ctx, c)
	}()
	finish := func(res ClickResult, late bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if late && res.Result != nil {
			if err := d.API.UpdateCard(ctx, c.MsgRef, resultParams(*res.Result)); err != nil {
				d.logf("更新审批卡片失败：%v", err)
			}
		}
		if res.After != nil {
			res.After(ctx)
		}
	}
	select {
	case res := <-done:
		go finish(res, false)
		if res.Result == nil {
			return tipOnly(res.Toast)
		}
		return cardResponse{CardData: &cardData{CardParamMap: resultParams(*res.Result)},
			UserPrivateData:   &cardData{CardParamMap: map[string]string{"tip": res.Toast}},
			CardUpdateOptions: map[string]bool{"updateCardDataByKey": true, "updatePrivateDataByKey": true}}
	case <-time.After(wait):
		go func() { finish(<-done, true) }()
		return tipOnly("处理中，稍后结果会更新到卡片上")
	}
}

// botMessage 是机器人收到的消息（群里 @ 机器人、单聊）。字段由钉钉填写。
type botMessage struct {
	ConversationID    string `json:"conversationId"`
	ConversationType  string `json:"conversationType"` // 1 单聊、2 群聊
	ConversationTitle string `json:"conversationTitle"`
	SenderStaffID     string `json:"senderStaffId"`
	SenderCorpID      string `json:"senderCorpId"`
	SenderNick        string `json:"senderNick"`
	SessionWebhook    string `json:"sessionWebhook"`
	Msgtype           string `json:"msgtype"`
	Text              struct {
		Content string `json:"content"`
	} `json:"text"`
}

// command 去掉开头的 @机器人名 与空白，返回命令词（小写、去掉开头的 /）、原样的第一个词与参数。
func command(text string) (cmd, first string, args []string) {
	fields := strings.Fields(strings.ReplaceAll(text, "\u3000", " "))
	for len(fields) > 0 && strings.HasPrefix(fields[0], "@") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return "", "", nil
	}
	return strings.ToLower(strings.TrimPrefix(fields[0], "/")), fields[0], fields[1:]
}

const dtGroupHelp = "我是 Cohive 机器人。在群里 @我 可以发：  \n" +
	"- `绑定群 绑定码`：把本群绑定到工作空间（绑定码在网页“管理 → 工作空间”里生成）  \n" +
	"- `群编号`：查看本群的编号  \n" +
	"- `我是谁`：查看你连接的 Cohive 账号  \n" +
	"连接钉钉账号的码请**私聊**发给我。"

const dtPrivateHelp = "我是 Cohive 机器人。要把你的钉钉账号连接到 Cohive，请在网页“个人中心”点“连接钉钉”，把网页上的绑定码发给我。"

// onBot 处理机器人消息：确认已在 Stream 层做过，这里开协程处理并经会话 Webhook 回复。
func (d *DingTalkChannel) onBot(_ context.Context, data []byte) {
	var m botMessage
	if json.Unmarshal(data, &m) != nil || m.ConversationID == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		reply := d.handleBot(ctx, m)
		if reply == "" || m.SessionWebhook == "" {
			return
		}
		if err := d.API.Reply(ctx, m.SessionWebhook, "Cohive", reply); err != nil {
			d.logf("回复钉钉消息失败：%v", err)
		}
	}()
}

func (d *DingTalkChannel) who(m botMessage) core.ChatSender {
	return core.ChatSender{Tenant: m.SenderCorpID, UserID: m.SenderStaffID, Name: m.SenderNick}
}

func (d *DingTalkChannel) whoami(ctx context.Context, m botMessage, chat string) string {
	esc := DingTalkFormat{}.Esc
	if d.Hub == nil {
		return ""
	}
	acc, wsLabel, role, ok := d.Hub.Whoami(ctx, ChatUser{Provider: store.ProviderDingTalk, Tenant: m.SenderCorpID, UserID: m.SenderStaffID}, chat)
	if !ok {
		return "你的钉钉账号还没有连接 Cohive。请在网页“个人中心”点“连接钉钉”，把绑定码私聊发给我。"
	}
	text := "你的钉钉账号已连接 Cohive 账号 " + esc(acc) + "。"
	if wsLabel != "" {
		text += "  \n在本群对应的工作空间 " + esc(wsLabel) + " 里：" + role
	}
	return text
}

// handleBot 返回要回复的内容（已是钉钉 Markdown）；空串表示不回复。
func (d *DingTalkChannel) handleBot(ctx context.Context, m botMessage) string {
	esc := DingTalkFormat{}.Esc
	text := m.Text.Content
	if m.Msgtype != "" && m.Msgtype != "text" {
		text = ""
	}
	cmd, first, args := command(text)
	private := m.ConversationType == "1"
	code := ""
	switch {
	case (cmd == "绑定" || cmd == "bind") && len(args) == 1:
		code = args[0]
	case len(args) == 0 && core.LooksLikeCode(first):
		code = first
	}

	if private {
		switch {
		case code != "" && d.Core != nil:
			res, err := d.Core.SubmitLinkCode(ctx, store.ProviderDingTalk, code, d.who(m), true)
			if err != nil {
				return d.userErr(err, "连接钉钉")
			}
			return "已收到。请回到 Cohive 网页确认：把这个钉钉账号连接到 Cohive 账号 " + esc(res.Account) + "。"
		case cmd == "我是谁" || cmd == "whoami":
			return d.whoami(ctx, m, "")
		}
		return dtPrivateHelp
	}

	switch {
	case cmd == "群编号" || cmd == "chatid":
		return fmt.Sprintf("这个群（%s）的编号是 %s  \n工作空间管理员也可以在网页“管理 → 工作空间”生成绑定码，在群里 @我 发 `绑定群 绑定码` 一步绑定。",
			esc(m.ConversationTitle), DingTalkFormat{}.Code(m.ConversationID))
	case cmd == "我是谁" || cmd == "whoami":
		return d.whoami(ctx, m, m.ConversationID)
	case (cmd == "绑定群" || cmd == "bindgroup") && len(args) == 1 && d.Core != nil:
		label, err := d.Core.CompleteChatBind(ctx, store.ProviderDingTalk, m.ConversationID, m.ConversationTitle, args[0])
		if err != nil {
			return d.userErr(err, "绑定群")
		}
		return "已把本群绑定到工作空间 " + esc(label) + "。之后的问题单通知与审批消息会发到这里。"
	case code != "" && d.Core != nil:
		// 账号绑定码出现在群里：作废（回复里不复述码）；不是账号绑定码时，可能是群绑定码少写了“群”
		_, err := d.Core.SubmitLinkCode(ctx, store.ProviderDingTalk, code, d.who(m), false)
		if errors.Is(err, core.ErrLinkCodeLeaked) {
			return core.ErrLinkCodeLeaked.Error()
		}
		label, err := d.Core.CompleteChatBind(ctx, store.ProviderDingTalk, m.ConversationID, m.ConversationTitle, code)
		if err == nil {
			return "已把本群绑定到工作空间 " + esc(label) + "。之后的问题单通知与审批消息会发到这里。"
		}
	}
	return dtGroupHelp
}

// userErr 把业务层的错误写成给人看的回复；内部错误只记日志。
func (d *DingTalkChannel) userErr(err error, what string) string {
	var ie *core.InputError
	var se *core.StateError
	switch {
	case errors.Is(err, core.ErrLinkCodeGone), errors.Is(err, core.ErrLinkCodeLeaked), errors.Is(err, core.ErrBindCodeGone),
		errors.Is(err, store.ErrAccountDisabled), errors.As(err, &ie), errors.As(err, &se):
		return DingTalkFormat{}.Esc(err.Error())
	}
	d.logf("钉钉机器人处理“%s”失败：%v", what, err)
	return "暂时无法完成，请稍后再试。"
}
