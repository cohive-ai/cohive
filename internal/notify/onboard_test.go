package notify_test

import (
	"context"
	"strings"
	"testing"

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
	calls     []any
	link      core.LinkResult
	err       error
	actor     store.Member
	actorErr  error
	workspace store.Workspace
}

func (f *fakeCore) CompleteTelegramLink(_ context.Context, p string, uid int64, name string) (core.LinkResult, error) {
	f.calls = append(f.calls, "link", p, uid, name)
	return f.link, f.err
}
func (f *fakeCore) DecideSignup(_ context.Context, a *store.Member, id int64, ok bool) (core.SignupDecision, error) {
	f.calls = append(f.calls, "signup", a.ID, id, ok)
	return core.SignupDecision{Signup: store.Signup{Name: "ann", DisplayName: "Ann"}, Workspace: f.workspace, Decider: a.Name}, f.err
}
func (f *fakeCore) DecideDeviceAuth(_ context.Context, a *store.Member, id int64, ok bool) (core.DeviceDecision, error) {
	f.calls = append(f.calls, "device", a.ID, id, ok)
	return core.DeviceDecision{Auth: store.DeviceAuth{MemberName: "ann", DeviceName: "mac"}, Workspace: f.workspace, Decider: a.Name}, f.err
}
func (f *fakeCore) Workspace(context.Context, int64) (store.Workspace, error) {
	return f.workspace, nil
}
func (f *fakeCore) MemberByTelegram(_ context.Context, ws, uid int64) (store.Member, error) {
	f.calls = append(f.calls, "who", ws, uid)
	return f.actor, f.actorErr
}

func newBot(c *fakeCore, api *fakeAPI) *notify.Bot {
	return &notify.Bot{Core: c, API: api, Name: "CohiveBot"}
}

func callback(data string, from int64) notify.Update {
	return notify.Update{UpdateID: 2, CallbackQuery: &notify.TGCallback{ID: "cb", Data: data,
		From:    notify.TGUser{ID: from, Username: "boss"},
		Message: &notify.TGMessage{MessageID: 77, Chat: notify.TGChat{ID: -100, Type: "supergroup"}}}}
}

func TestStartLinksTelegram(t *testing.T) {
	c := &fakeCore{link: core.LinkResult{Member: "lee", Workspace: "default"}}
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
	c := &fakeCore{workspace: ws, actor: store.Member{ID: 9, Name: "lee", Role: store.RoleAdmin, WorkspaceID: 3}}
	api := &fakeAPI{}
	bot := newBot(c, api)
	if err := bot.AnnounceSignup(ctx, store.Signup{ID: 5, WorkspaceID: 3, Name: "ann", DisplayName: "<Ann>"}); err != nil {
		t.Fatal(err)
	}
	if len(api.chats) != 1 || api.chats[0] != -1001 || !strings.Contains(api.buttons[0], "&lt;Ann&gt;") || api.btnData[0][0].Data != "su:3:5:y" {
		t.Fatalf("应在群里发审批消息：%v %v %+v", api.chats, api.buttons, api.btnData)
	}
	wsID := int64(3)
	if err := bot.AnnounceDevice(ctx, store.DeviceAuth{ID: 6, WorkspaceID: &wsID, MemberName: "ann", DeviceName: "mac", Tools: []string{"codex"}}); err != nil {
		t.Fatal(err)
	}
	if api.btnData[1][1].Data != "da:3:6:n" {
		t.Fatalf("设备审批按钮：%+v", api.btnData)
	}

	bot.Handle(ctx, callback("su:3:5:y", 900))
	if c.calls[0] != "who" || c.calls[1] != int64(3) || c.calls[2] != int64(900) || c.calls[3] != "signup" || c.calls[5] != int64(5) || c.calls[6] != true {
		t.Fatalf("应按点击者找成员再审批：%v", c.calls)
	}
	if len(api.edits) != 1 || !strings.Contains(api.edits[0], "已批准") || !strings.Contains(api.edits[0], "lee") || api.editKeys[0] != nil {
		t.Fatalf("应把消息改成结果并去掉按钮：%v", api.edits)
	}

	// 没配置群时不发
	api2 := &fakeAPI{}
	if err := newBot(&fakeCore{workspace: store.Workspace{ID: 3}}, api2).AnnounceSignup(ctx, store.Signup{WorkspaceID: 3}); err != nil || len(api2.buttons) != 0 {
		t.Fatalf("没有群时不发：%v %v", err, api2.buttons)
	}
}

func TestApproveCallbackRejected(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		core *fakeCore
		data string
		want string
	}{
		{"未连接", &fakeCore{actorErr: store.ErrNotFound}, "su:3:5:y", "还没有连接"},
		{"非管理员", &fakeCore{err: core.ErrNotAdmin}, "da:3:5:y", "管理员"},
		{"已处理", &fakeCore{err: core.ErrDecided}, "su:3:5:n", "处理过"},
		{"坏数据", &fakeCore{}, "su:x:5:y", ""},
		{"旧按钮", &fakeCore{}, "join:5:y", ""},
	} {
		api := &fakeAPI{}
		newBot(tc.core, api).Handle(ctx, callback(tc.data, 501))
		if len(api.answers) != 1 || !strings.Contains(api.answers[0], tc.want) || len(api.edits) != 0 {
			t.Errorf("%s：应只回应点击 %q，得到 %v %v", tc.name, tc.want, api.answers, api.edits)
		}
	}
}
