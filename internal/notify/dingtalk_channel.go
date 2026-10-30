package notify

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// DingTalkChannel 是钉钉的通知通道：群消息用互动卡片发（才能 @ 人、带回调按钮），
// 没配卡片模板或 Stream 不在线时降级：通知改发不能 @ 的 Markdown，审批消息不带按钮、请到网页处理。
// 机器人消息与卡片回调经 Stream 长连接收，服务端只需出站访问。
type DingTalkChannel struct {
	API              *DingTalk
	NoticeTemplate   string   // 通知卡片模板编号（变量 title、content）
	ApprovalTemplate string   // 审批卡片模板编号（变量 content、status、pending，私有变量 tip；按钮回传 decision）
	Core             CodeCore // 绑定码操作；为空时不处理绑定码
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

// Status 返回 Stream 是否在线，以及不在线的原因或配置提示（给管理员看，用服务器缺省语言）。
func (d *DingTalkChannel) Status() (bool, string) {
	online, why := d.streamOf().status()
	var notes []string
	if why != "" {
		notes = append(notes, why)
	}
	if d.NoticeTemplate == "" {
		notes = append(notes, i18n.T("notify.status.no_notice_template"))
	}
	if d.ApprovalTemplate == "" {
		notes = append(notes, i18n.T("notify.status.no_approval_template"))
	}
	return online, strings.Join(notes, i18n.T("notify.status.sep"))
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

// SendDirect 机器人单聊发一条 Markdown 消息（不 @ 人，换行与群消息一致）。
func (d *DingTalkChannel) SendDirect(ctx context.Context, to store.ChatIdentity, m Rendered) error {
	title := m.Title
	if title == "" {
		title = "Cohive"
	}
	return d.API.DirectMarkdown(ctx, to.UserID, title, dtText(m.Text))
}

// SendApproval 发审批卡片，返回卡片编号；没配审批模板或 Stream 不在线（点了按钮也收不到）时
// 发不带按钮的文字、返回空编号。
func (d *DingTalkChannel) SendApproval(ctx context.Context, chat string, m Rendered, a Approval) (string, error) {
	online, _ := d.streamOf().status()
	if d.ApprovalTemplate == "" || !online {
		text := dtText(m.Text + "\n" + m.lang().T("notify.dingtalk.no_buttons"))
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
				d.logf("failed to update approval card: %v", err)
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
		// 结果还没出来，按审批消息所属工作空间的语言提示
		lctx, lcancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer lcancel()
		return tipOnly(d.Hub.ClickLang(lctx, c).T("notify.toast.processing"))
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

// dtCaps：钉钉没有带参数的机器人链接，账号连接与群绑定都用码；认不出的消息回复用法。
var dtCaps = InboxCaps{LinkByCode: true, BindByCode: true, Help: true}

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
			d.logf("failed to reply to DingTalk message: %v", err)
		}
	}()
}

// inbound 把钉钉的机器人消息翻译成统一的收件消息；非文字消息当作空消息（回复用法）。
func (m botMessage) inbound() Inbound {
	text := m.Text.Content
	if m.Msgtype != "" && m.Msgtype != "text" {
		text = ""
	}
	in := Inbound{Provider: store.ProviderDingTalk, ChatID: m.ConversationID, ChatTitle: m.ConversationTitle,
		Private: m.ConversationType == "1",
		From:    ChatUser{Provider: store.ProviderDingTalk, Tenant: m.SenderCorpID, UserID: m.SenderStaffID, Name: m.SenderNick}}
	in.Cmd, in.First, in.Args = parseText(text)
	return in
}

// handleBot 返回要回复的内容（已是钉钉 Markdown）；空串表示不回复。
func (d *DingTalkChannel) handleBot(ctx context.Context, m botMessage) string {
	in := m.inbound()
	r := Inbox{Hub: d.Hub, Codes: d.Core, Caps: dtCaps, Logf: d.logf}.Handle(ctx, in)
	return dtReplyText(r, in)
}

// dtReplyText 按钉钉的文案与 Markdown 写出回复。
func dtReplyText(r Reply, in Inbound) string {
	esc, l := DingTalkFormat{}.Esc, r.Lang
	switch r.Kind {
	case ReplyHelp:
		if r.Private {
			return l.T("notify.dingtalk.private_help")
		}
		return l.T("notify.dingtalk.group_help")
	case ReplyChatID:
		if r.Private { // 私聊里没有群编号可说，回用法
			return l.T("notify.dingtalk.private_help")
		}
		return l.T("notify.dingtalk.chatid", i18n.D{"Title": esc(in.ChatTitle), "ID": DingTalkFormat{}.Code(in.ChatID)})
	case ReplyWhoami:
		switch {
		case !r.Checked:
			return ""
		case !r.Linked:
			return l.T("notify.dingtalk.whoami_unlinked")
		}
		text := l.T("notify.dingtalk.whoami_linked", i18n.D{"Account": esc(r.Account)})
		if r.Workspace != "" {
			text += "  \n" + l.T("notify.dingtalk.whoami_role", i18n.D{"Workspace": esc(r.Workspace), "Role": r.Role})
		}
		return text
	case ReplyLinkReceived:
		return l.T("notify.dingtalk.link_received", i18n.D{"Account": esc(r.Account)})
	case ReplyBound:
		return l.T("notify.dingtalk.bound", i18n.D{"Workspace": esc(r.Workspace)})
	case ReplyError:
		return esc(r.Err)
	case ReplyTryLater:
		return l.T("notify.dingtalk.try_later")
	}
	return ""
}
