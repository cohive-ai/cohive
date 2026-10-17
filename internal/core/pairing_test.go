package core_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

const goodPassword = "correct-horse-battery"

// acct 返回身份所属的账号。
func acct(t *testing.T, e env, who store.Identity) store.Account {
	t.Helper()
	a, err := store.AccountByID(context.Background(), e.svc.Pool, who.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// adminOf 把 b 设为默认工作空间的管理员，返回他的账号。
func adminOf(t *testing.T, e env) store.Account {
	t.Helper()
	if err := store.SetMemberRole(context.Background(), e.svc.Pool, e.b.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	return acct(t, e, e.b)
}

func invite(t *testing.T, e env, admin store.Account, spec core.InviteSpec) string {
	t.Helper()
	inv, err := e.svc.CreateInvite(context.Background(), core.WebActor(admin), e.b.WorkspaceID, spec)
	if err != nil {
		t.Fatal(err)
	}
	return inv.Code
}

// tokenOf 取出交付结果里某工作空间某工具的令牌。
func tokenOf(p core.DeviceDelivery, ws, tool string) string {
	for _, w := range p.Workspaces {
		if w.Workspace == ws {
			return w.Tokens[tool]
		}
	}
	return ""
}

func TestPersonalInviteRegister(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	admin := adminOf(t, e)
	code := invite(t, e, admin, core.InviteSpec{Kind: store.InvitePersonal, Admin: true})

	info, err := e.svc.LookupInvite(ctx, code, nil)
	if err != nil || !info.Valid || info.Workspace != "default" || info.Kind != store.InvitePersonal {
		t.Fatalf("邀请信息不符：%+v %v", info, err)
	}
	if _, err := e.svc.Register(ctx, core.RegisterInput{Code: code, Name: "lee-codex", Password: goodPassword}); err == nil ||
		!strings.Contains(err.Error(), "已被使用") {
		t.Fatalf("登录名重复应拒绝：%v", err)
	}
	if _, err := e.svc.Register(ctx, core.RegisterInput{Code: code, Name: "ann", Password: "short"}); err == nil {
		t.Fatal("密码太短应拒绝")
	}
	res, err := e.svc.Register(ctx, core.RegisterInput{Code: code, Name: "ann", DisplayName: "Ann", Password: goodPassword})
	if err != nil || res.Member == nil || !res.Member.IsAdmin() {
		t.Fatalf("个人邀请注册应直接生效并为管理员：%+v %v", res, err)
	}
	if _, err := e.svc.Register(ctx, core.RegisterInput{Code: code, Name: "bob", Password: goodPassword}); !errors.Is(err, core.ErrInviteInvalid) {
		t.Fatalf("个人邀请只能用一次：%v", err)
	}
	if _, err := e.svc.Login(ctx, "ann", goodPassword); err != nil {
		t.Fatalf("注册后应能登录：%v", err)
	}
	if _, err := e.svc.Login(ctx, "ann", "wrong-password!"); !errors.Is(err, core.ErrBadLogin) {
		t.Fatalf("密码错误：%v", err)
	}
	if _, err := e.svc.Login(ctx, "nobody", goodPassword); !errors.Is(err, core.ErrBadLogin) {
		t.Fatalf("不存在的账号：%v", err)
	}
}

func TestTeamInviteNeedsApproval(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	admin := adminOf(t, e)
	code := invite(t, e, admin, core.InviteSpec{Kind: store.InviteTeam, MaxUses: 2})

	res, err := e.svc.Register(ctx, core.RegisterInput{Code: code, Name: "carol", Password: goodPassword})
	if err != nil || res.Signup == nil || res.Member != nil {
		t.Fatalf("团队邀请应生成待审申请：%+v %v", res, err)
	}
	if _, err := e.svc.Register(ctx, core.RegisterInput{Code: code, Name: "carol", Password: goodPassword}); err == nil {
		t.Fatal("待审中的成员名不能重复申请")
	}
	if _, err := e.svc.Login(ctx, "carol", goodPassword); !errors.Is(err, core.ErrSignupPending) {
		t.Fatalf("待审时登录应提示等待批准：%v", err)
	}
	plain := acct(t, e, e.a)
	if _, err := e.svc.DecideSignup(ctx, core.WebActor(plain), 0, res.Signup.ID, true); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("非管理员不能审批：%v", err)
	}
	d, err := e.svc.DecideSignup(ctx, core.WebActor(admin), e.b.WorkspaceID, res.Signup.ID, true)
	if err != nil || d.Member == nil || d.Member.Name != "carol" || d.Decider != "lee-codex" {
		t.Fatalf("批准后应建成员：%+v %v", d, err)
	}
	if _, err := e.svc.DecideSignup(ctx, core.WebActor(admin), 0, res.Signup.ID, false); !errors.Is(err, core.ErrDecided) {
		t.Fatalf("不能重复审批：%v", err)
	}
	if _, err := e.svc.Login(ctx, "carol", goodPassword); err != nil {
		t.Fatalf("批准后可登录：%v", err)
	}

	res, err = e.svc.Register(ctx, core.RegisterInput{Code: code, Name: "dave", Password: goodPassword})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DecideSignup(ctx, core.ServerCLI(), 0, res.Signup.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Login(ctx, "dave", goodPassword); !errors.Is(err, core.ErrBadLogin) {
		t.Fatalf("被拒绝的申请不能登录：%v", err)
	}
	if _, err := e.svc.Register(ctx, core.RegisterInput{Code: code, Name: "erin", Password: goodPassword}); !errors.Is(err, core.ErrInviteInvalid) {
		t.Fatalf("用完次数后失效：%v", err)
	}
}

func TestInviteRules(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	admin := adminOf(t, e)
	plain := acct(t, e, e.a)
	if _, err := e.svc.CreateInvite(ctx, core.WebActor(plain), e.a.WorkspaceID, core.InviteSpec{Kind: store.InvitePersonal}); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("非管理员不能发邀请：%v", err)
	}
	var ie *core.InputError
	for _, spec := range []core.InviteSpec{{Kind: "x"}, {Kind: store.InviteTeam, Admin: true}, {Kind: store.InviteTeam, MaxUses: 1000},
		{Kind: store.InvitePersonal, TTL: time.Minute}} {
		if _, err := e.svc.CreateInvite(ctx, core.WebActor(admin), e.b.WorkspaceID, spec); !errors.As(err, &ie) {
			t.Errorf("%+v 应拒绝：%v", spec, err)
		}
	}
	inv, err := e.svc.CreateInvite(ctx, core.WebActor(admin), e.b.WorkspaceID, core.InviteSpec{Kind: store.InvitePersonal})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RevokeInvite(ctx, core.WebActor(admin), e.b.WorkspaceID, inv.Invite.ID); err != nil {
		t.Fatal(err)
	}
	if info, _ := e.svc.LookupInvite(ctx, inv.Code, nil); info.Valid {
		t.Fatal("作废后不可用")
	}
	if _, err := e.svc.LookupInvite(ctx, "nope", nil); !errors.Is(err, core.ErrInviteInvalid) {
		t.Fatalf("不存在的邀请：%v", err)
	}
}

func TestDeviceAuthFirstFreeThenApproval(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	admin := adminOf(t, e)
	me := acct(t, e, e.a)

	// 第一台设备：本人授权后直接生效
	st, err := e.svc.StartDeviceAuth(ctx, []string{"claude-code", "codex", "codex"}, "lee-mac.local 请点授权")
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret); p.Status != store.AuthPending {
		t.Fatalf("授权前应为等待：%+v", p)
	}
	v, err := e.svc.ViewDeviceAuth(ctx, me, st.Nonce)
	if err != nil || !v.FirstDevice || v.DeviceName != "lee-mac.local" || len(v.Tools) != 2 || len(v.Workspaces) != 1 {
		t.Fatalf("授权页内容不符：%+v %v", v, err)
	}
	a, err := e.svc.AuthorizeDevice(ctx, me, st.Nonce, true)
	if err != nil || a.Status != store.AuthApproved || a.DeviceID == nil {
		t.Fatalf("首台设备应直接批准（批准时建设备）：%+v %v", a, err)
	}
	if _, err := e.svc.ViewDeviceAuth(ctx, admin, st.Nonce); !errors.Is(err, core.ErrAuthGone) {
		t.Fatalf("别人不能查看已授权给我的请求：%v", err)
	}
	if _, err := e.svc.PollDeviceAuth(ctx, st.RequestID, "wrong"); !errors.Is(err, core.ErrAuthGone) {
		t.Fatalf("轮询密钥不对：%v", err)
	}
	p, err := e.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret)
	if err != nil || p.Status != store.AuthApproved || p.Account != "lee-claude" || len(p.Workspaces) != 1 ||
		len(p.Workspaces[0].Tokens) != 2 || !strings.HasPrefix(p.DeviceSecret, core.DeviceSecretPrefix) {
		t.Fatalf("应交付设备密钥与两个令牌：%+v %v", p, err)
	}
	who, err := store.LookupToken(ctx, e.svc.Pool, tokenOf(p, "default", "codex"))
	if err != nil || who.DeviceID == nil || *who.DeviceID != p.DeviceID {
		t.Fatalf("令牌应挂在设备上：%+v %v", who, err)
	}
	if p, _ := e.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret); p.Status != store.AuthDelivered || p.DeviceSecret != "" || p.Workspaces != nil {
		t.Fatalf("只交付一次：%+v", p)
	}

	// 设备密钥补签工具：同一设备、同一工作空间、同一工具的旧令牌被替换
	dev, _, err := e.svc.AuthenticateDevice(ctx, p.DeviceSecret)
	if err != nil || dev.ID != p.DeviceID {
		t.Fatalf("设备密钥应能证明设备：%+v %v", dev, err)
	}
	more, err := e.svc.IssueWorkspaceTokens(ctx, dev, "default", []string{"codex"})
	if err != nil || more.Tokens["codex"] == "" {
		t.Fatalf("补签：%+v %v", more, err)
	}
	if _, err := store.LookupToken(ctx, e.svc.Pool, tokenOf(p, "default", "codex")); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("旧令牌应吊销：%v", err)
	}
	if _, err := store.LookupToken(ctx, e.svc.Pool, tokenOf(p, "default", "claude-code")); err != nil {
		t.Fatalf("别的工具的令牌不受影响：%v", err)
	}

	// 第二台设备：需要管理员批准
	st2, err := e.svc.StartDeviceAuth(ctx, []string{"codex"}, "lee-win")
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := e.svc.ViewDeviceAuth(ctx, me, st2.Nonce); v.FirstDevice {
		t.Fatal("已有设备时不应标为首台")
	}
	a, err = e.svc.AuthorizeDevice(ctx, me, st2.Nonce, true)
	if err != nil || a.Status != store.AuthAwaitingAdmin {
		t.Fatalf("非首台应等管理员：%+v %v", a, err)
	}
	if _, err := e.svc.AuthorizeDevice(ctx, me, st2.Nonce, true); !errors.Is(err, core.ErrAuthGone) {
		t.Fatalf("不能重复授权：%v", err)
	}
	pending, err := e.svc.PendingDeviceAuths(ctx, &admin, e.b.WorkspaceID)
	if err != nil || len(pending) != 1 || pending[0].AccountName != "lee-claude" {
		t.Fatalf("管理员应看到待审设备：%+v %v", pending, err)
	}
	if _, err := e.svc.DecideDeviceAuth(ctx, core.WebActor(me), e.a.WorkspaceID, a.ID, true); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("成员不能自己批准：%v", err)
	}
	if _, err := e.svc.DecideDeviceAuth(ctx, core.WebActor(admin), e.b.WorkspaceID, a.ID, true); err != nil {
		t.Fatal(err)
	}
	p2, err := e.svc.PollDeviceAuth(ctx, st2.RequestID, st2.PollSecret)
	if err != nil || tokenOf(p2, "default", "codex") == "" {
		t.Fatalf("批准后交付：%+v %v", p2, err)
	}

	// 吊销设备：令牌与设备密钥一并失效；不能吊销看不到的设备
	other := acct(t, e, e.b)
	other.ServerAdmin = false
	if err := store.SetMemberRole(ctx, e.svc.Pool, e.b.MemberID, store.RoleMember); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RevokeDevice(ctx, core.WebActor(other), 0, p.DeviceID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不能吊销别人的设备：%v", err)
	}
	if err := e.svc.RevokeDevice(ctx, core.WebActor(me), 0, p.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupToken(ctx, e.svc.Pool, more.Tokens["codex"]); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("吊销设备后令牌失效：%v", err)
	}
	if _, _, err := e.svc.AuthenticateDevice(ctx, p.DeviceSecret); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("吊销设备后设备密钥失效：%v", err)
	}
}

func TestDeviceAuthDenyAndExpire(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	me := acct(t, e, e.a)
	st, _ := e.svc.StartDeviceAuth(ctx, []string{"codex"}, "x")
	if _, err := e.svc.AuthorizeDevice(ctx, me, st.Nonce, false); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret); p.Status != store.AuthDenied {
		t.Fatalf("拒绝：%+v", p)
	}
	st, _ = e.svc.StartDeviceAuth(ctx, []string{"codex"}, "x")
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE device_auths SET expires_at = now() - interval '1 second' WHERE id = $1`, st.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AuthorizeDevice(ctx, me, st.Nonce, true); !errors.Is(err, core.ErrAuthGone) {
		t.Fatalf("过期后不能授权：%v", err)
	}
	if p, _ := e.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret); p.Status != store.AuthExpired {
		t.Fatalf("过期：%+v", p)
	}
}

func TestInitialAdminAndPasswords(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.svc.DataDir = filepath.Join(t.TempDir(), "data")
	path, err := e.svc.EnsureInitialAdmin(ctx)
	if err != nil || path == "" {
		t.Fatalf("应生成初始管理员：%q %v", path, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("密码文件应仅本人可读：%v %v", info, err)
	}
	b, _ := os.ReadFile(path)
	var pw string
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "密码："); ok {
			pw = v
		}
	}
	sess, err := e.svc.Login(ctx, "admin", pw)
	if err != nil || !sess.Account.ServerAdmin {
		t.Fatalf("初始管理员应能登录且为服务器管理员：%+v %v", sess.Account, err)
	}
	if ms, _ := e.svc.Memberships(ctx, sess.Account.ID); len(ms) != 1 || ms[0].Workspace != "default" || ms[0].Role != store.RoleAdmin {
		t.Fatalf("初始管理员应是 default 工作空间的管理员：%+v", ms)
	}
	if st, _ := e.svc.PasswordStatus(ctx, sess.Account.ID); !st.MustChange {
		t.Fatal("首次登录必须改密码")
	}
	if again, err := e.svc.EnsureInitialAdmin(ctx); err != nil || again != "" {
		t.Fatalf("已有管理员时不再生成：%q %v", again, err)
	}
	other, err := e.svc.Login(ctx, "admin", pw)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ChangePassword(ctx, sess.Account, sess.ID, pw, goodPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("改密码后应删除初始密码文件：%v", err)
	}
	if _, _, err := e.svc.SessionAccount(ctx, other.ID); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("改密码后其他会话失效：%v", err)
	}
	if _, _, err := e.svc.SessionAccount(ctx, sess.ID); err != nil {
		t.Fatalf("当前会话保留：%v", err)
	}

	newPw, err := e.svc.ResetPassword(ctx, core.ServerCLI(), "admin")
	if err != nil || len(newPw) != 20 {
		t.Fatalf("重置密码：%v", err)
	}
	if _, _, err := e.svc.SessionAccount(ctx, sess.ID); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("重置后全部会话失效：%v", err)
	}
	if _, err := e.svc.Login(ctx, "admin", newPw); err != nil {
		t.Fatalf("新密码可登录：%v", err)
	}
}

func TestWebLoginCodeAndGlobalLogin(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	code, err := e.svc.CreateWebLoginCode(ctx, e.a.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := e.svc.UseWebLoginCode(ctx, code)
	if err != nil || sess.Account.Name != "lee-claude" {
		t.Fatalf("免密登录：%+v %v", sess, err)
	}
	if _, via, err := e.svc.SessionAccount(ctx, sess.ID); err != nil || via != store.SessionViaDevice {
		t.Fatalf("免密会话来源应为 device：%q %v", via, err)
	}
	if _, err := e.svc.UseWebLoginCode(ctx, code); !errors.Is(err, core.ErrWebLoginCode) {
		t.Fatalf("只能用一次：%v", err)
	}

	// 同一个账号在两个工作空间：登录不再选工作空间，登录后看到两个工作空间
	ws2, _ := store.CreateWorkspace(ctx, e.svc.Pool, "payments")
	if _, err := store.AddMembership(ctx, e.svc.Pool, e.a.AccountID, ws2.ID, store.RoleMember, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ChangePassword(ctx, acct(t, e, e.a), "", "", goodPassword); err != nil {
		t.Fatal(err)
	}
	s, err := e.svc.Login(ctx, "lee-claude", goodPassword)
	if err != nil || s.Account.ID != e.a.AccountID {
		t.Fatalf("登录：%+v %v", s, err)
	}
	if ms, _ := e.svc.Memberships(ctx, s.Account.ID); len(ms) != 2 {
		t.Fatalf("应看到两个工作空间：%+v", ms)
	}
}
