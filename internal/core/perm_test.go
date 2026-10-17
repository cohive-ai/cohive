package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// people 是多工作空间的权限测试夹具：pay、ops 两个工作空间。
//   - payAdmin：pay 的管理员
//   - opsAdmin：ops 的管理员
//   - root：服务器管理员（不是任何工作空间的成员）
//   - ann：同时在 pay、ops 的普通成员
type people struct {
	e                         env
	pay, ops                  int64
	payAdmin, opsAdmin, root  store.Account
	ann                       store.Account
	annPay, annOps            store.Member
	payAdminMember, opsMember store.Member
}

func newPeople(t *testing.T) people {
	t.Helper()
	ctx := context.Background()
	e := newEnv(t)
	q := e.svc.Pool
	mkWS := func(name string) int64 {
		ws, err := store.CreateWorkspace(ctx, q, name)
		if err != nil {
			t.Fatal(err)
		}
		return ws.ID
	}
	p := people{e: e, pay: mkWS("pay"), ops: mkWS("ops")}
	mk := func(name string) store.Account {
		a, err := store.CreateAccount(ctx, q, name, "")
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	join := func(a store.Account, ws int64, role string) store.Member {
		m, err := store.AddMembership(ctx, q, a.ID, ws, role, nil)
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	p.payAdmin, p.opsAdmin, p.root, p.ann = mk("pay-admin"), mk("ops-admin"), mk("root"), mk("ann")
	p.payAdminMember = join(p.payAdmin, p.pay, store.RoleAdmin)
	p.opsMember = join(p.opsAdmin, p.ops, store.RoleAdmin)
	p.annPay = join(p.ann, p.pay, store.RoleMember)
	p.annOps = join(p.ann, p.ops, store.RoleMember)
	if err := store.SetServerAdmin(ctx, q, p.root.ID, true); err != nil {
		t.Fatal(err)
	}
	p.root.ServerAdmin = true
	return p
}

// pendingDevice 为 ann 造一条等待管理员批准的设备申请（她已有过一台设备）。
func (p people) pendingDevice(t *testing.T) int64 {
	t.Helper()
	ctx := context.Background()
	if _, err := store.CreateDevice(ctx, p.e.svc.Pool, p.ann.ID, "old"); err != nil {
		t.Fatal(err)
	}
	st, err := p.e.svc.StartDeviceAuth(ctx, []string{"codex"}, "new")
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.e.svc.AuthorizeDevice(ctx, p.ann, st.Nonce, true)
	if err != nil || a.Status != store.AuthAwaitingAdmin {
		t.Fatalf("应等管理员批准：%+v %v", a, err)
	}
	return a.ID
}

// 6.2 第 7 条：批准人资格按在册关系在批准这一刻判定。
func TestDeviceApproverMustManageApplicantNow(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	id := p.pendingDevice(t)

	// pay 的管理员在 ops 的语境下不能批（他不是 ops 的管理员）
	if _, err := svc.DecideDeviceAuth(ctx, core.WebActor(p.payAdmin), p.ops, id, true); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("别的工作空间的管理员不能批：%v", err)
	}
	// ann 被移出 pay 后，pay 的管理员不再能批
	if err := store.RemoveMembership(ctx, svc.Pool, p.annPay.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.DecideDeviceAuth(ctx, core.WebActor(p.payAdmin), p.pay, id, true); !errors.Is(err, core.ErrNotManager) {
		t.Fatalf("申请人已移出 pay，pay 的管理员不能批：%v", err)
	}
	if _, err := svc.DecideDeviceAuth(ctx, core.WebActor(p.payAdmin), 0, id, true); !errors.Is(err, core.ErrNotManager) {
		t.Fatalf("不带工作空间语境时也按在册关系判定：%v", err)
	}
	// ops 的管理员还能批
	if _, err := svc.DecideDeviceAuth(ctx, core.WebActor(p.opsAdmin), p.ops, id, true); err != nil {
		t.Fatalf("申请人仍在 ops，ops 的管理员可以批：%v", err)
	}
}

// 申请人一个在册工作空间都没有时，只有服务器管理员能批。
func TestDeviceWithoutMembershipsOnlyServerAdmin(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	id := p.pendingDevice(t)
	for _, m := range []store.Member{p.annPay, p.annOps} {
		if err := store.RemoveMembership(ctx, svc.Pool, m.ID, nil); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []store.Account{p.payAdmin, p.opsAdmin} {
		if _, err := svc.DecideDeviceAuth(ctx, core.WebActor(a), 0, id, true); !errors.Is(err, core.ErrNotManager) {
			t.Fatalf("%s 不应能批：%v", a.Name, err)
		}
	}
	if _, err := svc.DecideDeviceAuth(ctx, core.WebActor(p.root), 0, id, true); err != nil {
		t.Fatalf("服务器管理员可以批：%v", err)
	}
}

// 服务器管理员能管理任一工作空间（不必是成员）；已停用的管理员什么都不能做；普通成员不能管理。
func TestCanAdminRules(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	spec := core.InviteSpec{Kind: store.InvitePersonal}
	if _, err := svc.CreateInvite(ctx, core.WebActor(p.root), p.pay, spec); err != nil {
		t.Fatalf("服务器管理员可以给任一工作空间发邀请：%v", err)
	}
	if _, err := svc.CreateInvite(ctx, core.WebActor(p.payAdmin), p.pay, spec); err != nil {
		t.Fatalf("工作空间管理员可以给本空间发邀请：%v", err)
	}
	if _, err := svc.CreateInvite(ctx, core.WebActor(p.payAdmin), p.ops, spec); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("不能给别的工作空间发邀请：%v", err)
	}
	if _, err := svc.CreateInvite(ctx, core.WebActor(p.ann), p.pay, spec); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("普通成员不能发邀请：%v", err)
	}
	// 管理员被移出后，旧的账号快照不再有效
	if err := store.RemoveMembership(ctx, svc.Pool, p.payAdminMember.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateInvite(ctx, core.WebActor(p.payAdmin), p.pay, spec); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("移出后不再是管理员：%v", err)
	}
	// 服务器管理员被停用后，什么都不能做
	other, _ := store.CreateAccount(ctx, svc.Pool, "root2", "")
	if err := store.SetServerAdmin(ctx, svc.Pool, other.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := svc.DisableAccount(ctx, core.WebActor(p.root), other.ID); err != nil {
		t.Fatal(err)
	}
	other.ServerAdmin = true
	if _, err := svc.CreateInvite(ctx, core.WebActor(other), p.pay, spec); !errors.Is(err, store.ErrAccountDisabled) {
		t.Fatalf("已停用的服务器管理员不能做管理操作：%v", err)
	}
	if _, err := svc.ServerAccounts(ctx, &p.payAdmin, ""); !errors.Is(err, core.ErrNotServerAdmin) {
		t.Fatalf("工作空间管理员不能看服务器账号列表：%v", err)
	}
}

// 吊销设备：管理员只能吊销他管得到的人的设备，看不到的一律报不存在。
func TestRevokeDeviceScope(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	d, err := store.CreateDevice(ctx, svc.Pool, p.ann.ID, "mac")
	if err != nil {
		t.Fatal(err)
	}
	stranger, _ := store.CreateAccount(ctx, svc.Pool, "stranger", "")
	if err := svc.RevokeDevice(ctx, core.WebActor(stranger), 0, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("管不到的人不能吊销：%v", err)
	}
	if err := svc.RevokeDevice(ctx, core.WebActor(p.payAdmin), p.ops, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("在 ops 语境下 pay 的管理员看不到：%v", err)
	}
	if err := svc.RevokeDevice(ctx, core.WebActor(p.payAdmin), p.pay, d.ID); err != nil {
		t.Fatalf("pay 的管理员可以吊销 pay 成员的设备：%v", err)
	}
}

// 6.2 第 11 条：两名服务器管理员同时互相撤销，最终至少保留一名。
func TestConcurrentServerAdminRevoke(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	b, _ := store.CreateAccount(ctx, svc.Pool, "root-b", "")
	if err := svc.SetServerAdmin(ctx, core.WebActor(p.root), b.ID, true); err != nil {
		t.Fatal(err)
	}
	b.ServerAdmin = true
	hold, err := svc.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(ctx, `LOCK TABLE accounts IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, pair := range [][2]store.Account{{p.root, b}, {b, p.root}} {
		wg.Add(1)
		go func(actor, target store.Account) {
			defer wg.Done()
			_ = svc.SetServerAdmin(ctx, core.WebActor(actor), target.ID, false)
		}(pair[0], pair[1])
	}
	time.Sleep(300 * time.Millisecond)
	_ = hold.Rollback(ctx)
	wg.Wait()
	n, err := store.CountActiveServerAdmins(ctx, svc.Pool)
	if err != nil || n != 1 {
		t.Fatalf("互相撤销后应恰好剩 1 名服务器管理员：%d %v", n, err)
	}
	// 最后一名不能撤销自己
	var last store.Account
	for _, a := range []store.Account{p.root, b} {
		if x, _ := store.AccountByID(ctx, svc.Pool, a.ID); x.ServerAdmin {
			last = x
		}
	}
	var ie *core.InputError
	if err := svc.SetServerAdmin(ctx, core.WebActor(last), last.ID, false); !errors.As(err, &ie) {
		t.Fatalf("最后一名服务器管理员不能撤销：%v", err)
	}
}

// 第二道防线：成员资格被移出、账号被停用时，哪怕令牌没来得及吊销，也一律查不到。
func TestLookupTokenSecondLine(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	q := p.e.svc.Pool
	plainPay, _, err := store.IssueToken(ctx, q, p.annPay.ID, "codex")
	if err != nil {
		t.Fatal(err)
	}
	plainOps, _, err := store.IssueToken(ctx, q, p.annOps.ID, "codex")
	if err != nil {
		t.Fatal(err)
	}
	// 只改成员资格状态，不动令牌
	if _, err := q.Exec(ctx, `UPDATE members SET status = 'removed' WHERE id = $1`, p.annPay.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupToken(ctx, q, plainPay); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("已移出的成员资格的令牌应无效：%v", err)
	}
	if who, err := store.LookupToken(ctx, q, plainOps); err != nil || who.AccountID != p.ann.ID {
		t.Fatalf("另一个工作空间的令牌不受影响：%+v %v", who, err)
	}
	// 只标停用，不动令牌
	if _, err := q.Exec(ctx, `UPDATE accounts SET disabled_at = now() WHERE id = $1`, p.ann.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupToken(ctx, q, plainOps); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("已停用账号的令牌应无效：%v", err)
	}
}

// 停用的两道防线各自有用例（设计稿 6.2 第 9 条）：
// 第一道：停用时在同一事务里删掉会话、吊销设备；第二道：查会话、查设备密钥时再查一次账号未停用。

// 第一道防线：停用后直接查库，账号的网页会话与未用的一次性网页登录码都已删除。
func TestDisableDeletesSessionsInDB(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	if err := svc.ChangePassword(ctx, p.ann, "", "", goodPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "ann", goodPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateWebLoginCode(ctx, p.ann.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DisableAccount(ctx, core.WebActor(p.root), p.ann.ID); err != nil {
		t.Fatal(err)
	}
	var sessions, codes int
	if err := svc.Pool.QueryRow(ctx, `SELECT count(*) FROM web_sessions WHERE account_id = $1`, p.ann.ID).Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if err := svc.Pool.QueryRow(ctx, `SELECT count(*) FROM web_login_codes WHERE account_id = $1 AND used_at IS NULL`, p.ann.ID).Scan(&codes); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 || codes != 0 {
		t.Fatalf("停用后应删除全部会话与未用的网页登录码：会话 %d，登录码 %d", sessions, codes)
	}
}

// 第二道防线（会话）：只把账号标为停用、不删会话，会话也查不到。
func TestDisabledAccountSessionRejected(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	if err := svc.ChangePassword(ctx, p.ann, "", "", goodPassword); err != nil {
		t.Fatal(err)
	}
	sess, err := svc.Login(ctx, "ann", goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Pool.Exec(ctx, `UPDATE accounts SET disabled_at = now() WHERE id = $1`, p.ann.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.SessionAccount(ctx, sess.ID); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("已停用账号的会话应查不到：%v", err)
	}
}

// 第二道防线（设备密钥）：只把账号标为停用、不吊销设备，设备密钥也查不到。
func TestDisabledAccountDeviceSecretRejected(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	d := pair(t, svc, p.ann, "codex")
	if _, err := svc.Pool.Exec(ctx, `UPDATE accounts SET disabled_at = now() WHERE id = $1`, p.ann.ID); err != nil {
		t.Fatal(err)
	}
	var live int
	if err := svc.Pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE account_id = $1 AND revoked_at IS NULL`, p.ann.ID).Scan(&live); err != nil || live != 1 {
		t.Fatalf("前提：设备未吊销：%d %v", live, err)
	}
	if _, _, err := svc.AuthenticateDevice(ctx, d.DeviceSecret); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("已停用账号的设备密钥应查不到：%v", err)
	}
}
