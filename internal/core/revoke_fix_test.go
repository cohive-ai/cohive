package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// 首台批准后、终端领取前吊销设备：终端不应再领到令牌。
func TestRevokedBeforeDelivery(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	me := acct(t, e, e.a)
	st, _ := e.svc.StartDeviceAuth(ctx, []string{"codex"}, "a")
	a, err := e.svc.AuthorizeDevice(ctx, me, st.Nonce, true)
	if err != nil || a.DeviceID == nil {
		t.Fatalf("授权：%+v %v", a, err)
	}
	if err := e.svc.RevokeDevice(ctx, core.WebActor(me), 0, *a.DeviceID); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret)
	if err != nil || p.Status != store.AuthDenied || p.Workspaces != nil || p.DeviceSecret != "" {
		t.Fatalf("设备已吊销，不应交付令牌：%+v %v", p, err)
	}
	var n int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM tokens WHERE device_id = $1`, *a.DeviceID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("不应为已吊销设备签发任何令牌：%d %v", n, err)
	}
}

// 令牌所属设备已吊销（哪怕令牌本身没来得及标记吊销）：令牌一律无效；也不能在该设备上补签。
func TestTokenOfRevokedDeviceRejected(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	me := acct(t, e, e.a)
	st, _ := e.svc.StartDeviceAuth(ctx, []string{"codex"}, "a")
	if _, err := e.svc.AuthorizeDevice(ctx, me, st.Nonce, true); err != nil {
		t.Fatal(err)
	}
	p, _ := e.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret)
	if _, err := store.LookupToken(ctx, e.svc.Pool, tokenOf(p, "default", "codex")); err != nil {
		t.Fatal(err)
	}
	dev, _, err := e.svc.AuthenticateDevice(ctx, p.DeviceSecret)
	if err != nil {
		t.Fatal(err)
	}
	// 只标记设备吊销，不动令牌，模拟与签发并发时漏掉的令牌
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE devices SET revoked_at = now() WHERE id = $1`, p.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupToken(ctx, e.svc.Pool, tokenOf(p, "default", "codex")); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("设备已吊销的令牌应无效：%v", err)
	}
	if _, err := e.svc.IssueWorkspaceTokens(ctx, dev, "default", []string{"claude-code"}); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("已吊销的设备不能补签：%v", err)
	}
}

// 已批准但过期未领取：批准时建的设备应被吊销，不留孤儿设备。
func TestApprovedButExpiredRevokesDevice(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	me := acct(t, e, e.a)
	st, _ := e.svc.StartDeviceAuth(ctx, []string{"codex"}, "a")
	a, err := e.svc.AuthorizeDevice(ctx, me, st.Nonce, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE device_auths SET expires_at = now() - interval '1 second' WHERE id = $1`, st.RequestID); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret); p.Status != store.AuthExpired || p.Workspaces != nil {
		t.Fatalf("过期：%+v", p)
	}
	var revoked bool
	if err := e.svc.Pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM devices WHERE id = $1`, *a.DeviceID).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("过期未领取的设备应吊销：%v %v", revoked, err)
	}
}
