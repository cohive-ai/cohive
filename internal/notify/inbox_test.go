package notify_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

// 与平台无关的收件处理：用一个假想的新通讯工具（provider 为 example）验证，接新平台时只需翻译消息、写文案。

type fakeCodes struct {
	calls   []string
	linkErr error // 私聊发码的结果
	grpErr  error // 群里出现账号绑定码时的结果
	bindErr error
	onBind  func() // 绑定成功时调用（模拟群已绑到工作空间）
}

func (f *fakeCodes) SubmitLinkCode(_ context.Context, provider, code string, who core.ChatSender, private bool) (core.LinkSubmit, error) {
	f.calls = append(f.calls, fmt.Sprintf("link|%s|%s|%s|%s|%s|%v", provider, code, who.Tenant, who.UserID, who.Name, private))
	if !private {
		return core.LinkSubmit{}, f.grpErr
	}
	return core.LinkSubmit{Account: "lee"}, f.linkErr
}

func (f *fakeCodes) CompleteChatBind(_ context.Context, provider, chatID, title, code string) (string, error) {
	f.calls = append(f.calls, strings.Join([]string{"bind", provider, chatID, title, code}, "|"))
	if f.bindErr == nil && f.onBind != nil {
		f.onBind()
	}
	return "支付", f.bindErr
}

var allCaps = notify.InboxCaps{LinkByCode: true, BindByCode: true, Help: true}

func exampleIn(private bool, cmd, first string, args ...string) notify.Inbound {
	return notify.Inbound{Provider: "example", ChatID: "-1001", ChatTitle: "运维群", Private: private,
		From: notify.ChatUser{Provider: "example", Tenant: "t1", UserID: "42", Name: "李"}, Cmd: cmd, First: first, Args: args}
}

func TestInboxLinkCodeInPrivateChat(t *testing.T) {
	ctx := context.Background()
	codes := &fakeCodes{}
	x := notify.Inbox{Codes: codes, Caps: allCaps, Lang: i18n.ZhCN}
	for _, in := range []notify.Inbound{exampleIn(true, "bind", "bind", "abcd-2345"), exampleIn(true, "abcd2345", "ABCD2345")} {
		r := x.Handle(ctx, in)
		if r.Kind != notify.ReplyLinkReceived || r.Account != "lee" || !r.Private {
			t.Fatalf("私聊发码应交给业务层并等网页确认：%+v", r)
		}
	}
	want := "link|example|abcd-2345|t1|42|李|true\nlink|example|ABCD2345|t1|42|李|true"
	if got := strings.Join(codes.calls, "\n"); got != want {
		t.Fatalf("应把码连同平台填写的发送者交给业务层：\n%s", got)
	}

	// 业务错误给人看，内部错误只记日志
	var logged []string
	codes = &fakeCodes{linkErr: core.ErrLinkCodeGone}
	x = notify.Inbox{Codes: codes, Caps: allCaps, Lang: i18n.ZhCN, Logf: func(f string, a ...any) { logged = append(logged, fmt.Sprintf(f, a...)) }}
	if r := x.Handle(ctx, exampleIn(true, "abcd2345", "ABCD2345")); r.Kind != notify.ReplyError || r.Err != i18n.Message(i18n.ZhCN, core.ErrLinkCodeGone) {
		t.Fatalf("码已失效应如实告知：%+v", r)
	}
	codes.linkErr = errors.New("db down")
	if r := x.Handle(ctx, exampleIn(true, "abcd2345", "ABCD2345")); r.Kind != notify.ReplyTryLater || len(logged) != 1 || !strings.Contains(logged[0], "db down") {
		t.Fatalf("内部错误应记日志并请对方稍后再试：%+v %q", r, logged)
	}
}

func TestInboxCodeInGroup(t *testing.T) {
	ctx := context.Background()
	// 账号绑定码出现在群里：作废，不去绑群，回复不复述码
	codes := &fakeCodes{grpErr: core.ErrLinkCodeLeaked}
	r := notify.Inbox{Codes: codes, Caps: allCaps, Lang: i18n.ZhCN}.Handle(ctx, exampleIn(false, "abcd2345", "ABCD2345"))
	if r.Kind != notify.ReplyError || !strings.Contains(r.Err, "已作废") || strings.Contains(r.Err, "ABCD2345") || len(codes.calls) != 1 {
		t.Fatalf("群里的账号绑定码应作废：%+v %v", r, codes.calls)
	}
	// 不是账号绑定码：当作少写了“绑定群”的群绑定码
	codes = &fakeCodes{grpErr: core.ErrLinkCodeGone}
	r = notify.Inbox{Codes: codes, Caps: allCaps, Lang: i18n.ZhCN}.Handle(ctx, exampleIn(false, "abcd2345", "ABCD2345"))
	if r.Kind != notify.ReplyBound || r.Workspace != "支付" || codes.calls[1] != "bind|example|-1001|运维群|ABCD2345" {
		t.Fatalf("应改按群绑定码处理：%+v %v", r, codes.calls)
	}
	// 两样都不是：回用法
	codes = &fakeCodes{grpErr: core.ErrLinkCodeGone, bindErr: core.ErrBindCodeGone}
	if r = (notify.Inbox{Codes: codes, Caps: allCaps}).Handle(ctx, exampleIn(false, "abcd2345", "ABCD2345")); r.Kind != notify.ReplyHelp || r.Private {
		t.Fatalf("认不出的码回群里的用法：%+v", r)
	}
}

func TestInboxBindGroupCommand(t *testing.T) {
	ctx := context.Background()
	enWS := store.Workspace{ID: 3, Name: "pay", Language: "en"}
	// 绑定前群没绑工作空间（服务器缺省语言中文），绑定成功后按工作空间的语言（英文）回复
	fc := &fakeCore{groups: map[int64]store.Workspace{}}
	codes := &fakeCodes{onBind: func() { fc.groups[-1001] = enWS }}
	x := notify.Inbox{Hub: &notify.Hub{Core: fc}, Codes: codes, Caps: allCaps}
	bind := func() notify.Reply { return x.Handle(ctx, exampleIn(false, "绑定群", "绑定群", "GOODCODE")) }
	r := bind()
	if r.Kind != notify.ReplyBound || r.Workspace != "支付" || codes.calls[0] != "bind|example|-1001|运维群|GOODCODE" {
		t.Fatalf("绑定群：%+v %v", r, codes.calls)
	}
	if r.Lang != i18n.EN {
		t.Fatalf("绑定成功后应按工作空间的语言回复：%+v", r)
	}
	codes.bindErr = core.ErrBindCodeGone
	if r = bind(); r.Kind != notify.ReplyError || r.Err != i18n.Message(i18n.EN, core.ErrBindCodeGone) {
		t.Fatalf("群绑定码失效：%+v", r)
	}
	// 私聊里发“绑定群”不绑
	codes.calls = nil
	if r = x.Handle(ctx, exampleIn(true, "bindgroup", "bindgroup", "GOODCODE")); r.Kind != notify.ReplyHelp || len(codes.calls) != 0 {
		t.Fatalf("私聊里不能绑群：%+v %v", r, codes.calls)
	}
}

// 能力开关：不用码的通讯工具（Telegram）不碰业务层的码，认不出的消息不回复。
func TestInboxCapsOff(t *testing.T) {
	ctx := context.Background()
	codes := &fakeCodes{}
	x := notify.Inbox{Codes: codes, Lang: i18n.ZhCN}
	for _, in := range []notify.Inbound{exampleIn(true, "abcd2345", "ABCD2345"), exampleIn(true, "bind", "bind", "ABCD2345"),
		exampleIn(false, "abcd2345", "ABCD2345"), exampleIn(false, "bindgroup", "bindgroup", "GOODCODE"), exampleIn(false, "hello", "hello")} {
		if r := x.Handle(ctx, in); r.Kind != notify.ReplyNone {
			t.Fatalf("%+v 应不回复：%+v", in, r)
		}
	}
	if len(codes.calls) != 0 {
		t.Fatalf("不应调用业务层：%v", codes.calls)
	}
	// 只开群绑定：群里的码直接按群绑定码处理，不去查账号绑定码
	x.Caps = notify.InboxCaps{BindByCode: true}
	if r := x.Handle(ctx, exampleIn(false, "abcd2345", "ABCD2345")); r.Kind != notify.ReplyBound || len(codes.calls) != 1 || !strings.HasPrefix(codes.calls[0], "bind|") {
		t.Fatalf("只开群绑定：%+v %v", r, codes.calls)
	}
	if r := x.Handle(ctx, exampleIn(true, "abcd2345", "ABCD2345")); r.Kind != notify.ReplyNone {
		t.Fatalf("没开私聊发码：%+v", r)
	}
	// 没有业务层：码一律不处理
	x = notify.Inbox{Caps: allCaps, Lang: i18n.ZhCN}
	if r := x.Handle(ctx, exampleIn(true, "abcd2345", "ABCD2345")); r.Kind != notify.ReplyHelp || !r.Private {
		t.Fatalf("没有业务层时私聊回用法：%+v", r)
	}
}

func TestInboxWhoamiAndChatID(t *testing.T) {
	ctx := context.Background()
	ws := store.Workspace{ID: 3, Name: "pay", DisplayName: "支付", Language: "en"}
	fc := &fakeCore{groups: map[int64]store.Workspace{-1001: ws}, actor: store.Account{ID: 9, Name: "lee", Language: "zh-CN"},
		memberships: []store.Membership{{WorkspaceID: 3, Role: store.RoleAdmin}}}
	x := notify.Inbox{Hub: &notify.Hub{Core: fc}, Caps: allCaps}

	r := x.Handle(ctx, exampleIn(false, "whoami", "whoami"))
	if r.Kind != notify.ReplyWhoami || !r.Checked || !r.Linked || r.Account != "lee" || r.Workspace != "支付" || r.Role != "admin" || r.Lang != i18n.EN {
		t.Fatalf("群里的我是谁：账号、本群工作空间与角色，用群的语言：%+v", r)
	}
	r = x.Handle(ctx, exampleIn(true, "我是谁", "我是谁"))
	if r.Kind != notify.ReplyWhoami || r.Account != "lee" || r.Workspace != "" || r.Lang != i18n.ZhCN {
		t.Fatalf("私聊的我是谁：只有账号，用账号的语言：%+v", r)
	}
	fc.actorErr = store.ErrNotFound
	if r = x.Handle(ctx, exampleIn(true, "whoami", "whoami")); r.Kind != notify.ReplyWhoami || !r.Checked || r.Linked {
		t.Fatalf("没连接：%+v", r)
	}
	if r = (notify.Inbox{Caps: allCaps}).Handle(ctx, exampleIn(false, "whoami", "whoami")); r.Kind != notify.ReplyWhoami || r.Checked {
		t.Fatalf("没有业务层时不查账号：%+v", r)
	}

	if r = x.Handle(ctx, exampleIn(false, "群编号", "群编号")); r.Kind != notify.ReplyChatID || r.Private {
		t.Fatalf("群编号：%+v", r)
	}
	if r = x.Handle(ctx, exampleIn(true, "chatid", "chatid")); r.Kind != notify.ReplyChatID || !r.Private {
		t.Fatalf("私聊里问群编号：%+v", r)
	}
}
