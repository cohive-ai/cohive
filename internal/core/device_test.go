package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// pair 为账号走完一次首台设备的授权与领取，返回交付结果。
func pair(t *testing.T, svc *core.Service, a store.Account, tools ...string) core.DeviceDelivery {
	t.Helper()
	ctx := context.Background()
	st, err := svc.StartDeviceAuth(ctx, tools, "mac")
	if err != nil {
		t.Fatal(err)
	}
	auth, err := svc.AuthorizeDevice(ctx, a, st.Nonce, true)
	if err != nil || auth.Status != store.AuthApproved {
		t.Fatalf("首台应免审：%+v %v", auth, err)
	}
	p, err := svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret)
	if err != nil || p.Status != store.AuthApproved {
		t.Fatalf("领取：%+v %v", p, err)
	}
	return p
}

// 交付时为本人每个在册工作空间 × 每个工具签发令牌；设备密钥能为新加入的工作空间签发令牌。
func TestDeliveryCoversAllWorkspaces(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	d := pair(t, svc, p.ann, "codex", "claude-code")
	if len(d.Workspaces) != 2 || tokenOf(d, "pay", "codex") == "" || tokenOf(d, "ops", "claude-code") == "" {
		t.Fatalf("应交付 pay、ops 两个工作空间的令牌：%+v", d)
	}
	for _, ws := range []string{"pay", "ops"} {
		who, err := store.LookupToken(ctx, svc.Pool, tokenOf(d, ws, "codex"))
		if err != nil || who.AccountID != p.ann.ID || who.DeviceID == nil || *who.DeviceID != d.DeviceID {
			t.Fatalf("%s 的令牌应属于 ann 的这台设备：%+v %v", ws, who, err)
		}
	}
	// 新加入一个工作空间：不必重新配对，用设备密钥签发
	dev, _, err := svc.AuthenticateDevice(ctx, d.DeviceSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IssueWorkspaceTokens(ctx, dev, "default", []string{"codex"}); !errors.Is(err, core.ErrNotMember) {
		t.Fatalf("还不是 default 的成员：%v", err)
	}
	ws, _ := store.WorkspaceByName(ctx, svc.Pool, "default")
	if _, err := svc.AddMember(ctx, core.WebActor(p.root), ws.ID, "ann", ""); err != nil {
		t.Fatal(err)
	}
	wt, err := svc.IssueWorkspaceTokens(ctx, dev, "default", []string{"codex"})
	if err != nil || wt.Tokens["codex"] == "" || wt.Role != store.RoleMember {
		t.Fatalf("加入后可签发：%+v %v", wt, err)
	}
}

// 6.2 第 5 条：首台判定是账号级的：在任何工作空间有过设备（含已吊销、已批准未领取），就不算首台。
func TestFirstDeviceIsAccountWide(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	d := pair(t, svc, p.ann, "codex")
	if err := svc.RevokeDevice(ctx, core.WebActor(p.ann), 0, d.DeviceID); err != nil {
		t.Fatal(err)
	}
	// 被移出 pay 也不影响：设备属于人
	if err := store.RemoveMembership(ctx, svc.Pool, p.annPay.ID, nil); err != nil {
		t.Fatal(err)
	}
	st, _ := svc.StartDeviceAuth(ctx, []string{"codex"}, "b")
	if v, _ := svc.ViewDeviceAuth(ctx, p.ann, st.Nonce); v.FirstDevice || len(v.Workspaces) != 1 || v.Workspaces[0] != "ops" {
		t.Fatalf("有过设备就不是首台；授权页列出在册的工作空间：%+v", v)
	}
	if a, err := svc.AuthorizeDevice(ctx, p.ann, st.Nonce, true); err != nil || a.Status != store.AuthAwaitingAdmin {
		t.Fatalf("应等管理员：%+v %v", a, err)
	}

	// 已批准未领取的首台也算有过：再发起第二台需要审批
	zoe, _ := store.CreateAccount(ctx, svc.Pool, "zoe", "")
	st1, _ := svc.StartDeviceAuth(ctx, []string{"codex"}, "z1")
	if a, err := svc.AuthorizeDevice(ctx, zoe, st1.Nonce, true); err != nil || a.Status != store.AuthApproved {
		t.Fatalf("zoe 首台：%+v %v", a, err)
	}
	st2, _ := svc.StartDeviceAuth(ctx, []string{"codex"}, "z2")
	if a, err := svc.AuthorizeDevice(ctx, zoe, st2.Nonce, true); err != nil || a.Status != store.AuthAwaitingAdmin {
		t.Fatalf("首台已批准未领取时，第二台也需审批：%+v %v", a, err)
	}
}

// 6.2 第 6 条（业务层部分）：移出后该工作空间的令牌立即失效、设备密钥签不出它的令牌；
// 别的工作空间不受影响；加回后可以重新签发（转发器的自动换新见命令行测试）。
func TestRemoveThenReAdd(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	d := pair(t, svc, p.ann, "codex")
	dev, _, _ := svc.AuthenticateDevice(ctx, d.DeviceSecret)
	// 服务器管理员不是 pay 的成员：转交要记操作人，提示先把自己加进去
	if err := svc.ApplyHandover(ctx, core.WebActor(p.root), p.pay, p.annPay.ID, core.HandoverPlan{}, true); !isInput(err) {
		t.Fatalf("非成员的服务器管理员不能在网页上移出：%v", err)
	}
	if err := svc.ApplyHandover(ctx, core.WebActor(p.payAdmin), p.pay, p.annPay.ID, core.HandoverPlan{}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupToken(ctx, svc.Pool, tokenOf(d, "pay", "codex")); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("移出后 pay 的令牌应失效：%v", err)
	}
	if _, err := store.LookupToken(ctx, svc.Pool, tokenOf(d, "ops", "codex")); err != nil {
		t.Fatalf("ops 的令牌不受影响：%v", err)
	}
	// 第一道防线：移出时在同一事务里把令牌标为吊销（不只靠查询时的在册条件）
	var live int
	if err := svc.Pool.QueryRow(ctx, `SELECT count(*) FROM tokens WHERE member_id = $1 AND revoked_at IS NULL`, p.annPay.ID).Scan(&live); err != nil || live != 0 {
		t.Fatalf("移出后该成员资格不应有未吊销的令牌：%d %v", live, err)
	}
	if _, err := svc.IssueWorkspaceTokens(ctx, dev, "pay", []string{"codex"}); !errors.Is(err, core.ErrNotMember) {
		t.Fatalf("移出后设备密钥签不出 pay 的令牌：%v", err)
	}
	m, err := svc.AddMember(ctx, core.WebActor(p.payAdmin), p.pay, "ann", "")
	if err != nil || m.ID != p.annPay.ID {
		t.Fatalf("加回应复用原成员资格：%+v %v", m, err)
	}
	wt, err := svc.IssueWorkspaceTokens(ctx, dev, "pay", []string{"codex"})
	if err != nil || wt.Tokens["codex"] == "" {
		t.Fatalf("加回后可重新签发：%+v %v", wt, err)
	}
	if _, err := store.LookupToken(ctx, svc.Pool, tokenOf(d, "pay", "codex")); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("移出前的旧令牌仍应无效：%v", err)
	}
}

func isInput(err error) bool {
	var ie *core.InputError
	return errors.As(err, &ie)
}
