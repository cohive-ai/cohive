package notify

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// SlackChannel 是 Slack 的通知通道：群通知与审批消息用消息块发，按用户编号 @ 人；按钮点击、@机器人、私聊经
// Socket 模式长连接收，服务端只需出站访问。一个 Slack 应用只装在一个工作区里，只服务这一个工作区：
// 别的工作区的人（跨组织共享频道里的外部成员）点按钮、发码一律拒绝。
type SlackChannel struct {
	API  *Slack
	Core CodeCore // 绑定码操作；为空时不处理绑定码
	Hub  *Hub
	Logf func(format string, args ...any)

	socket *slackSocket
	mu     sync.Mutex
	self   slackSelf       // 机器人自己的用户编号与工作区，每次连接时查一次
	seen   map[string]bool // 处理过的事件编号（Slack 没收到确认时会重发）
	order  []string
}

// slackCaps：Slack 没有带参数的机器人链接，账号连接与群绑定都用码；认不出的消息回复用法。
var slackCaps = InboxCaps{LinkByCode: true, BindByCode: true, Help: true}

func (s *SlackChannel) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
		return
	}
	if s.Hub != nil {
		s.Hub.logf(format, args...)
	}
}

func (s *SlackChannel) Provider() string  { return store.ProviderSlack }
func (s *SlackChannel) Format() Formatter { return SlackFormat{} }

func (s *SlackChannel) socketOf() *slackSocket {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.socket == nil {
		s.socket = &slackSocket{api: s.API, logf: s.logf, onConnect: s.whoAmI, onEvent: s.onEvent}
	}
	return s.socket
}

// Run 维持 Socket 模式长连接，直到 ctx 结束。
func (s *SlackChannel) Run(ctx context.Context) { s.socketOf().Run(ctx) }

// Status 返回 Socket 模式是否在线，以及不在线的原因（给管理员看）。
func (s *SlackChannel) Status() (bool, string) { return s.socketOf().status() }

// whoAmI 查机器人自己的用户编号与工作区：用来忽略自己发的消息、认出别的工作区的人。令牌错时连接不上，原因显示在状态里。
func (s *SlackChannel) whoAmI(ctx context.Context) error {
	me, err := s.API.AuthTest(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.self = me
	s.mu.Unlock()
	return nil
}

func (s *SlackChannel) me() slackSelf {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.self
}

// ---- 发送 ----

// Send 往频道里发一条通知。
func (s *SlackChannel) Send(ctx context.Context, chat string, m Rendered) error {
	_, err := s.API.PostMessage(ctx, slackMessage{Channel: chat, Text: m.Text, Blocks: []any{slackSection(m.Text)}})
	return err
}

// slackApprovalValue 是审批按钮带的数据：种类:工作空间:编号:y 或 n（与 Telegram 相同，只用来核对，审批对象以审批消息记录为准）。
func slackApprovalValue(a Approval, approve bool) string {
	yn := "n"
	if approve {
		yn = "y"
	}
	return a.Kind + ":" + strconv.FormatInt(a.WorkspaceID, 10) + ":" + strconv.FormatInt(a.RefID, 10) + ":" + yn
}

// SendApproval 发带“批准 / 拒绝”按钮的审批消息，返回消息时间戳。Socket 模式不在线（点了按钮也收不到）时
// 发不带按钮的文字，提示去网页处理；仍返回时间戳，别处处理后照样改成结果。
func (s *SlackChannel) SendApproval(ctx context.Context, chat string, m Rendered, a Approval) (string, error) {
	l := m.lang()
	if online, _ := s.socketOf().status(); !online {
		text := m.Text + "\n" + l.T("notify.slack.no_buttons")
		return s.API.PostMessage(ctx, slackMessage{Channel: chat, Text: text, Blocks: []any{slackSection(text)}})
	}
	button := func(label, id, style string, approve bool) map[string]any {
		return map[string]any{"type": "button", "action_id": id, "style": style, "value": slackApprovalValue(a, approve),
			"text": map[string]any{"type": "plain_text", "text": label}}
	}
	actions := map[string]any{"type": "actions", "block_id": "cohive_approval", "elements": []any{
		button(l.T("notify.button.approve"), "cohive_approve", "primary", true),
		button(l.T("notify.button.reject"), "cohive_reject", "danger", false)}}
	return s.API.PostMessage(ctx, slackMessage{Channel: chat, Text: m.Text, Blocks: []any{slackSection(m.Text), actions}})
}

// FinishApproval 把审批消息改成结果、去掉按钮。
func (s *SlackChannel) FinishApproval(ctx context.Context, chat, msgRef string, m Rendered) error {
	if msgRef == "" {
		return nil
	}
	return s.API.UpdateMessage(ctx, slackMessage{Channel: chat, TS: msgRef, Text: m.Text, Blocks: []any{slackSection(m.Text)}})
}

// ---- 收件 ----

// firstSeen 记下事件编号，重复的返回假。只记最近 500 个。
func (s *SlackChannel) firstSeen(id string) bool {
	if id == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if s.seen[id] {
		return false
	}
	s.seen[id] = true
	s.order = append(s.order, id)
	if len(s.order) > 500 {
		delete(s.seen, s.order[0])
		s.order = s.order[1:]
	}
	return true
}

// onEvent 分派 Socket 模式推来的事件：按钮点击与消息各开协程处理。
func (s *SlackChannel) onEvent(_ context.Context, kind string, payload json.RawMessage) {
	switch kind {
	case "interactive":
		var p slackActionPayload
		if json.Unmarshal(payload, &p) != nil || p.Type != "block_actions" {
			return
		}
		go s.withTimeout(func(ctx context.Context) { s.onClick(ctx, p) })
	case "events_api":
		var p struct {
			TeamID  string     `json:"team_id"`
			EventID string     `json:"event_id"`
			Event   slackEvent `json:"event"`
		}
		if json.Unmarshal(payload, &p) != nil || !s.firstSeen(p.EventID) {
			return
		}
		go s.withTimeout(func(ctx context.Context) { s.onMessage(ctx, p.TeamID, p.Event) })
	}
}

func (s *SlackChannel) withTimeout(f func(ctx context.Context)) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f(ctx)
}

// external 判断某个工作区的人是不是外人（跨组织共享频道里别的工作区的成员）。还不知道自己的工作区时不拦。
func (s *SlackChannel) external(team string) bool {
	mine := s.me().TeamID
	return team != "" && mine != "" && team != mine
}

// slackActionPayload 是按钮点击（block_actions）。用户、工作区、频道都由 Slack 填写。
type slackActionPayload struct {
	Type string `json:"type"`
	Team struct {
		ID string `json:"id"`
	} `json:"team"`
	User struct {
		ID       string `json:"id"`
		TeamID   string `json:"team_id"`
		Username string `json:"username"`
		Name     string `json:"name"`
	} `json:"user"`
	Channel struct {
		ID string `json:"id"`
	} `json:"channel"`
	Container struct {
		MessageTS string `json:"message_ts"`
		ChannelID string `json:"channel_id"`
	} `json:"container"`
	Message struct {
		TS string `json:"ts"`
	} `json:"message"`
	Actions []struct {
		ActionID string `json:"action_id"`
		Value    string `json:"value"`
	} `json:"actions"`
}

// clickFromSlack 把按钮点击翻译成 Click；不是审批按钮时 ok 为假。身份只取 Slack 填写的用户与工作区。
func clickFromSlack(p slackActionPayload) (Click, bool) {
	if len(p.Actions) == 0 {
		return Click{}, false
	}
	parts := strings.Split(p.Actions[0].Value, ":")
	if len(parts) != 4 || (parts[0] != "su" && parts[0] != "da") || (parts[3] != "y" && parts[3] != "n") {
		return Click{}, false
	}
	wsID, err1 := strconv.ParseInt(parts[1], 10, 64)
	id, err2 := strconv.ParseInt(parts[2], 10, 64)
	if err1 != nil || err2 != nil {
		return Click{}, false
	}
	chat, ts := p.Channel.ID, p.Message.TS
	if chat == "" {
		chat = p.Container.ChannelID
	}
	if ts == "" {
		ts = p.Container.MessageTS
	}
	team := p.User.TeamID
	if team == "" {
		team = p.Team.ID
	}
	if chat == "" || ts == "" || p.User.ID == "" {
		return Click{}, false
	}
	return Click{Provider: store.ProviderSlack, ChatID: chat, MsgRef: ts, Approve: parts[3] == "y",
		Approval: Approval{Kind: parts[0], WorkspaceID: wsID, RefID: id},
		From:     ChatUser{Provider: store.ProviderSlack, Tenant: team, UserID: p.User.ID, Handle: p.User.Username, Name: p.User.Name}}, true
}

// onClick 处理审批按钮：确认已在连接上做过；审批后把被点的消息改成结果，没改（没权限、已处理）时只给点击者发一条临时提示。
func (s *SlackChannel) onClick(ctx context.Context, p slackActionPayload) {
	c, ok := clickFromSlack(p)
	if !ok || s.Hub == nil {
		return
	}
	tell := func(text string) {
		if text == "" {
			return
		}
		if err := s.API.PostEphemeral(ctx, slackMessage{Channel: c.ChatID, User: c.From.UserID, Text: text}); err != nil {
			s.logf("failed to answer Slack button click: %v", err)
		}
	}
	if s.external(c.From.Tenant) {
		tell(s.Hub.ClickLang(ctx, c).T("notify.slack.external_user"))
		return
	}
	res := s.Hub.Click(ctx, c)
	if res.Result != nil {
		if err := s.FinishApproval(ctx, c.ChatID, c.MsgRef, *res.Result); err != nil {
			s.logf("failed to update approval message: %v", err)
		}
	} else {
		tell(res.Toast)
	}
	if res.After != nil {
		res.After(ctx)
	}
}

// slackEvent 是 @机器人（app_mention）或私聊（message，channel_type 为 im）。字段由 Slack 填写。
type slackEvent struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	User        string `json:"user"`
	UserTeam    string `json:"user_team"` // 跨组织共享频道里是发消息的人所在的工作区
	Team        string `json:"team"`
	BotID       string `json:"bot_id"`
	Text        string `json:"text"`
	TS          string `json:"ts"`
	ThreadTS    string `json:"thread_ts"`
	Channel     string `json:"channel"`
	ChannelType string `json:"channel_type"`
}

// slackText 把 Slack 消息文字还原成用户打的字：反转义 &amp; &lt; &gt;，去掉开头的 <@机器人>。
func slackText(text string) string {
	text = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&").Replace(text)
	fields := strings.Fields(text)
	for len(fields) > 0 && strings.HasPrefix(fields[0], "<@") && strings.HasSuffix(fields[0], ">") {
		fields = fields[1:]
	}
	return strings.Join(fields, " ")
}

// onMessage 处理 @机器人 与私聊：翻译成统一的收件消息交给 Inbox，再把回复发回同一个会话（在话题里问的就回到话题里）。
func (s *SlackChannel) onMessage(ctx context.Context, team string, ev slackEvent) {
	private := ev.Type == "message" && ev.ChannelType == "im"
	if ev.Type != "app_mention" && !private {
		return
	}
	if ev.Subtype != "" || ev.BotID != "" || ev.User == "" || ev.User == s.me().UserID || ev.Channel == "" {
		return // 编辑、删除、机器人自己与别的机器人发的消息不处理
	}
	if ev.UserTeam != "" {
		team = ev.UserTeam
	} else if ev.Team != "" {
		team = ev.Team
	}
	in := Inbound{Provider: store.ProviderSlack, ChatID: ev.Channel, Private: private,
		From: ChatUser{Provider: store.ProviderSlack, Tenant: team, UserID: ev.User}}
	in.Cmd, in.First, in.Args = parseText(slackText(ev.Text))
	x := Inbox{Hub: s.Hub, Codes: s.Core, Caps: slackCaps, Logf: s.logf}
	var reply string
	if s.external(team) {
		reply = x.lang(ctx, in).T("notify.slack.external_user")
	} else {
		if private {
			in.From.Name = s.API.UserName(ctx, ev.User) // 网页确认连接时给本人看是谁发来的码
		} else {
			if name := s.API.ChannelName(ctx, ev.Channel); name != "" { // 绑群时记下频道名，网页上显示
				in.ChatTitle = "#" + name
			}
		}
		reply = slackReplyText(x.Handle(ctx, in), in)
	}
	if reply == "" {
		return
	}
	if _, err := s.API.PostMessage(ctx, slackMessage{Channel: ev.Channel, ThreadTS: ev.ThreadTS, Text: reply}); err != nil {
		s.logf("failed to reply to Slack message: %v", err)
	}
}

// slackReplyText 按 Slack 的文案与 mrkdwn 写出回复。
func slackReplyText(r Reply, in Inbound) string {
	f, l := SlackFormat{}, r.Lang
	switch r.Kind {
	case ReplyHelp:
		if r.Private {
			return l.T("notify.slack.private_help")
		}
		return l.T("notify.slack.group_help")
	case ReplyChatID:
		if r.Private { // 私聊里没有频道编号可说，回用法
			return l.T("notify.slack.private_help")
		}
		title := in.ChatTitle
		if title == "" {
			title = in.ChatID
		}
		return l.T("notify.slack.chatid", i18n.D{"Title": f.Esc(title), "ID": f.Code(in.ChatID)})
	case ReplyWhoami:
		switch {
		case !r.Checked:
			return ""
		case !r.Linked:
			return l.T("notify.slack.whoami_unlinked")
		}
		text := l.T("notify.slack.whoami_linked", i18n.D{"Account": f.Esc(r.Account)})
		if r.Workspace != "" {
			text += "\n" + l.T("notify.slack.whoami_role", i18n.D{"Workspace": f.Esc(r.Workspace), "Role": r.Role})
		}
		return text
	case ReplyLinkReceived:
		return l.T("notify.slack.link_received", i18n.D{"Account": f.Esc(r.Account)})
	case ReplyBound:
		return l.T("notify.slack.bound", i18n.D{"Workspace": f.Esc(r.Workspace)})
	case ReplyError:
		return f.Esc(r.Err)
	case ReplyTryLater:
		return l.T("notify.slack.try_later")
	}
	return ""
}
