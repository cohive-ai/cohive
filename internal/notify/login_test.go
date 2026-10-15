package notify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

type fakeLogins struct {
	res        core.LoginCodeResult
	err        error
	gotUser    int64
	gotWS      string
	calledWith int
}

func (f *fakeLogins) IssueLoginCode(_ context.Context, uid int64, ws string) (core.LoginCodeResult, error) {
	f.calledWith++
	f.gotUser, f.gotWS = uid, ws
	return f.res, f.err
}

func TestLoginOnlyInPrivateChat(t *testing.T) {
	logins := &fakeLogins{}
	reply, ok := notify.HandleLogin(context.Background(), msg("/login", "supergroup", -100, "lee"), "CohiveBot", logins)
	if !ok || !strings.Contains(reply, "私聊") || logins.calledWith != 0 {
		t.Fatalf("群里不应签发登录码，只提示私聊：%q（签发调用 %d 次）", reply, logins.calledWith)
	}
}

func TestLoginIssuesCode(t *testing.T) {
	logins := &fakeLogins{res: core.LoginCodeResult{Code: "ABCD-EFGH",
		Member:    store.Member{Name: "lee-codex"},
		Workspace: store.Workspace{Name: "default", PublicURL: "https://cohive.example.com"}}}
	reply, ok := notify.HandleLogin(context.Background(), msg("/login", "private", 42, "lee"), "CohiveBot", logins)
	if !ok || logins.gotUser != 42 {
		t.Fatalf("应按发送者的用户编号签发：%v %d", ok, logins.gotUser)
	}
	for _, want := range []string{"ABCD-EFGH", "10 分钟", "cohive login --server https://cohive.example.com --code ABCD-EFGH", "cohive init"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("回复应包含 %q：\n%s", want, reply)
		}
	}
}

func TestLoginWorkspaceChoice(t *testing.T) {
	logins := &fakeLogins{res: core.LoginCodeResult{Choices: []string{"default", "payments"}}}
	reply, _ := notify.HandleLogin(context.Background(), msg("/login", "private", 42, "lee"), "CohiveBot", logins)
	if !strings.Contains(reply, "/login default") || !strings.Contains(reply, "/login payments") {
		t.Fatalf("应列出可选的工作空间：%s", reply)
	}
	notify.HandleLogin(context.Background(), msg("/login payments", "private", 42, "lee"), "CohiveBot", logins)
	if logins.gotWS != "payments" {
		t.Fatalf("应把工作空间名传给签发：%q", logins.gotWS)
	}
}

func TestLoginNotBound(t *testing.T) {
	logins := &fakeLogins{err: core.ErrNotBound}
	reply, _ := notify.HandleLogin(context.Background(), msg("/login", "private", 42, "lee"), "CohiveBot", logins)
	if !strings.Contains(reply, "还没有绑定") || !strings.Contains(reply, "--telegram-id 42") {
		t.Fatalf("未绑定时应给出管理员绑定命令：%s", reply)
	}
}

func TestLoginIgnoresOtherBots(t *testing.T) {
	if _, ok := notify.HandleLogin(context.Background(), msg("/login@OtherBot", "private", 42, "lee"), "CohiveBot", &fakeLogins{}); ok {
		t.Fatal("发给别的机器人的命令不应处理")
	}
}
