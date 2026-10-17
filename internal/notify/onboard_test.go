package notify_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

// fakeAPI 记录机器人发出的 Telegram 操作。
type fakeAPI struct {
	replies  []string
	texts    []string
	buttons  []string
	btnData  [][]notify.Button
	edits    []string
	answers  []string
	editKeys [][][]notify.Button
	chats    []int64
}

func (f *fakeAPI) Reply(_ context.Context, _, _ int64, text string) error {
	f.replies = append(f.replies, text)
	return nil
}
func (f *fakeAPI) SendText(_ context.Context, _ int64, text string) error {
	f.texts = append(f.texts, text)
	return nil
}
func (f *fakeAPI) SendButtons(_ context.Context, chat int64, text string, b [][]notify.Button) (int64, error) {
	f.chats = append(f.chats, chat)
	f.buttons = append(f.buttons, text)
	f.btnData = append(f.btnData, b...)
	return 1, nil
}
func (f *fakeAPI) EditMessage(_ context.Context, _, _ int64, text string, b [][]notify.Button) error {
	f.edits = append(f.edits, text)
	f.editKeys = append(f.editKeys, b)
	return nil
}
func (f *fakeAPI) AnswerCallback(_ context.Context, _ string, text string) error {
	f.answers = append(f.answers, text)
	return nil
}

// fakeCore 模拟业务层。
type fakeCore struct {
	calls       []any
	link        core.LinkResult
	err         error
	actor       store.Account
	actorErr    error
	workspace   store.Workspace           // Workspace 与 WorkspaceByGroup 的默认结果
	groups      map[int64]store.Workspace // 群编号 → 工作空间；为空时一律返回 workspace
	memberships []store.Membership        // 申请人在册的工作空间
	byID        map[int64]store.Workspace // Workspace(id)；为空时返回 workspace
	posts       []store.BotPost
}

func (f *fakeCore) CompleteTelegramLink(_ context.Context, p string, uid int64, name string) (core.LinkResult, error) {
	f.calls = append(f.calls, "link", p, uid, name)
	return f.link, f.err
}
func (f *fakeCore) DecideSignup(_ context.Context, act core.Actor, ws, id int64, ok bool) (core.SignupDecision, error) {
	a := act.Account
	f.calls = append(f.calls, "signup", a.ID, ws, id, ok)
	d := core.SignupDecision{Signup: store.Signup{ID: id, Name: "ann", DisplayName: "Ann", Status: store.SignupRejected},
		Workspace: f.workspace, Decider: a.Name}
	if ok {
		d.Signup.Status = store.SignupApproved
		d.Member = &store.Member{WorkspaceID: f.workspace.ID, Name: "ann", DisplayName: "Ann", TelegramUsername: "ann_tg"}
	}
	return d, f.err
}
func (f *fakeCore) DecideDeviceAuth(_ context.Context, act core.Actor, ws, id int64, ok bool) (core.DeviceDecision, error) {
	a := act.Account
	f.calls = append(f.calls, "device", a.ID, ws, id, ok)
	st := store.AuthDenied
	if ok {
		st = store.AuthApproved
	}
	return core.DeviceDecision{Auth: store.DeviceAuth{ID: id, AccountName: "ann", DeviceName: "mac", Status: st},
		Workspaces: []string{"payments", "ops"}, Decider: a.Name}, f.err
}
func (f *fakeCore) Workspace(_ context.Context, id int64) (store.Workspace, error) {
	if w, ok := f.byID[id]; ok {
		return w, nil
	}
	return f.workspace, nil
}
func (f *fakeCore) WorkspaceByGroup(_ context.Context, chat int64) (store.Workspace, error) {
	if f.groups == nil {
		return f.workspace, nil
	}
	if w, ok := f.groups[chat]; ok {
		return w, nil
	}
	return store.Workspace{}, store.ErrNotFound
}
func (f *fakeCore) AccountByTelegram(_ context.Context, uid int64) (store.Account, error) {
	f.calls = append(f.calls, "who", uid)
	return f.actor, f.actorErr
}
func (f *fakeCore) Memberships(context.Context, int64) ([]store.Membership, error) {
	return f.memberships, nil
}
func (f *fakeCore) RecordBotPost(_ context.Context, p store.BotPost) error {
	f.posts = append(f.posts, p)
	return nil
}
func (f *fakeCore) BotPosts(_ context.Context, kind string, ref int64) ([]store.BotPost, error) {
	var out []store.BotPost
	for _, p := range f.posts {
		if p.Kind == kind && p.RefID == ref {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeCore) RecordDenied(context.Context, core.Actor, core.DeniedAttempt) error { return nil }

func newBot(c *fakeCore, api *fakeAPI) *notify.Bot {
	return &notify.Bot{Core: c, API: api, Name: "CohiveBot"}
}

func callbackIn(data string, from, chat int64) notify.Update {
	return notify.Update{UpdateID: 2, CallbackQuery: &notify.TGCallback{ID: "cb", Data: data,
		From:    notify.TGUser{ID: from, Username: "boss"},
		Message: &notify.TGMessage{MessageID: 77, Chat: notify.TGChat{ID: chat, Type: "supergroup"}}}}
}

func callback(data string, from int64) notify.Update { return callbackIn(data, from, -100) }

func TestStartLinksTelegram(t *testing.T) {
	c := &fakeCore{link: core.LinkResult{Account: "lee"}}
	api := &fakeAPI{}
	newBot(c, api).Handle(context.Background(), msg("/start ln_abc", "private", 42, "lee_ut"))
	if len(c.calls) != 4 || c.calls[1] != "ln_abc" || c.calls[2] != int64(42) || c.calls[3] != "lee_ut" {
		t.Fatalf("应以私聊发送人身份完成连接：%v", c.calls)
	}
	if len(api.replies) != 1 || !strings.Contains(api.replies[0], "已连接") {
		t.Fatalf("应回复连接结果：%v", api.replies)
	}

	for _, tc := range []struct {
		text, chat, want string
		err              error
	}{
		{"/start", "private", "个人中心", nil},
		{"/start ln_x", "private", "已失效", core.ErrLinkGone},
	} {
		api := &fakeAPI{}
		newBot(&fakeCore{err: tc.err}, api).Handle(context.Background(), msg(tc.text, tc.chat, 42, "lee"))
		if len(api.replies) != 1 || !strings.Contains(api.replies[0], tc.want) {
			t.Errorf("%q：回复应包含 %q，得到 %v", tc.text, tc.want, api.replies)
		}
	}
	api = &fakeAPI{}
	newBot(&fakeCore{}, api).Handle(context.Background(), msg("/start ln_x", "supergroup", -100, "lee"))
	if len(api.replies) != 0 {
		t.Fatalf("群里的 /start 应忽略：%v", api.replies)
	}
}

func TestAnnounceAndApprove(t *testing.T) {
	ctx := context.Background()
	ws := store.Workspace{ID: 3, Name: "payments", Notify: store.NotifyConfig{Channel: "telegram", Target: "-1001"}}
	c := &fakeCore{workspace: ws, groups: map[int64]store.Workspace{-1001: ws}, actor: store.Account{ID: 9, Name: "lee"}}
	api := &fakeAPI{}
	bot := newBot(c, api)
	if err := bot.AnnounceSignup(ctx, store.Signup{ID: 5, WorkspaceID: 3, Name: "ann", DisplayName: "<Ann>"}); err != nil {
		t.Fatal(err)
	}
	if len(api.chats) != 1 || api.chats[0] != -1001 || !strings.Contains(api.buttons[0], "&lt;Ann&gt;") || api.btnData[0][0].Data != "su:3:5:y" {
		t.Fatalf("应在群里发审批消息：%v %v %+v", api.chats, api.buttons, api.btnData)
	}
	if len(c.posts) != 1 || c.posts[0].ChatID != -1001 || c.posts[0].RefID != 5 {
		t.Fatalf("应记下审批消息：%+v", c.posts)
	}

	bot.Handle(ctx, callbackIn("su:3:5:y", 900, -1001))
	if c.calls[0] != "who" || c.calls[1] != int64(900) || c.calls[2] != "signup" || c.calls[4] != int64(3) || c.calls[5] != int64(5) || c.calls[6] != true {
		t.Fatalf("应按点击者找账号、按本群的工作空间审批：%v", c.calls)
	}
	if len(api.edits) != 1 || !strings.Contains(api.edits[0], "已批准") || !strings.Contains(api.edits[0], "lee") || api.editKeys[0] != nil {
		t.Fatalf("应把消息改成结果并去掉按钮：%v", api.edits)
	}
	if len(api.texts) != 1 || !strings.Contains(api.texts[0], "@ann_tg") || !strings.Contains(api.texts[0], "已加入") {
		t.Fatalf("批准后应在群里说一声并 @ 新成员：%v", api.texts)
	}

	// 没配置群时不发
	api2 := &fakeAPI{}
	if err := newBot(&fakeCore{workspace: store.Workspace{ID: 3}}, api2).AnnounceSignup(ctx, store.Signup{WorkspaceID: 3}); err != nil || len(api2.buttons) != 0 {
		t.Fatalf("没有群时不发：%v %v", err, api2.buttons)
	}
}

// 设备申请发到申请人在册所在、配置了群的每个工作空间群；任一处批准后各群的消息都改成结果。
func TestDeviceAnnouncedToAllGroups(t *testing.T) {
	ctx := context.Background()
	pay := store.Workspace{ID: 3, Name: "payments", Notify: store.NotifyConfig{Channel: "telegram", Target: "-1001"}}
	ops := store.Workspace{ID: 4, Name: "ops", Notify: store.NotifyConfig{Channel: "telegram", Target: "-1002"}}
	quiet := store.Workspace{ID: 5, Name: "quiet"}
	accID := int64(7)
	c := &fakeCore{
		byID:        map[int64]store.Workspace{3: pay, 4: ops, 5: quiet},
		groups:      map[int64]store.Workspace{-1001: pay, -1002: ops},
		memberships: []store.Membership{{WorkspaceID: 3, Workspace: "payments", Label: "payments"}, {WorkspaceID: 4, Workspace: "ops", Label: "ops"}, {WorkspaceID: 5, Workspace: "quiet", Label: "quiet"}},
		actor:       store.Account{ID: 9, Name: "boss"},
	}
	api := &fakeAPI{}
	bot := newBot(c, api)
	if err := bot.AnnounceDevice(ctx, store.DeviceAuth{ID: 6, AccountID: &accID, AccountName: "ann", DeviceName: "mac", Tools: []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	if len(api.chats) != 2 || api.chats[0] != -1001 || api.chats[1] != -1002 {
		t.Fatalf("应发到两个配置了群的工作空间：%v", api.chats)
	}
	if api.btnData[0][0].Data != "da:3:6:y" || api.btnData[1][1].Data != "da:4:6:n" {
		t.Fatalf("按钮应带各自群的工作空间：%+v", api.btnData)
	}
	if !strings.Contains(api.buttons[0], "payments、ops、quiet") {
		t.Fatalf("审批消息应写明批准后可用于他全部工作空间：%s", api.buttons[0])
	}
	bot.Handle(ctx, callbackIn("da:4:6:y", 900, -1002))
	if c.calls[2] != "device" || c.calls[4] != int64(4) {
		t.Fatalf("应按点击所在群的工作空间审批：%v", c.calls)
	}
	if len(api.edits) != 2 {
		t.Fatalf("两个群里的审批消息都应改成结果：%v", api.edits)
	}
}

func TestApproveCallbackRejected(t *testing.T) {
	ctx := context.Background()
	pay := store.Workspace{ID: 3, Name: "payments"}
	disabled := time.Now()
	for _, tc := range []struct {
		name string
		core *fakeCore
		data string
		chat int64
		want string
	}{
		{"未连接", &fakeCore{workspace: pay, actorErr: store.ErrNotFound}, "su:3:5:y", -100, "还没有连接"},
		{"已停用", &fakeCore{workspace: pay, actor: store.Account{ID: 1, DisabledAt: &disabled}}, "su:3:5:y", -100, "还没有连接"},
		{"非管理员", &fakeCore{workspace: pay, err: core.ErrNotAdmin}, "da:3:5:y", -100, "管理员"},
		{"管不到申请人", &fakeCore{workspace: pay, err: core.ErrNotManager}, "da:3:5:y", -100, "管理员"},
		{"已处理", &fakeCore{workspace: pay, err: core.ErrDecided}, "su:3:5:n", -100, "处理过"},
		{"按钮与本群不符", &fakeCore{workspace: pay}, "su:4:5:y", -100, "不符"},
		{"群没有绑定", &fakeCore{workspace: pay, groups: map[int64]store.Workspace{}}, "su:3:5:y", -100, "不符"},
		{"坏数据", &fakeCore{workspace: pay}, "su:x:5:y", -100, ""},
		{"旧按钮", &fakeCore{workspace: pay}, "join:5:y", -100, ""},
	} {
		api := &fakeAPI{}
		newBot(tc.core, api).Handle(ctx, callbackIn(tc.data, 501, tc.chat))
		if len(api.answers) != 1 || !strings.Contains(api.answers[0], tc.want) || len(api.edits) != 0 {
			t.Errorf("%s：应只回应点击 %q，得到 %v %v", tc.name, tc.want, api.answers, api.edits)
		}
		for _, c := range tc.core.calls {
			if c == "signup" || c == "device" {
				if tc.name == "按钮与本群不符" || tc.name == "群没有绑定" || tc.name == "未连接" || tc.name == "已停用" {
					t.Errorf("%s：不应调用审批：%v", tc.name, tc.core.calls)
				}
			}
		}
	}
}

// 有人加入工作空间后在群里打招呼并 @ 他。
func TestAnnounceJoined(t *testing.T) {
	ws := store.Workspace{ID: 3, Name: "payments", Notify: store.NotifyConfig{Channel: "telegram", Target: "-1001"}}
	api := &fakeAPI{}
	if err := newBot(&fakeCore{workspace: ws}, api).AnnounceJoined(context.Background(),
		store.Member{WorkspaceID: 3, Name: "ann", DisplayName: "Ann", TelegramUsername: "ann_tg"}); err != nil {
		t.Fatal(err)
	}
	if len(api.texts) != 1 || !strings.Contains(api.texts[0], "@ann_tg") || !strings.Contains(api.texts[0], "payments") {
		t.Fatalf("应在群里 @ 新成员：%v", api.texts)
	}
}

// 加入通知：连接了 Telegram 的按用户编号提到（链接文字为转义后的登录名与显示名），只有用户名的退回 @用户名，都没有只写名字。
func TestAnnounceJoinedMention(t *testing.T) {
	ws := store.Workspace{ID: 3, Name: "payments", Notify: store.NotifyConfig{Channel: "telegram", Target: "-1001"}}
	id := int64(4242)
	for _, tc := range []struct {
		name    string
		m       store.Member
		want    string
		notWant string
	}{
		{"有编号", store.Member{Name: "ann", DisplayName: `<A&"n>`, TelegramUsername: "ann_tg", TelegramUserID: &id},
			`<a href="tg://user?id=4242">ann（&lt;A&amp;&#34;n&gt;）</a> 已加入`, "@ann_tg"},
		{"只有用户名", store.Member{Name: "ann", DisplayName: "Ann", TelegramUsername: "ann_tg"}, "@ann_tg（ann（Ann）） 已加入", "tg://"},
		{"都没有", store.Member{Name: "ann", DisplayName: "Ann"}, "ann（Ann） 已加入", "@"},
	} {
		tc.m.WorkspaceID = 3
		api := &fakeAPI{}
		if err := newBot(&fakeCore{workspace: ws}, api).AnnounceJoined(context.Background(), tc.m); err != nil {
			t.Fatal(err)
		}
		if len(api.texts) != 1 || !strings.Contains(api.texts[0], tc.want) || strings.Contains(api.texts[0], tc.notWant) {
			t.Errorf("%s：期望含 %q、不含 %q，得到 %v", tc.name, tc.want, tc.notWant, api.texts)
		}
	}
}

// 机器人消息用显示名，并转义 HTML 特殊字符。
func TestBotUsesEscapedDisplayName(t *testing.T) {
	ctx := context.Background()
	ws := store.Workspace{ID: 3, Name: "payments", DisplayName: "<b>支付&组</b>", Notify: store.NotifyConfig{Channel: "telegram", Target: "-1001"}}
	api := &fakeAPI{}
	if err := newBot(&fakeCore{workspace: ws}, api).AnnounceSignup(ctx, store.Signup{ID: 5, WorkspaceID: 3, Name: "ann"}); err != nil {
		t.Fatal(err)
	}
	if len(api.buttons) != 1 || !strings.Contains(api.buttons[0], "&lt;b&gt;支付&amp;组&lt;/b&gt;") || strings.Contains(api.buttons[0], "payments") {
		t.Fatalf("应写转义后的显示名：%v", api.buttons)
	}
}
