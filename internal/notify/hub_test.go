package notify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

// plainFormat 是测试用的第二种通讯工具格式：@ 写成 [@编号]，便于断言。
type plainFormat struct{}

func (plainFormat) Esc(s string) string               { return strings.ReplaceAll(s, "*", `\*`) }
func (plainFormat) Code(s string) string              { return "`" + s + "`" }
func (plainFormat) Link(_ i18n.Lang, u string) string { return "<" + u + ">" }
func (plainFormat) Mention(_ i18n.Lang, label string, id *store.ChatIdentity, _ bool) string {
	if id == nil || id.UserID == "" {
		return label
	}
	return "[@" + id.UserID + "]" + label
}

// fakeChannel 记录发出的消息。
type fakeChannel struct {
	provider  string
	sent      []notify.Rendered
	approvals []notify.Approval
	finished  []string // chat|msg|text
}

func (f *fakeChannel) Provider() string         { return f.provider }
func (f *fakeChannel) Format() notify.Formatter { return plainFormat{} }
func (f *fakeChannel) Send(_ context.Context, chat string, m notify.Rendered) error {
	f.sent = append(f.sent, m)
	return nil
}
func (f *fakeChannel) SendApproval(_ context.Context, chat string, m notify.Rendered, a notify.Approval) (string, error) {
	f.sent = append(f.sent, m)
	f.approvals = append(f.approvals, a)
	return "card-" + chat, nil
}
func (f *fakeChannel) FinishApproval(_ context.Context, chat, msg string, m notify.Rendered) error {
	f.finished = append(f.finished, chat+"|"+msg+"|"+m.Text)
	return nil
}

// 两种通讯工具同时启用：消息按工作空间绑定的群发到对应的通道，各用各的格式；
// 任一处审批后，两种通道里的审批消息都改成结果。
func TestHubRoutesByWorkspaceChannel(t *testing.T) {
	ctx := context.Background()
	tgWS := store.Workspace{ID: 3, Name: "pay", Notify: store.NotifyConfig{Channel: store.ProviderTelegram, Target: "-1001"}}
	dtWS := store.Workspace{ID: 4, Name: "ops", Notify: store.NotifyConfig{Channel: store.ProviderDingTalk, Target: "cid-ops"}}
	accID := int64(7)
	c := &fakeCore{
		byID:        map[int64]store.Workspace{3: tgWS, 4: dtWS},
		groups:      map[int64]store.Workspace{-1001: tgWS},
		memberships: []store.Membership{{WorkspaceID: 3, Label: "pay"}, {WorkspaceID: 4, Label: "ops"}},
		actor:       store.Account{ID: 9, Name: "boss"},
	}
	api := &fakeAPI{}
	dt := &fakeChannel{provider: store.ProviderDingTalk}
	bot := &notify.Bot{Core: c, API: api, Name: "CohiveBot"}
	hub := &notify.Hub{Core: c, Channels: map[string]notify.Channel{store.ProviderTelegram: bot, store.ProviderDingTalk: dt}}
	bot.Hub = hub

	if err := hub.AnnounceDevice(ctx, store.DeviceAuth{ID: 6, AccountID: &accID, AccountName: "a*n", DeviceName: "mac"}); err != nil {
		t.Fatal(err)
	}
	if len(api.chats) != 1 || api.chats[0] != -1001 {
		t.Fatalf("Telegram 群应收到一条：%v", api.chats)
	}
	if len(dt.approvals) != 1 || dt.approvals[0] != (notify.Approval{Kind: "da", WorkspaceID: 4, RefID: 6}) {
		t.Fatalf("钉钉群应收到一条：%+v", dt.approvals)
	}
	if !strings.Contains(dt.sent[0].Text, `a\*n`) || strings.Contains(api.buttons[0], `\*`) {
		t.Fatalf("各通道用各自的转义：%q / %q", dt.sent[0].Text, api.buttons[0])
	}
	if len(c.posts) != 2 || c.posts[1].Provider != store.ProviderDingTalk || c.posts[1].MessageID != "card-cid-ops" {
		t.Fatalf("两条审批消息都应记下：%+v", c.posts)
	}

	// 在 Telegram 群里批准：Telegram 那条由通道自己改，钉钉那条由 Hub 改
	bot.Handle(ctx, callbackIn("da:3:6:y", 900, -1001))
	if len(api.edits) != 1 || len(dt.finished) != 1 || !strings.HasPrefix(dt.finished[0], "cid-ops|card-cid-ops|") ||
		!strings.Contains(dt.finished[0], "已批准") {
		t.Fatalf("两种通道的审批消息都应改成结果：%v / %v", api.edits, dt.finished)
	}
}

// 审批对象以审批消息记录为准：按钮数据与记录不符时拒绝；没有记录又没有按钮数据（钉钉卡片）时拒绝；
// 钉钉卡片回调不带群时，以记录里的群核对工作空间。
func TestHubClickTrustsRecordedPost(t *testing.T) {
	ctx := context.Background()
	ws := store.Workspace{ID: 3, Name: "pay", Notify: store.NotifyConfig{Channel: store.ProviderTelegram, Target: "-1001"}}
	c := &fakeCore{workspace: ws, groups: map[int64]store.Workspace{-1001: ws}, actor: store.Account{ID: 9, Name: "lee"},
		posts: []store.BotPost{{Kind: "su", RefID: 5, WorkspaceID: 3, Provider: store.ProviderTelegram, ChatID: "-1001", MessageID: "77"}}}
	hub := &notify.Hub{Core: c, Channels: map[string]notify.Channel{store.ProviderTelegram: &notify.Bot{Core: c, API: &fakeAPI{}}}}
	from := notify.ChatUser{Provider: store.ProviderTelegram, UserID: "900"}

	res := hub.Click(ctx, notify.Click{Provider: store.ProviderTelegram, ChatID: "-1001", MsgRef: "77", From: from,
		Approval: notify.Approval{Kind: "su", WorkspaceID: 3, RefID: 6}, Approve: true})
	if res.Result != nil || !strings.Contains(res.Toast, "不符") || len(c.calls) != 0 {
		t.Fatalf("按钮数据与记录不符应拒绝且不审批：%+v %v", res, c.calls)
	}
	res = hub.Click(ctx, notify.Click{Provider: store.ProviderTelegram, ChatID: "-1001", MsgRef: "999", From: from, Approve: true})
	if res.Result != nil || !strings.Contains(res.Toast, "失效") {
		t.Fatalf("没有记录又没有按钮数据应拒绝：%+v", res)
	}
	res = hub.Click(ctx, notify.Click{Provider: store.ProviderTelegram, MsgRef: "77", From: from, Approve: true})
	if res.Result == nil || len(c.calls) < 3 || c.calls[2] != "signup" || c.calls[5] != int64(5) {
		t.Fatalf("不带群时按记录的群核对后审批：%+v %v", res, c.calls)
	}
}

// 群消息按编号 @：只 @ 连接了本通讯工具的人，Mentions 只列出他们。
func TestRenderForMentionsLinkedOnly(t *testing.T) {
	ws := store.Workspace{ID: 1, Name: "pay"}
	is := store.Issue{ID: 12, Title: "t"}
	n := store.Notification{Event: "verify", Recipients: []int64{1, 2}, ActorMemberID: 3}
	people := map[int64]notify.Person{
		1: {Name: "ann", Chats: []store.ChatIdentity{{Provider: store.ProviderDingTalk, Tenant: "corp", UserID: "u-ann"}}},
		2: {Name: "bob", Chats: []store.ChatIdentity{{Provider: store.ProviderTelegram, UserID: "42"}}},
		3: {Name: "lee"},
	}
	m := notify.RenderFor(plainFormat{}, store.ProviderDingTalk, "", ws, is, n, people)
	if !strings.Contains(m.Text, "[@u-ann]ann") || !strings.Contains(m.Text, "bob") || strings.Contains(m.Text, "[@42]") {
		t.Fatalf("只按钉钉连接 @：%s", m.Text)
	}
	if len(m.Mentions) != 1 || m.Mentions[0].UserID != "u-ann" || m.Title != "#12 请验证" {
		t.Fatalf("Mentions 与标题：%+v %q", m.Mentions, m.Title)
	}
}

// 加入通知：管理员让人加入时 @ 他；本人凭邀请加入（操作者是他自己）只写名字不 @。
func TestSelfJoinedNoMention(t *testing.T) {
	ctx := context.Background()
	ws := store.Workspace{ID: 3, Name: "pay", Notify: store.NotifyConfig{Channel: store.ProviderTelegram, Target: "-1001"}}
	c := &fakeCore{workspace: ws}
	api := &fakeAPI{}
	bot := &notify.Bot{Core: c, API: api}
	id := int64(555)
	m := store.Member{ID: 7, WorkspaceID: 3, Name: "ann", TelegramUserID: &id, TelegramUsername: "anntg"}
	if err := bot.AnnounceJoined(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := bot.AnnounceSelfJoined(ctx, m); err != nil {
		t.Fatal(err)
	}
	if len(api.texts) != 2 || !strings.Contains(api.texts[0], "tg://user?id=555") || strings.Contains(api.texts[1], "tg://user?id=555") ||
		!strings.Contains(api.texts[1], "ann 已加入工作空间 pay") {
		t.Fatalf("管理员让人加入时 @，本人加入时只写名字：%q", api.texts)
	}
}

// 归档、取消归档后在绑定的群里说一声（工作空间语言）；没绑群时不发。
func TestAnnounceArchived(t *testing.T) {
	ctx := context.Background()
	ws := store.Workspace{ID: 3, Name: "pay", DisplayName: "支付组", Notify: store.NotifyConfig{Channel: store.ProviderTelegram, Target: "-1001"}}
	c := &fakeCore{workspace: ws}
	api := &fakeAPI{}
	bot := &notify.Bot{Core: c, API: api}
	if err := bot.AnnounceArchived(ctx, 3, true, "lee"); err != nil {
		t.Fatal(err)
	}
	if err := bot.AnnounceArchived(ctx, 3, false, "lee"); err != nil {
		t.Fatal(err)
	}
	if len(api.texts) != 2 || !strings.Contains(api.texts[0], "支付组 已被 lee 归档") || !strings.Contains(api.texts[0], "不再在本群发通知") ||
		!strings.Contains(api.texts[1], "支付组 已被 lee 取消归档") {
		t.Fatalf("应各告知一次：%q", api.texts)
	}
	c.workspace = store.Workspace{ID: 3, Name: "pay"}
	if err := bot.AnnounceArchived(ctx, 3, true, "lee"); err != nil || len(api.texts) != 2 {
		t.Fatalf("没绑群不应发：%v %q", err, api.texts)
	}
}
