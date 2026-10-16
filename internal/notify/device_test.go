package notify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

var readyView = core.DeviceView{ID: 5, DeviceName: "lee<Mac>", Tools: []string{"claude-code", "codex"},
	Workspace: "payments", MemberName: "lee"}

func TestStartDeviceLoginShowsConfirm(t *testing.T) {
	c := &fakeCore{device: readyView}
	api := &fakeAPI{}
	newBot(c, api).Handle(context.Background(), msg("/start dl_abc", "private", 42, "lee"))
	if len(c.decided) != 3 || c.decided[1] != "dl_abc" || c.decided[2] != int64(42) {
		t.Fatalf("应以私聊发送人身份打开登录请求：%v", c.decided)
	}
	if len(api.buttons) != 1 || !strings.Contains(api.buttons[0], "lee&lt;Mac&gt;") || !strings.Contains(api.buttons[0], "只确认你自己") {
		t.Fatalf("应发出带转义设备名的确认消息：%v", api.buttons)
	}
	if len(api.btnData) != 1 || len(api.btnData[0]) != 2 || api.btnData[0][0].Data != "dl:5:y" || api.btnData[0][1].Data != "dlx:5" {
		t.Fatalf("按钮应是确认登录和取消：%+v", api.btnData)
	}
}

func TestStartDeviceLoginChooseWorkspace(t *testing.T) {
	c := &fakeCore{device: core.DeviceView{ID: 5, DeviceName: "mac", Tools: []string{"codex"},
		Choices: []store.BoundMember{{Member: store.Member{Name: "lee", WorkspaceID: 3}, WorkspaceName: "payments"}}}}
	api := &fakeAPI{}
	newBot(c, api).Handle(context.Background(), msg("/start dl_abc", "private", 42, "lee"))
	if len(api.btnData) != 2 || api.btnData[0][0].Data != "dlw:5:3" || !strings.Contains(api.btnData[0][0].Text, "payments") {
		t.Fatalf("应先让用户选工作空间：%+v", api.btnData)
	}
}

func TestStartOther(t *testing.T) {
	cases := []struct {
		name, text, chat, want string
		err                    error
	}{
		{"无参数", "/start", "private", "cohive login", nil},
		{"未加入", "/start dl_x", "private", "/join", core.ErrNotBound},
		{"已失效", "/start dl_x", "private", "已失效", core.ErrDeviceGone},
		{"被别人打开", "/start dl_x", "private", "作废", core.ErrDeviceTaken},
	}
	for _, c := range cases {
		api := &fakeAPI{}
		newBot(&fakeCore{deviceErr: c.err}, api).Handle(context.Background(), msg(c.text, c.chat, 42, "lee"))
		if len(api.replies) != 1 || !strings.Contains(api.replies[0], c.want) {
			t.Errorf("%s：回复应包含 %q，得到 %v", c.name, c.want, api.replies)
		}
	}
	// 群里的 /start 不处理
	api := &fakeAPI{}
	newBot(&fakeCore{}, api).Handle(context.Background(), msg("/start dl_x", "supergroup", -100, "lee"))
	if len(api.replies)+len(api.buttons) != 0 {
		t.Fatalf("群里的 /start 应忽略：%v %v", api.replies, api.buttons)
	}
}

func TestDeviceCallbacks(t *testing.T) {
	ctx := context.Background()

	c := &fakeCore{device: readyView}
	api := &fakeAPI{}
	newBot(c, api).Handle(ctx, callback("dl:5:y", 42))
	if c.decided[0] != "confirm" || c.decided[1] != int64(5) || c.decided[2] != int64(42) {
		t.Fatalf("应以点击者身份确认：%v", c.decided)
	}
	if len(api.edits) != 1 || !strings.Contains(api.edits[0], "已确认") || api.editKeys[0] != nil || api.answers[0] != "已确认" {
		t.Fatalf("确认后应改消息、去掉按钮：%v %v", api.edits, api.answers)
	}

	c = &fakeCore{deviceErr: core.ErrDeviceTaken}
	api = &fakeAPI{}
	newBot(c, api).Handle(ctx, callback("dl:5:y", 42))
	if len(api.edits) != 1 || !strings.Contains(api.edits[0], "作废") || api.editKeys[0] != nil {
		t.Fatalf("失效时应改消息说明已作废：%v", api.edits)
	}

	c = &fakeCore{device: readyView}
	api = &fakeAPI{}
	newBot(c, api).Handle(ctx, callback("dlw:5:3", 42))
	if c.decided[0] != "choose" || c.decided[3] != int64(3) || len(api.editKeys) != 1 || len(api.editKeys[0]) != 1 || api.editKeys[0][0][0].Data != "dl:5:y" {
		t.Fatalf("选工作空间后应换成确认按钮：%v %+v", c.decided, api.editKeys)
	}

	c = &fakeCore{}
	api = &fakeAPI{}
	newBot(c, api).Handle(ctx, callback("dlx:5", 42))
	if c.decided[0] != "cancel" || len(api.edits) != 1 || !strings.Contains(api.edits[0], "已取消") {
		t.Fatalf("取消：%v %v", c.decided, api.edits)
	}

	c = &fakeCore{}
	api = &fakeAPI{}
	newBot(c, api).Handle(ctx, callback("dl:x:1", 42))
	if len(c.decided) != 0 || len(api.answers) != 1 {
		t.Fatalf("无法识别的按钮只回应点击：%v", c.decided)
	}
}

func TestBotUsername(t *testing.T) {
	if (&notify.Bot{Name: "CohiveBot"}).Username() != "CohiveBot" {
		t.Fatal("配置了用户名时直接返回")
	}
}
