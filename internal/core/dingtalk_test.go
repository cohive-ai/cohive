package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// 钉钉账号连接：单聊发码 → 待确认 → 本人在网页确认才生效；取消作废；码只能用一次；钉钉账号已连别人时拒绝。
func TestDingTalkLinkNeedsWebConfirm(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	lee := core.ChatSender{Tenant: "corp1", UserID: "u-ann", Name: "安"}

	l, err := svc.StartLink(ctx, p.ann.ID, core.ProviderDingTalk)
	if err != nil || !core.LooksLikeCode(l.Nonce) {
		t.Fatalf("应生成 8 位绑定码：%+v %v", l, err)
	}
	res, err := svc.SubmitLinkCode(ctx, core.ProviderDingTalk, strings.ToLower(l.Nonce[:4])+"-"+l.Nonce[4:], lee, true)
	if err != nil || res.Account != "ann" {
		t.Fatalf("单聊发码（大小写、连字符不敏感）：%+v %v", res, err)
	}
	if _, err := store.ChatIdentityOf(ctx, svc.Pool, p.ann.ID, store.ProviderDingTalk); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("网页确认前不应生效")
	}
	st, _ := svc.LinkStatus(ctx, p.ann.ID, l.ID)
	if st.Status != store.LinkAwaitingConfirm || st.CandidateName != "安" {
		t.Fatalf("待确认，网页显示钉钉昵称：%+v", st)
	}
	if _, err := svc.SubmitLinkCode(ctx, core.ProviderDingTalk, l.Nonce, core.ChatSender{Tenant: "corp1", UserID: "u-evil"}, true); !errors.Is(err, core.ErrLinkCodeGone) {
		t.Fatalf("码只能用一次，第二个人发同一个码无效：%v", err)
	}
	if err := svc.ConfirmLink(ctx, core.WebActor(p.payAdmin), l.ID, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("别人不能确认我的连接：%v", err)
	}
	if err := svc.ConfirmLink(ctx, core.WebActor(p.ann), l.ID, true); err != nil {
		t.Fatal(err)
	}
	if c, err := store.ChatIdentityOf(ctx, svc.Pool, p.ann.ID, store.ProviderDingTalk); err != nil || c.UserID != "u-ann" || c.Tenant != "corp1" || c.DisplayName != "安" {
		t.Fatalf("确认后连接：%+v %v", c, err)
	}
	if err := svc.ConfirmLink(ctx, core.WebActor(p.ann), l.ID, true); err == nil {
		t.Fatal("确认只能一次")
	}

	// 同一钉钉账号想再连到别的 Cohive 账号：发码时就拒绝
	l2, _ := svc.StartLink(ctx, p.payAdmin.ID, core.ProviderDingTalk)
	var ie *core.InputError
	if _, err := svc.SubmitLinkCode(ctx, core.ProviderDingTalk, l2.Nonce, lee, true); !errors.As(err, &ie) || !strings.Contains(err.Error(), "ann") {
		t.Fatalf("钉钉账号已连接别人：%v", err)
	}
	// 取消
	l3, _ := svc.StartLink(ctx, p.opsAdmin.ID, core.ProviderDingTalk)
	if _, err := svc.SubmitLinkCode(ctx, core.ProviderDingTalk, l3.Nonce, core.ChatSender{Tenant: "corp1", UserID: "u-ops"}, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.ConfirmLink(ctx, core.WebActor(p.opsAdmin), l3.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ChatIdentityOf(ctx, svc.Pool, p.opsAdmin.ID, store.ProviderDingTalk); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("取消后不应连接")
	}
}

// 账号绑定码出现在群里：立即作废，之后私聊发也无效；已收到、等确认的码再出现在群里也作废。
func TestDingTalkLinkCodeLeakedInGroup(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	l, _ := svc.StartLink(ctx, p.ann.ID, core.ProviderDingTalk)
	if _, err := svc.SubmitLinkCode(ctx, core.ProviderDingTalk, l.Nonce, core.ChatSender{Tenant: "corp1", UserID: "u-x"}, false); !errors.Is(err, core.ErrLinkCodeLeaked) {
		t.Fatalf("群里出现的码：%v", err)
	}
	if _, err := svc.SubmitLinkCode(ctx, core.ProviderDingTalk, l.Nonce, core.ChatSender{Tenant: "corp1", UserID: "u-ann"}, true); !errors.Is(err, core.ErrLinkCodeGone) {
		t.Fatalf("作废后私聊发也无效：%v", err)
	}
	l2, _ := svc.StartLink(ctx, p.ann.ID, core.ProviderDingTalk)
	if _, err := svc.SubmitLinkCode(ctx, core.ProviderDingTalk, l2.Nonce, core.ChatSender{Tenant: "corp1", UserID: "u-ann"}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitLinkCode(ctx, core.ProviderDingTalk, l2.Nonce, core.ChatSender{Tenant: "corp1", UserID: "u-x"}, false); !errors.Is(err, core.ErrLinkCodeLeaked) {
		t.Fatalf("等确认的码出现在群里：%v", err)
	}
	if err := svc.ConfirmLink(ctx, core.WebActor(p.ann), l2.ID, true); err == nil {
		t.Fatal("作废后不能再确认")
	}
	// Telegram 的连接参数不能当钉钉绑定码用
	tl, _ := svc.StartLink(ctx, p.ann.ID, core.ProviderTelegram)
	if _, err := svc.SubmitLinkCode(ctx, core.ProviderDingTalk, tl.Nonce, core.ChatSender{Tenant: "corp1", UserID: "u-ann"}, true); !errors.Is(err, core.ErrLinkCodeGone) {
		t.Fatalf("不同通讯工具的码不通用：%v", err)
	}
}

// 群绑定：只有工作空间管理员能生成码；码一次性；一个群只能绑一个工作空间；工作空间已绑 Telegram 群时要求先解绑。
func TestDingTalkChatBind(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	if _, err := svc.StartChatBind(ctx, core.WebActor(p.ann), p.pay, core.ProviderDingTalk); err == nil {
		t.Fatal("普通成员不能生成群绑定码")
	}
	b, err := svc.StartChatBind(ctx, core.WebActor(p.payAdmin), p.pay, core.ProviderDingTalk)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CompleteChatBind(ctx, core.ProviderDingTalk, "cid-pay", "支付群", "WRONG234"); !errors.Is(err, core.ErrBindCodeGone) {
		t.Fatalf("错的码：%v", err)
	}
	label, err := svc.CompleteChatBind(ctx, core.ProviderDingTalk, "cid-pay", "支付群", strings.ToLower(b.Code))
	if err != nil || label != "pay" {
		t.Fatalf("绑定：%q %v", label, err)
	}
	if ws, err := svc.WorkspaceByChat(ctx, store.ProviderDingTalk, "", "cid-pay"); err != nil || ws.ID != p.pay {
		t.Fatalf("由群反查工作空间：%+v %v", ws, err)
	}
	if st, _ := svc.ChatBindStatus(ctx, core.WebActor(p.payAdmin), p.pay, b.ID); st.Status != store.BindBound || st.Detail != "支付群" {
		t.Fatalf("网页看到已绑定：%+v", st)
	}
	if _, err := svc.CompleteChatBind(ctx, core.ProviderDingTalk, "cid-other", "x", b.Code); !errors.Is(err, core.ErrBindCodeGone) {
		t.Fatalf("码只能用一次：%v", err)
	}
	// 同一个群不能再绑到 ops
	b2, _ := svc.StartChatBind(ctx, core.WebActor(p.opsAdmin), p.ops, core.ProviderDingTalk)
	var se *core.StateError
	if _, err := svc.CompleteChatBind(ctx, core.ProviderDingTalk, "cid-pay", "支付群", b2.Code); !errors.As(err, &se) || !strings.Contains(err.Error(), "pay") {
		t.Fatalf("一个群只能绑一个工作空间：%v", err)
	}
	// ops 已绑 Telegram 群：生成码时就要求先解绑
	if err := svc.UpdateWorkspace(ctx, core.WebActor(p.opsAdmin), p.ops, core.WorkspaceSettings{TelegramChat: "-1001"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartChatBind(ctx, core.WebActor(p.opsAdmin), p.ops, core.ProviderDingTalk); !errors.As(err, &se) {
		t.Fatalf("已绑 Telegram 群时先解绑：%v", err)
	}
	// 解绑
	if err := svc.UnbindChat(ctx, core.WebActor(p.ann), p.pay, core.ProviderDingTalk); err == nil {
		t.Fatal("普通成员不能解绑")
	}
	if err := svc.UnbindChat(ctx, core.WebActor(p.payAdmin), p.pay, core.ProviderDingTalk); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.WorkspaceByChat(ctx, store.ProviderDingTalk, "", "cid-pay"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("解绑后不再反查到")
	}
}

// 绑定码过期后无效（账号绑定码、群绑定码都一样）。
func TestDingTalkCodesExpire(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	l, _ := svc.StartLink(ctx, p.ann.ID, core.ProviderDingTalk)
	b, _ := svc.StartChatBind(ctx, core.WebActor(p.payAdmin), p.pay, core.ProviderDingTalk)
	if _, err := svc.Pool.Exec(ctx, `UPDATE link_requests SET expires_at = now() - interval '1 second' WHERE id = $1`, l.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pool.Exec(ctx, `UPDATE chat_bind_requests SET expires_at = now() - interval '1 second' WHERE id = $1`, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitLinkCode(ctx, core.ProviderDingTalk, l.Nonce, core.ChatSender{Tenant: "corp1", UserID: "u-ann"}, true); !errors.Is(err, core.ErrLinkCodeGone) {
		t.Fatalf("过期的账号绑定码：%v", err)
	}
	if _, err := svc.CompleteChatBind(ctx, core.ProviderDingTalk, "cid-pay", "支付群", b.Code); !errors.Is(err, core.ErrBindCodeGone) {
		t.Fatalf("过期的群绑定码：%v", err)
	}
	if st, _ := svc.ChatBindStatus(ctx, core.WebActor(p.payAdmin), p.pay, b.ID); st.Status != store.LinkExpired {
		t.Fatalf("网页上显示已过期：%+v", st)
	}
}
