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
}

func (f *fakeAPI) Reply(_ context.Context, _, _ int64, text string) error {
	f.replies = append(f.replies, text)
	return nil
}
func (f *fakeAPI) SendText(_ context.Context, _ int64, text string) error {
	f.texts = append(f.texts, text)
	return nil
}
func (f *fakeAPI) SendButtons(_ context.Context, _ int64, text string, b [][]notify.Button) (int64, error) {
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
	fakeLogins
	join      core.JoinResult
	joinErr   error
	gotChat   string
	decision  core.JoinDecision
	decideErr error
	decided   []any
}

func (f *fakeCore) RequestJoin(_ context.Context, chat string, uid int64, username, display string) (core.JoinResult, error) {
	f.gotChat = chat
	return f.join, f.joinErr
}
func (f *fakeCore) DecideJoin(_ context.Context, id, uid int64, approve bool) (core.JoinDecision, error) {
	f.decided = append(f.decided, id, uid, approve)
	return f.decision, f.decideErr
}

func newBot(c *fakeCore, api *fakeAPI) *notify.Bot {
	return &notify.Bot{Core: c, API: api, Name: "CohiveBot"}
}

func callback(data string, from int64) notify.Update {
	return notify.Update{UpdateID: 2, CallbackQuery: &notify.TGCallback{ID: "cb", Data: data,
		From:    notify.TGUser{ID: from, Username: "boss"},
		Message: &notify.TGMessage{MessageID: 77, Chat: notify.TGChat{ID: -100, Type: "supergroup"}}}}
}

func TestJoinCreatesApprovalMessage(t *testing.T) {
	c := &fakeCore{join: core.JoinResult{Workspace: store.Workspace{Name: "payments"},
		Request: &store.JoinRequest{ID: 7, TelegramUsername: "ann_dev", DisplayName: "Ann"}}}
	api := &fakeAPI{}
	newBot(c, api).Handle(context.Background(), msg("/join", "supergroup", -1001234, "ann_dev"))
	if c.gotChat != "-1001234" {
		t.Fatalf("应按群编号查工作空间：%q", c.gotChat)
	}
	if len(api.buttons) != 1 || !strings.Contains(api.buttons[0], "payments") || !strings.Contains(api.buttons[0], "@ann_dev") {
		t.Fatalf("应在群里发审批消息：%v", api.buttons)
	}
	if api.btnData[0][0].Data != "join:7:y" || api.btnData[0][1].Data != "join:7:n" {
		t.Fatalf("按钮数据不符：%+v", api.btnData)
	}
}

func TestJoinOtherOutcomes(t *testing.T) {
	cases := []struct {
		name string
		core *fakeCore
		chat string
		want string
	}{
		{"已是成员", &fakeCore{join: core.JoinResult{Workspace: store.Workspace{Name: "payments"}, AlreadyMember: &store.Member{Name: "ann"}}}, "supergroup", "已经是"},
		{"直接加入", &fakeCore{join: core.JoinResult{Workspace: store.Workspace{Name: "payments"}, Member: &store.Member{Name: "ann"}}}, "supergroup", "cohive login"},
		{"群没配置", &fakeCore{joinErr: core.ErrNoWorkspaceForChat}, "supergroup", "/chatid"},
		{"私聊里发", &fakeCore{}, "private", "群里"},
	}
	for _, c := range cases {
		api := &fakeAPI{}
		newBot(c.core, api).Handle(context.Background(), msg("/join", c.chat, -1001234, "ann"))
		if len(api.replies) != 1 || !strings.Contains(api.replies[0], c.want) {
			t.Errorf("%s：回复应包含 %q，得到 %v", c.name, c.want, api.replies)
		}
	}
}

func TestApproveCallback(t *testing.T) {
	c := &fakeCore{decision: core.JoinDecision{Workspace: store.Workspace{Name: "payments"},
		Request: store.JoinRequest{ID: 7, TelegramUsername: "ann_dev", DisplayName: "Ann", Status: "approved"},
		Member:  &store.Member{Name: "ann_dev"}, Decider: store.Member{Name: "lee"}}}
	api := &fakeAPI{}
	newBot(c, api).Handle(context.Background(), callback("join:7:y", 900))
	if len(c.decided) != 3 || c.decided[0] != int64(7) || c.decided[1] != int64(900) || c.decided[2] != true {
		t.Fatalf("应以点击者身份批准 7 号申请：%v", c.decided)
	}
	if len(api.answers) != 1 || len(api.edits) != 1 || !strings.Contains(api.edits[0], "lee") || !strings.Contains(api.edits[0], "批准") || api.editKeys[0] != nil {
		t.Fatalf("应回应点击并把审批消息改成结果、去掉按钮：%v %v", api.answers, api.edits)
	}
	if len(api.texts) != 1 || !strings.Contains(api.texts[0], "@ann_dev") || !strings.Contains(api.texts[0], "cohive login") {
		t.Fatalf("应在群里告诉新成员下一步：%v", api.texts)
	}
}

func TestApproveCallbackNotAdmin(t *testing.T) {
	c := &fakeCore{decideErr: core.ErrNotAdmin}
	api := &fakeAPI{}
	newBot(c, api).Handle(context.Background(), callback("join:7:y", 501))
	if len(api.answers) != 1 || !strings.Contains(api.answers[0], "管理员") || len(api.edits) != 0 {
		t.Fatalf("非管理员点击只提示，不改消息：%v %v", api.answers, api.edits)
	}
}

func TestBadCallbackData(t *testing.T) {
	c := &fakeCore{}
	api := &fakeAPI{}
	newBot(c, api).Handle(context.Background(), callback("join:abc:y", 900))
	if len(c.decided) != 0 || len(api.answers) != 1 {
		t.Fatalf("无法识别的按钮数据只回应点击：%v %v", c.decided, api.answers)
	}
}
