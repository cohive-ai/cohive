package notify_test

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

// 本文件是英文用例：群通知、机器人回复、审批消息按工作空间的语言，私聊按收件人账号的语言。
// 其余测试固定用中文（见 i18n_test.go）。

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func enWorkspace(showTitle, showBrief bool, url string) tws {
	w := workspace(showTitle, showBrief, url)
	w.ws.Language = "en"
	return w
}

// 工作空间语言为 en 时，Telegram 群里的每一种事件都渲染成英文，信息不丢：单号、服务、谁交给谁、下一步、链接。
func TestRenderEnglishTelegram(t *testing.T) {
	is := sampleIssue()
	w := enWorkspace(true, true, "https://cohive.example.com")
	w.ws.Remind = store.RemindConfig{OpenHours: 4, VerifyHours: 24}
	for _, tc := range []struct {
		event, tool string
		actor       int64
		recipients  []int64
		want        []string
	}{
		{"assigned", "claude-code", claude.ID, []int64{codex.ID}, []string{"#12 assigned to lee-codex: accept, decline or transfer", "Service order-api, Sent by lee-claude via Claude Code"}},
		{"assigned", "codex", codex.ID, []int64{codex.ID}, []string{"Transferred by lee-codex via Codex"}},
		{"declined", "codex", codex.ID, []int64{claude.ID}, []string{"#12 declined by lee-codex, back to @lee_claude: check the reason"}},
		{"verify", "codex", codex.ID, []int64{claude.ID}, []string{"#12 fixed, @lee_claude please verify", "Verification requested by lee-codex via Codex"}},
		{"reopened", "claude-code", claude.ID, []int64{codex.ID}, []string{"#12 failed verification and was reopened for lee-codex"}},
		{"escalated", "claude-code", claude.ID, []int64{claude.ID, codex.ID}, []string{"#12 has been reopened several times and needs a human"}},
		{"accepted", "codex", codex.ID, []int64{claude.ID}, []string{"#12 accepted by lee-codex, FYI reporter @lee_claude", "Accepted by lee-codex via Codex"}},
		{"closed", "claude-code", claude.ID, []int64{codex.ID}, []string{"#12 verified and closed by lee-claude, FYI owner lee-codex", "Closed by lee-claude via Claude Code"}},
	} {
		text := notify.Render(w.base, w.ws, is, store.Notification{Event: tc.event, ActorMemberID: tc.actor, ActorTool: tc.tool, Recipients: tc.recipients}, members())
		for _, want := range append(tc.want, "Title: 下单接口返回 500", "https://cohive.example.com/w/payments/issues/12") {
			if !strings.Contains(text, want) {
				t.Errorf("%s：应包含 %q：\n%s", tc.event, want, text)
			}
		}
		// 标题与简述是用户输入的中文；其余文字都应是英文
		rest := strings.NewReplacer(is.Title, "", is.Brief, "").Replace(text)
		if hasHan(rest) {
			t.Errorf("%s：除用户输入外不应出现中文：\n%s", tc.event, text)
		}
	}
	// 告知型事件写“view”，要动手的写“handle”
	if text := render(t, enWorkspace(false, false, ""), is, "accepted", codex.ID, "codex", claude.ID); !strings.Contains(text, "to view it") || strings.Contains(text, "to handle it") {
		t.Fatalf("告知型事件：\n%s", text)
	}
	if text := render(t, enWorkspace(false, false, ""), is, "assigned", claude.ID, "codex", codex.ID); text == "" {
		t.Fatal("渲染不应为空")
	}
	// 超时催办
	n := store.Notification{Event: "overdue", IssueStatus: "OPEN", ActorMemberID: codex.ID, ActorTool: "cohive", Recipients: []int64{codex.ID}}
	text := notify.Render(w.base, w.ws, is, n, members())
	if !strings.Contains(text, "#12 waiting over 4 hours for acceptance: lee-codex please accept, decline or transfer") {
		t.Fatalf("待接单催办：\n%s", text)
	}
	n = store.Notification{Event: "overdue_admin", IssueStatus: "READY_FOR_VERIFICATION", ActorMemberID: claude.ID, ActorTool: "cohive", Recipients: []int64{claude.ID, linked.ID}}
	text = notify.Render(w.base, w.ws, is, n, members())
	if !strings.Contains(text, "#12 waiting over 48 hours for verification, still no action: @lee_claude please verify as soon as possible; admins ") {
		t.Fatalf("第 2 档催办：\n%s", text)
	}
	w.ws.Remind.OpenHours = 0
	n = store.Notification{Event: "overdue", IssueStatus: "OPEN", Recipients: []int64{codex.ID}}
	if text = notify.Render(w.base, w.ws, is, n, members()); !strings.Contains(text, "waiting a long time for acceptance") {
		t.Fatalf("时长已关：\n%s", text)
	}
	n = store.Notification{Event: "overdue", IssueStatus: "OPEN"}
	if text = notify.Render(w.base, w.ws, is, n, members()); !strings.Contains(text, "#12 timed out waiting for acceptance") {
		t.Fatalf("没有收件人：\n%s", text)
	}
	// 不 @ 操作者本人在英文里同样生效；未知成员写英文
	n = store.Notification{Event: "assigned", ActorMemberID: linked.ID, Recipients: []int64{linked.ID, 99}}
	text = notify.Render(w.base, w.ws, is, n, members())
	if strings.Contains(text, "tg://user?id=777") || !strings.Contains(text, "(unknown member)") {
		t.Fatalf("自己不 @、未知成员写英文：\n%s", text)
	}
}

// 工作空间没设语言时用服务器缺省语言；设了的以工作空间为准，与服务器缺省语言无关。
func TestRenderFollowsWorkspaceLanguage(t *testing.T) {
	is := sampleIssue()
	// 服务器缺省语言是中文（TestMain），工作空间没设：中文
	if text := render(t, workspace(false, false, ""), is, "assigned", claude.ID, "codex", codex.ID); !strings.Contains(text, "新单交给") {
		t.Fatalf("没设语言应跟随服务器缺省语言：\n%s", text)
	}
	// 服务器缺省语言改成英文：没设语言的工作空间变英文，设了中文的仍是中文
	i18n.SetDefault(i18n.EN)
	defer i18n.SetDefault(i18n.ZhCN)
	if text := render(t, workspace(false, false, ""), is, "assigned", claude.ID, "codex", codex.ID); !strings.Contains(text, "assigned to") {
		t.Fatalf("没设语言应跟随服务器缺省语言（英文）：\n%s", text)
	}
	w := workspace(false, false, "")
	w.ws.Language = "zh-CN"
	if text := render(t, w, is, "assigned", claude.ID, "codex", codex.ID); !strings.Contains(text, "新单交给") {
		t.Fatalf("工作空间设了中文：\n%s", text)
	}
}

// 钉钉：标题、链接文字、括注、加粗等随工作空间语言；结果消息的最后一行是 status 变量。
func TestRenderEnglishDingTalk(t *testing.T) {
	is := sampleIssue()
	people := map[int64]notify.Person{
		claude.ID: {Name: claude.Name},
		codex.ID:  {Name: codex.Name, Chats: []store.ChatIdentity{{Provider: store.ProviderDingTalk, Tenant: "corp", UserID: "u-codex"}}},
	}
	ws := store.Workspace{Name: "payments", Language: "en", Notify: store.NotifyConfig{Channel: store.ProviderDingTalk, Target: "cid", ShowTitle: true}}
	m := notify.RenderFor(notify.DingTalkFormat{}, store.ProviderDingTalk, "https://c.example.com", ws, is,
		store.Notification{Event: "closed", ActorMemberID: claude.ID, ActorTool: "claude-code", Recipients: []int64{codex.ID}}, people)
	if m.Title != "#12 Closed" || !strings.Contains(m.Text, "FYI owner @lee-codex") || len(m.Mentions) != 1 || m.Lang != i18n.EN ||
		!strings.Contains(m.Text, "[View issue](https://c.example.com/w/payments/issues/12)") {
		t.Fatalf("钉钉英文关单消息：%+v", m)
	}
	m = notify.RenderFor(notify.DingTalkFormat{}, store.ProviderDingTalk, "", ws, is,
		store.Notification{Event: "overdue_admin", IssueStatus: "OPEN", Recipients: []int64{codex.ID}}, people)
	if m.Title != "#12 Reminder (admins asked to step in)" {
		t.Fatalf("钉钉催办标题：%q", m.Title)
	}
	m = notify.RenderFor(notify.DingTalkFormat{}, store.ProviderDingTalk, "", ws, is,
		store.Notification{Event: "assigned", ActorMemberID: claude.ID, Recipients: []int64{codex.ID}}, people)
	if m.Title != "#12 New issue" {
		t.Fatalf("钉钉新单标题：%q", m.Title)
	}
}

// Telegram 命令回复按给定的语言。
func TestHandleCommandEnglish(t *testing.T) {
	reply, ok := notify.HandleCommand(msg("/chatid", "supergroup", -1001234567890, "lee"), "CohiveBot", i18n.EN)
	if !ok || !strings.Contains(reply, "The chat ID of this group (开发群) is <code>-1001234567890</code>") || !strings.Contains(reply, "Admin → Workspace") {
		t.Fatalf("/chatid：%s", reply)
	}
	reply, ok = notify.HandleCommand(msg("/chatid", "private", 42, "lee"), "CohiveBot", i18n.EN)
	if !ok || !strings.Contains(reply, "private chat") {
		t.Fatalf("私聊 /chatid：%s", reply)
	}
	reply, ok = notify.HandleCommand(msg("/whoami", "group", -1, ""), "CohiveBot", i18n.EN)
	if !ok || !strings.Contains(reply, "(not set)") || !strings.Contains(reply, "Connect Telegram") || hasHan(reply) {
		t.Fatalf("/whoami：%s", reply)
	}
}

// Telegram 机器人：群里的回复用群所属工作空间的语言，私聊用对方账号的语言，查不到时用服务器缺省语言。
func TestBotReplyLanguage(t *testing.T) {
	ctx := context.Background()
	enWS := store.Workspace{ID: 3, Name: "pay", Language: "en", Notify: store.NotifyConfig{Channel: "telegram", Target: "-1001"}}
	zhWS := store.Workspace{ID: 4, Name: "ops", Language: "zh-CN", Notify: store.NotifyConfig{Channel: "telegram", Target: "-1002"}}

	// 群里：英文工作空间的群回英文，中文工作空间的群回中文，没绑定的群用服务器缺省语言（中文）
	c := &fakeCore{workspace: enWS, groups: map[int64]store.Workspace{-1001: enWS, -1002: zhWS},
		actor: store.Account{ID: 9, Name: "lee"}}
	api := &fakeAPI{}
	bot := newBot(c, api)
	bot.Handle(ctx, msg("/chatid", "supergroup", -1001, "lee"))
	bot.Handle(ctx, msg("/chatid", "supergroup", -1002, "lee"))
	bot.Handle(ctx, msg("/chatid", "supergroup", -1003, "lee"))
	if len(api.replies) != 3 || !strings.Contains(api.replies[0], "chat ID") || !strings.Contains(api.replies[1], "的编号是") || !strings.Contains(api.replies[2], "的编号是") {
		t.Fatalf("群里的回复语言：%q", api.replies)
	}

	// /whoami 在英文群里：角色也是英文
	c2 := &fakeCore{workspace: enWS, groups: map[int64]store.Workspace{-1001: enWS}, actor: store.Account{ID: 9, Name: "lee"},
		memberships: []store.Membership{{WorkspaceID: 3, Role: store.RoleAdmin}}}
	api2 := &fakeAPI{}
	newBot(c2, api2).Handle(ctx, msg("/whoami", "supergroup", -1001, "lee_dev"))
	if len(api2.replies) != 1 || !strings.Contains(api2.replies[0], "Connected Cohive account: lee") ||
		!strings.Contains(api2.replies[0], "In this group's workspace pay: admin") || hasHan(api2.replies[0]) {
		t.Fatalf("英文群里的 /whoami：%q", api2.replies)
	}

	// 私聊：按收件人账号的语言（账号设了英文，服务器缺省语言是中文）
	c3 := &fakeCore{actor: store.Account{ID: 9, Name: "lee", Language: "en"}, link: core.LinkResult{Account: "lee"}}
	api3 := &fakeAPI{}
	bot3 := newBot(c3, api3)
	bot3.Handle(ctx, msg("/start", "private", 42, "lee"))
	bot3.Handle(ctx, msg("/start ln_abc", "private", 42, "lee"))
	bot3.Handle(ctx, msg("/chatid", "private", 42, "lee"))
	bot3.Handle(ctx, msg("/whoami", "private", 42, "lee"))
	if len(api3.replies) != 4 || !strings.Contains(api3.replies[0], "I'm the Cohive bot") ||
		!strings.Contains(api3.replies[1], "Connected to Cohive account lee") || !strings.Contains(api3.replies[2], "private chat") ||
		!strings.Contains(api3.replies[3], "Your Telegram username is") {
		t.Fatalf("私聊按账号语言：%q", api3.replies)
	}
	// 账号没设语言：用服务器缺省语言（中文）
	c4 := &fakeCore{actor: store.Account{ID: 9, Name: "lee"}}
	api4 := &fakeAPI{}
	newBot(c4, api4).Handle(ctx, msg("/start", "private", 42, "lee"))
	if len(api4.replies) != 1 || !strings.Contains(api4.replies[0], "我是 Cohive 机器人") {
		t.Fatalf("账号没设语言：%q", api4.replies)
	}
	// 还没连接账号的人私聊：服务器缺省语言；连接失败的提示也用它
	c5 := &fakeCore{actorErr: store.ErrNotFound, err: core.ErrLinkGone}
	api5 := &fakeAPI{}
	newBot(c5, api5).Handle(ctx, msg("/start ln_x", "private", 42, "lee"))
	if len(api5.replies) != 1 || !hasHan(api5.replies[0]) {
		t.Fatalf("未连接的人用服务器缺省语言：%q", api5.replies)
	}
}

// 审批消息、结果、加入通知、点击提示按各自工作空间的语言；同一个设备申请发到一英一中两个群，各用各的。
func TestApprovalMessagesEnglish(t *testing.T) {
	ctx := context.Background()
	enWS := store.Workspace{ID: 3, Name: "payments", Language: "en", Notify: store.NotifyConfig{Channel: "telegram", Target: "-1001"}}
	zhWS := store.Workspace{ID: 4, Name: "ops", Notify: store.NotifyConfig{Channel: "telegram", Target: "-1002"}}
	accID := int64(7)
	c := &fakeCore{
		byID:        map[int64]store.Workspace{3: enWS, 4: zhWS},
		groups:      map[int64]store.Workspace{-1001: enWS, -1002: zhWS},
		workspace:   enWS,
		memberships: []store.Membership{{WorkspaceID: 3, Label: "payments"}, {WorkspaceID: 4, Label: "ops"}},
		actor:       store.Account{ID: 9, Name: "boss"},
	}
	api := &fakeAPI{}
	bot := newBot(c, api)
	if err := bot.AnnounceDevice(ctx, store.DeviceAuth{ID: 6, AccountID: &accID, AccountName: "ann", DeviceName: "mac", Tools: []string{"codex", "claude-code"}}); err != nil {
		t.Fatal(err)
	}
	if len(api.buttons) != 2 {
		t.Fatalf("应发到两个群：%v", api.buttons)
	}
	if want := "ann requested to add a new device mac (tools: codex, claude-code)"; !strings.Contains(api.buttons[0], want) ||
		!strings.Contains(api.buttons[0], "all of their workspaces: payments, ops") || !strings.Contains(api.buttons[0], "Admin → Pending") || hasHan(api.buttons[0]) {
		t.Fatalf("英文群里的设备申请：%s", api.buttons[0])
	}
	if !strings.Contains(api.buttons[1], "申请添加新设备") {
		t.Fatalf("中文群里的设备申请：%s", api.buttons[1])
	}
	if api.btnData[0][0].Text != "Approve" || api.btnData[0][1].Text != "Reject" || api.btnData[1][0].Text != "批准" {
		t.Fatalf("按钮文字随群的语言：%+v", api.btnData)
	}

	// 在英文群里批准：该群的消息改成英文结果，另一个（中文）群的消息改成中文结果
	bot.Handle(ctx, callbackIn("da:3:6:y", 900, -1001))
	if len(api.answers) != 1 || api.answers[0] != "Approved" {
		t.Fatalf("点击提示：%v", api.answers)
	}
	if len(api.edits) != 2 || !strings.Contains(api.edits[0], "Approved (by boss)") || !strings.Contains(api.edits[1], "已批准（审批人 boss）") {
		t.Fatalf("各群的结果消息各用各的语言：%v", api.edits)
	}

	// 点击的提示：没连接账号、已处理
	c.actorErr = store.ErrNotFound
	api.answers = nil
	bot.Handle(ctx, callbackIn("da:3:6:y", 901, -1001))
	if len(api.answers) != 1 || !strings.Contains(api.answers[0], "Your Telegram account isn't connected to Cohive") {
		t.Fatalf("未连接的提示：%v", api.answers)
	}
	c.actorErr = nil
	c.err = core.ErrDecided
	api.answers = nil
	bot.Handle(ctx, callbackIn("su:3:5:n", 900, -1001))
	if len(api.answers) != 1 || hasHan(api.answers[0]) {
		t.Fatalf("已处理的提示应是英文：%v", api.answers)
	}
	// 按钮与审批消息记录不符
	api.answers = nil
	bot.Handle(ctx, callbackIn("su:4:5:y", 900, -1001))
	if len(api.answers) != 1 || !strings.Contains(api.answers[0], "doesn't match the request") {
		t.Fatalf("与记录不符的提示：%v", api.answers)
	}
	// 群没有绑定工作空间：查不到群语言，用服务器缺省语言（这里临时设成英文）
	i18n.SetDefault(i18n.EN)
	defer i18n.SetDefault(i18n.ZhCN)
	api5 := &fakeAPI{}
	newBot(&fakeCore{workspace: enWS, groups: map[int64]store.Workspace{}}, api5).Handle(ctx, callbackIn("su:3:5:y", 900, -1001))
	if len(api5.answers) != 1 || !strings.Contains(api5.answers[0], "doesn't belong to the workspace bound to this group") {
		t.Fatalf("群没有绑定时的提示：%v", api5.answers)
	}
}

// 注册申请与加入通知：英文工作空间里是英文（含已有账号的写法与括注）。
func TestSignupAndJoinedEnglish(t *testing.T) {
	ctx := context.Background()
	ws := store.Workspace{ID: 3, Name: "payments", Language: "en", Notify: store.NotifyConfig{Channel: "telegram", Target: "-1001"}}
	api := &fakeAPI{}
	bot := newBot(&fakeCore{workspace: ws}, api)
	if err := bot.AnnounceSignup(ctx, store.Signup{ID: 5, WorkspaceID: 3, Name: "ann", DisplayName: "Ann"}); err != nil {
		t.Fatal(err)
	}
	if err := bot.AnnounceSignup(ctx, store.Signup{ID: 6, WorkspaceID: 3, Name: "bob", Existing: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(api.buttons[0], "New member ann (Ann) requested to join workspace payments.") ||
		!strings.Contains(api.buttons[1], "bob (existing account) requested to join workspace payments.") || hasHan(api.buttons[0]+api.buttons[1]) {
		t.Fatalf("注册申请：%v", api.buttons)
	}
	id := int64(555)
	m := store.Member{ID: 7, WorkspaceID: 3, Name: "ann", DisplayName: "Ann", TelegramUserID: &id}
	if err := bot.AnnounceJoined(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := bot.AnnounceSelfJoined(ctx, m); err != nil {
		t.Fatal(err)
	}
	if len(api.texts) != 2 || !strings.Contains(api.texts[0], `<a href="tg://user?id=555">ann (Ann)</a> joined workspace payments.`) ||
		!strings.Contains(api.texts[1], "ann (Ann) joined workspace payments.") || strings.Contains(api.texts[1], "tg://") {
		t.Fatalf("加入通知：%q", api.texts)
	}
}

// 钉钉：群里指令回复用群所属工作空间的语言；中文指令与英文指令都认；单聊用对方账号的语言。
func TestDingTalkBotEnglish(t *testing.T) {
	g := newFakeGateway(t)
	enWS := store.Workspace{ID: 4, Name: "ops", Language: "en", Notify: store.NotifyConfig{Channel: store.ProviderDingTalk, Target: "cid-ops"}}
	fc := &fakeCore{workspace: enWS, dtGroups: map[string]store.Workspace{"cid-ops": enWS},
		dtUsers: map[string]store.Account{"corp1/u-lee": {ID: 9, Name: "lee", Language: "en"}, "corp1/u-zh": {ID: 10, Name: "wang"}}}
	dc := &fakeDTCore{link: core.LinkSubmit{Account: "lee"}}
	ch := &notify.DingTalkChannel{API: g.api(), Core: dc, Logf: t.Logf}
	ch.Hub = &notify.Hub{Core: fc, Channels: map[string]notify.Channel{store.ProviderDingTalk: ch}, Logf: t.Logf}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ch.Run(ctx)
	<-g.opened
	waitOnline(t, ch)
	hook := g.srv.URL + "/robot/send"
	say := func(id, conv, kind, user, content string) {
		g.frames <- frame("CALLBACK", "/v1.0/im/bot/messages/get", id, map[string]any{
			"conversationId": conv, "conversationType": kind, "conversationTitle": "Ops", "senderStaffId": user, "senderCorpId": "corp1",
			"sessionWebhook": hook, "msgtype": "text", "text": map[string]string{"content": content}})
		g.ackFor(t, id)
	}
	say("m1", "cid-ops", "2", "u-lee", "@Cohive chatid")
	say("m2", "cid-ops", "2", "u-lee", "@Cohive 群编号") // 中文指令继续认
	say("m3", "cid-ops", "2", "u-lee", "@Cohive hello")
	say("m4", "cid-dm", "1", "u-lee", "hello")
	say("m5", "cid-dm", "1", "u-zh", "hello") // 账号没设语言：服务器缺省语言（中文）
	say("m6", "cid-ops", "2", "u-lee", "@Cohive bindgroup GOODCODE")
	replies := strings.Split(waitReplies(t, g, 6), "\n")
	joined := strings.Join(replies, "\n")
	for _, want := range []string{"The chat ID of this group (Ops) is `cid-ops`", "`bindgroup CODE`", "To connect your DingTalk account to Cohive, open Profile",
		"This group is now bound to workspace", "我是 Cohive 机器人。要把你的钉钉账号连接到 Cohive"} {
		if !strings.Contains(joined, want) {
			t.Errorf("回复应包含 %q：\n%s", want, joined)
		}
	}
	if strings.Count(joined, "The chat ID of this group (Ops) is") != 2 {
		t.Errorf("中文指令“群编号”应同样得到英文回复：\n%s", joined)
	}
}

// 钉钉审批卡片：Stream 不在线时的降级提示、点击的“处理中”提示按工作空间语言。
func TestDingTalkApprovalEnglish(t *testing.T) {
	f := newFakeDingTalk(t)
	ctx := context.Background()
	card := &notify.DingTalkChannel{API: f.api(), NoticeTemplate: "tpl-n"}
	if _, err := card.SendApproval(ctx, "cid1", notify.Rendered{Title: "Sign-up request", Text: "x", Lang: i18n.EN}, notify.Approval{Kind: "su", WorkspaceID: 4, RefID: 5}); err != nil {
		t.Fatal(err)
	}
	if c := f.callsTo("/v1.0/robot/groupMessages/send"); len(c) != 1 || !strings.Contains(c[0].body["msgParam"].(string), "Admin → Pending") ||
		hasHan(c[0].body["msgParam"].(string)) {
		t.Fatalf("英文降级提示：%+v", c)
	}

	g := newFakeGateway(t)
	ws := store.Workspace{ID: 4, Name: "ops", Language: "en", Notify: store.NotifyConfig{Channel: store.ProviderDingTalk, Target: "cid-ops"}}
	fc := &fakeCore{workspace: ws, dtGroups: map[string]store.Workspace{"cid-ops": ws},
		dtUsers: map[string]store.Account{"corp1/u-lee": {ID: 9, Name: "lee"}}, delay: 300 * time.Millisecond}
	ch := &notify.DingTalkChannel{API: g.api(), ApprovalTemplate: "tpl-a", Logf: t.Logf, ClickWait: 50 * time.Millisecond}
	ch.Hub = &notify.Hub{Core: fc, Channels: map[string]notify.Channel{store.ProviderDingTalk: ch}, Logf: t.Logf}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go ch.Run(rctx)
	<-g.opened
	waitOnline(t, ch)
	if err := ch.Hub.AnnounceSignup(rctx, store.Signup{ID: 5, WorkspaceID: 4, Name: "ann"}); err != nil {
		t.Fatal(err)
	}
	ref := fc.posts[0].MessageID
	g.frames <- frame("CALLBACK", "/v1.0/card/instances/callback", "c1", map[string]any{
		"outTrackId": ref, "corpId": "corp1", "userId": "u-lee",
		"content": `{"cardPrivateData":{"params":{"decision":"approve"}}}`})
	a := g.ackFor(t, "c1")
	if !strings.Contains(a["data"].(string), "Processing. The card will show the result shortly.") {
		t.Fatalf("“处理中”提示应是英文：%v", a)
	}
	for i := 0; i < 200 && len(g.callsTo("/v1.0/card/instances")) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	u := g.callsTo("/v1.0/card/instances")
	if len(u) != 1 || !strings.Contains(u[0].body["cardData"].(map[string]any)["cardParamMap"].(map[string]any)["status"].(string), "Approved (by") {
		t.Fatalf("结果卡片的状态应是英文：%+v", u)
	}
}

// 管理员在服务器缺省语言为中文时看到的渠道提示（给管理员看，用服务器缺省语言）。
func TestDingTalkStatusLanguage(t *testing.T) {
	ch := &notify.DingTalkChannel{API: &notify.DingTalk{}}
	_, detail := ch.Status()
	if !strings.Contains(detail, "未配通知卡片模板") {
		t.Fatalf("中文提示：%s", detail)
	}
	i18n.SetDefault(i18n.EN)
	defer i18n.SetDefault(i18n.ZhCN)
	_, detail = ch.Status()
	if !strings.Contains(detail, "Notification card template not set") || !strings.Contains(detail, "; ") {
		t.Fatalf("英文提示：%s", detail)
	}
}
