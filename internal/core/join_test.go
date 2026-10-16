package core_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// 已有账号凭个人邀请加入另一个工作空间：直接成为成员（角色按邀请）；已是成员时不消耗邀请。
func TestAcceptPersonalInvite(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	stranger, _ := store.CreateAccount(ctx, svc.Pool, "zoe", "Zoe")
	inv, err := svc.CreateInvite(ctx, &p.payAdmin, p.pay, core.InviteSpec{Kind: store.InvitePersonal, Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	info, err := svc.LookupInvite(ctx, inv.Code, &stranger)
	if err != nil || !info.Valid || info.AlreadyMember || info.Role != store.RoleAdmin || info.Workspace != "pay" {
		t.Fatalf("邀请信息：%+v %v", info, err)
	}
	// ann 已是 pay 的成员：提示已是成员，不消耗邀请
	if _, err := svc.AcceptInvite(ctx, p.ann, inv.Code); !errors.Is(err, store.ErrAlreadyMember) {
		t.Fatalf("已是成员应拒绝：%v", err)
	}
	if info, _ := svc.LookupInvite(ctx, inv.Code, &p.ann); !info.AlreadyMember || !info.Valid {
		t.Fatalf("已是成员的人看到 already_member，且邀请仍可用：%+v", info)
	}
	res, err := svc.AcceptInvite(ctx, stranger, inv.Code)
	if err != nil || res.Member == nil || res.Member.Role != store.RoleAdmin || res.Workspace.Name != "pay" {
		t.Fatalf("接受个人邀请：%+v %v", res, err)
	}
	if _, err := svc.AcceptInvite(ctx, p.opsAdmin, inv.Code); !errors.Is(err, core.ErrInviteInvalid) {
		t.Fatalf("个人邀请只能用一次：%v", err)
	}
	ms, _ := svc.Memberships(ctx, stranger.ID)
	if len(ms) != 1 || ms[0].Workspace != "pay" {
		t.Fatalf("应加入 pay：%+v", ms)
	}
}

// 已有账号凭团队邀请申请加入：生成加入申请，管理员批准后成为成员；不新建账号。
func TestAcceptTeamInviteCreatesJoinRequest(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	inv, err := svc.CreateInvite(ctx, &p.opsAdmin, p.ops, core.InviteSpec{Kind: store.InviteTeam, MaxUses: 5})
	if err != nil {
		t.Fatal(err)
	}
	// 已是 ops 成员的人接受 ops 的团队邀请：提示已是成员，不生成申请、不消耗次数
	if _, err := svc.AcceptInvite(ctx, p.ann, inv.Code); !errors.Is(err, store.ErrAlreadyMember) {
		t.Fatalf("已是成员应拒绝：%v", err)
	}
	if list, _ := svc.PendingSignups(ctx, &p.opsAdmin, p.ops); len(list) != 0 {
		t.Fatalf("不应为已是成员的人生成申请：%+v", list)
	}
	res, err := svc.AcceptInvite(ctx, p.payAdmin, inv.Code)
	if err != nil || res.Signup == nil || !res.Signup.Existing || res.Signup.Name != "pay-admin" || res.Member != nil {
		t.Fatalf("应生成加入申请：%+v %v", res, err)
	}
	if _, err := svc.AcceptInvite(ctx, p.payAdmin, inv.Code); !errors.Is(err, core.ErrJoinPending) {
		t.Fatalf("重复申请应提示已在等批准：%v", err)
	}
	list, err := svc.PendingSignups(ctx, &p.opsAdmin, p.ops)
	if err != nil || len(list) != 1 || !list[0].Existing {
		t.Fatalf("待审列表应标出已有账号：%+v %v", list, err)
	}
	// 别的工作空间的管理员不能批
	if _, err := svc.DecideSignup(ctx, &p.payAdmin, p.ops, res.Signup.ID, true); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("申请人自己（pay 的管理员）不能批 ops 的申请：%v", err)
	}
	// 在 pay 的语境下批 ops 的申请：申请不属于 pay，报不存在
	if _, err := svc.DecideSignup(ctx, &p.payAdmin, p.pay, res.Signup.ID, true); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("申请不属于 pay：%v", err)
	}
	d, err := svc.DecideSignup(ctx, &p.opsAdmin, p.ops, res.Signup.ID, true)
	if err != nil || d.Member == nil || d.Account == nil || d.Account.ID != p.payAdmin.ID {
		t.Fatalf("批准加入申请：%+v %v", d, err)
	}
	var n int
	if err := svc.Pool.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE name = 'pay-admin'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("不应新建账号：%d %v", n, err)
	}
	if ok, _ := store.IsActiveMemberOf(ctx, svc.Pool, p.payAdmin.ID, p.ops); !ok {
		t.Fatal("批准后应是 ops 的成员")
	}
}

// 停用的账号不能接受邀请，也不能被批准加入。
func TestDisabledAccountCannotJoin(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	zoe, _ := store.CreateAccount(ctx, svc.Pool, "zoe", "")
	team, _ := svc.CreateInvite(ctx, &p.opsAdmin, p.ops, core.InviteSpec{Kind: store.InviteTeam})
	res, err := svc.AcceptInvite(ctx, zoe, team.Code)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.DisableAccount(ctx, &p.root, zoe.ID); err != nil {
		t.Fatal(err)
	}
	var ie *core.InputError
	if _, err := svc.DecideSignup(ctx, &p.opsAdmin, p.ops, res.Signup.ID, true); !errors.As(err, &ie) {
		t.Fatalf("停用的账号不能被批准加入：%v", err)
	}
	personal, _ := svc.CreateInvite(ctx, &p.opsAdmin, p.ops, core.InviteSpec{Kind: store.InvitePersonal})
	if _, err := svc.AcceptInvite(ctx, zoe, personal.Code); !errors.Is(err, store.ErrAccountDisabled) {
		t.Fatalf("停用的账号不能接受邀请：%v", err)
	}
}

// 6.2 第 10 条：登录名全服务器唯一，包括别的工作空间的新人待审申请。
func TestGlobalLoginNames(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	payTeam, _ := svc.CreateInvite(ctx, &p.payAdmin, p.pay, core.InviteSpec{Kind: store.InviteTeam})
	opsTeam, _ := svc.CreateInvite(ctx, &p.opsAdmin, p.ops, core.InviteSpec{Kind: store.InviteTeam})
	if _, err := svc.Register(ctx, core.RegisterInput{Code: payTeam.Code, Name: "ann", Password: goodPassword}); err == nil ||
		!strings.Contains(err.Error(), "已被使用") {
		t.Fatalf("与已有账号同名应拒绝：%v", err)
	}
	if _, err := svc.Register(ctx, core.RegisterInput{Code: payTeam.Code, Name: "dora", Password: goodPassword}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Register(ctx, core.RegisterInput{Code: opsTeam.Code, Name: "dora", Password: goodPassword}); err == nil ||
		!strings.Contains(err.Error(), "已有人申请") {
		t.Fatalf("别的工作空间里同名的待审申请也占位：%v", err)
	}
}

// 两个工作空间的团队邀请并发注册同一个名字：只有一个成功。
func TestConcurrentSameNameAcrossWorkspaces(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	codes := []string{}
	for _, x := range []struct {
		a  store.Account
		ws int64
	}{{p.payAdmin, p.pay}, {p.opsAdmin, p.ops}} {
		inv, err := svc.CreateInvite(ctx, &x.a, x.ws, core.InviteSpec{Kind: store.InviteTeam})
		if err != nil {
			t.Fatal(err)
		}
		codes = append(codes, inv.Code)
	}
	hold, err := svc.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(ctx, `LOCK TABLE signups IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, c := range codes {
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			_, err := svc.Register(ctx, core.RegisterInput{Code: code, Name: "dup", Password: goodPassword})
			errs <- err
		}(c)
	}
	time.Sleep(1500 * time.Millisecond)
	_ = hold.Rollback(ctx)
	wg.Wait()
	close(errs)
	ok, taken := 0, 0
	var ie *core.InputError
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.As(err, &ie):
			taken++
		default:
			t.Fatalf("不应返回内部错误：%v", err)
		}
	}
	if ok != 1 || taken != 1 {
		t.Fatalf("应一个成功、一个提示重名：成功 %d，重名 %d", ok, taken)
	}
}

// 登录不再选工作空间：密码正确但账号已停用时才提示已停用，其余一律“登录名或密码不对”。
func TestLoginDisabledOnlyWithRightPassword(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	if err := svc.ChangePassword(ctx, p.ann, "", "", goodPassword); err != nil {
		t.Fatal(err)
	}
	if s, err := svc.Login(ctx, "ann", goodPassword); err != nil || s.Account.ID != p.ann.ID {
		t.Fatalf("登录：%v", err)
	}
	if err := svc.DisableAccount(ctx, &p.root, p.ann.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "ann", "wrong-password!"); !errors.Is(err, core.ErrBadLogin) {
		t.Fatalf("密码错误时不暴露停用：%v", err)
	}
	if _, err := svc.Login(ctx, "ann", goodPassword); !errors.Is(err, store.ErrAccountDisabled) {
		t.Fatalf("密码正确时提示已停用：%v", err)
	}
}
