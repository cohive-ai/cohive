package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

func TestTelegramLink(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	l, err := e.svc.StartLink(ctx, e.a.AccountID, core.ProviderTelegram)
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.CompleteTelegramLink(ctx, l.Nonce, 8001, "lee_ut")
	if err != nil || res.Account != "lee-claude" {
		t.Fatalf("连接：%+v %v", res, err)
	}
	m, _ := store.MemberByID(ctx, e.svc.Pool, e.a.MemberID)
	if m.TelegramUserID == nil || *m.TelegramUserID != 8001 || m.TelegramUsername != "lee_ut" {
		t.Fatalf("应绑定编号与用户名：%+v", m)
	}
	if st, _ := e.svc.LinkStatus(ctx, e.a.AccountID, l.ID); st.Status != store.LinkLinked {
		t.Fatalf("状态：%+v", st)
	}
	if _, err := e.svc.CompleteTelegramLink(ctx, l.Nonce, 8001, "lee_ut"); !errors.Is(err, core.ErrLinkGone) {
		t.Fatalf("链接只能用一次：%v", err)
	}
	if _, err := e.svc.LinkStatus(ctx, e.b.AccountID, l.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("别人看不到我的连接请求：%v", err)
	}

	// 同一个 Telegram 账号全服务器只能连接一个 Cohive 账号
	l2, _ := e.svc.StartLink(ctx, e.b.AccountID, core.ProviderTelegram)
	var ie *core.InputError
	if _, err := e.svc.CompleteTelegramLink(ctx, l2.Nonce, 8001, "lee_ut"); !errors.As(err, &ie) {
		t.Fatalf("重复连接应拒绝：%v", err)
	}
	if st, _ := e.svc.LinkStatus(ctx, e.b.AccountID, l2.ID); st.Status != store.LinkFailed || st.Detail == "" {
		t.Fatalf("失败原因应记下：%+v", st)
	}

	// 解除：本人可以；工作空间管理员也不能解除别人的（只有服务器管理员能）
	if err := store.SetMemberRole(ctx, e.svc.Pool, e.b.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	other := acct(t, e, e.b)
	if err := e.svc.UnlinkTelegram(ctx, other, e.a.AccountID); !errors.Is(err, core.ErrNotServerAdmin) {
		t.Fatalf("工作空间管理员不能解除别人的连接：%v", err)
	}
	if err := e.svc.UnlinkTelegram(ctx, acct(t, e, e.a), e.a.AccountID); err != nil {
		t.Fatal(err)
	}
	if m, _ := store.MemberByID(ctx, e.svc.Pool, e.a.MemberID); m.TelegramUserID != nil || m.TelegramUsername != "" {
		t.Fatalf("应已解除：%+v", m)
	}
	if _, err := e.svc.StartLink(ctx, e.a.AccountID, "slack"); !errors.As(err, &ie) {
		t.Fatalf("暂不支持的工具：%v", err)
	}
}
