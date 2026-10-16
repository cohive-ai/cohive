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

func adminOf(t *testing.T, e env) store.Member {
	t.Helper()
	ctx := context.Background()
	if err := store.SetMemberRole(ctx, e.svc.Pool, e.b.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	m, err := store.MemberByID(ctx, e.svc.Pool, e.b.MemberID)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func invite(t *testing.T, e env, admin store.Member, spec core.InviteSpec) string {
	t.Helper()
	inv, err := e.svc.CreateInvite(context.Background(), &admin, admin.WorkspaceID, spec)
	if err != nil {
		t.Fatal(err)
	}
	return inv.Code
}

func TestPersonalInviteRegister(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	admin := adminOf(t, e)
	code := invite(t, e, admin, core.InviteSpec{Kind: store.InvitePersonal, Admin: true})

	info, err := e.svc.LookupInvite(ctx, code)
	if err != nil || !info.Valid || info.Workspace != "default" || info.Kind != store.InvitePersonal {
		t.Fatalf("邀请信息不符：%+v %v", info, err)
	}
	if _, err := e.svc.Register(ctx, core.RegisterInput{Code: code, Name: "lee-codex", Password: goodPassword}); err == nil ||
		!strings.Contains(err.Error(), "已被使用") {
		t.Fatalf("成员名重复应拒绝：%v", err)
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
	if _, err := e.svc.Login(ctx, "ann", goodPassword, ""); err != nil {
		t.Fatalf("注册后应能登录：%v", err)
	}
	if _, err := e.svc.Login(ctx, "ann", "wrong-password!", ""); !errors.Is(err, core.ErrBadLogin) {
		t.Fatalf("密码错误：%v", err)
	}
	if _, err := e.svc.Login(ctx, "nobody", goodPassword, ""); !errors.Is(err, core.ErrBadLogin) {
		t.Fatalf("不存在的成员：%v", err)
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
	if _, err := e.svc.Login(ctx, "carol", goodPassword, ""); !errors.Is(err, core.ErrSignupPending) {
		t.Fatalf("待审时登录应提示等待批准：%v", err)
	}
	plain := store.Member{ID: e.a.MemberID, WorkspaceID: e.a.WorkspaceID, Role: store.RoleMember}
	if _, err := e.svc.DecideSignup(ctx, &plain, res.Signup.ID, true); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("非管理员不能审批：%v", err)
	}
	d, err := e.svc.DecideSignup(ctx, &admin, res.Signup.ID, true)
	if err != nil || d.Member == nil || d.Member.Name != "carol" || d.Decider != "lee-codex" {
		t.Fatalf("批准后应建成员：%+v %v", d, err)
	}
	if _, err := e.svc.DecideSignup(ctx, &admin, res.Signup.ID, false); !errors.Is(err, core.ErrDecided) {
		t.Fatalf("不能重复审批：%v", err)
	}
	if _, err := e.svc.Login(ctx, "carol", goodPassword, ""); err != nil {
		t.Fatalf("批准后可登录：%v", err)
	}

	res, err = e.svc.Register(ctx, core.RegisterInput{Code: code, Name: "dave", Password: goodPassword})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.DecideSignup(ctx, nil, res.Signup.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Login(ctx, "dave", goodPassword, ""); !errors.Is(err, core.ErrBadLogin) {
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
	plain := store.Member{ID: e.a.MemberID, WorkspaceID: e.a.WorkspaceID, Role: store.RoleMember}
	if _, err := e.svc.CreateInvite(ctx, &plain, plain.WorkspaceID, core.InviteSpec{Kind: store.InvitePersonal}); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("非管理员不能发邀请：%v", err)
	}
	var ie *core.InputError
	for _, spec := range []core.InviteSpec{{Kind: "x"}, {Kind: store.InviteTeam, Admin: true}, {Kind: store.InviteTeam, MaxUses: 1000},
		{Kind: store.InvitePersonal, TTL: time.Minute}} {
		if _, err := e.svc.CreateInvite(ctx, &admin, admin.WorkspaceID, spec); !errors.As(err, &ie) {
			t.Errorf("%+v 应拒绝：%v", spec, err)
		}
	}
	inv, err := e.svc.CreateInvite(ctx, &admin, admin.WorkspaceID, core.InviteSpec{Kind: store.InvitePersonal})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.RevokeInvite(ctx, &admin, admin.WorkspaceID, inv.Invite.ID); err != nil {
		t.Fatal(err)
	}
	if info, _ := e.svc.LookupInvite(ctx, inv.Code); info.Valid {
		t.Fatal("作废后不可用")
	}
	if _, err := e.svc.LookupInvite(ctx, "nope"); !errors.Is(err, core.ErrInviteInvalid) {
		t.Fatalf("不存在的邀请：%v", err)
	}
}

func TestDeviceAuthFirstFreeThenApproval(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	admin := adminOf(t, e)
	me, err := store.MemberByID(ctx, e.svc.Pool, e.a.MemberID)
	if err != nil {
		t.Fatal(err)
	}

	// 第一台设备：成员授权后直接生效
	st, err := e.svc.StartDeviceAuth(ctx, []string{"claude-code", "codex", "codex"}, "lee-mac.local 请点授权")
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret); p.Status != store.AuthPending {
		t.Fatalf("授权前应为等待：%+v", p)
	}
	v, err := e.svc.ViewDeviceAuth(ctx, me, st.Nonce)
	if err != nil || !v.FirstDevice || v.DeviceName != "lee-mac.local" || len(v.Tools) != 2 {
		t.Fatalf("授权页内容不符：%+v %v", v, err)
	}
	a, err := e.svc.AuthorizeDevice(ctx, me, st.Nonce, true)
	if err != nil || a.Status != store.AuthApproved {
		t.Fatalf("首台设备应直接批准：%+v %v", a, err)
	}
	if _, err := e.svc.ViewDeviceAuth(ctx, admin, st.Nonce); !errors.Is(err, core.ErrAuthGone) {
		t.Fatalf("别人不能查看已授权给我的请求：%v", err)
	}
	if _, err := e.svc.PollDeviceAuth(ctx, st.RequestID, "wrong"); !errors.Is(err, core.ErrAuthGone) {
		t.Fatalf("轮询密钥不对：%v", err)
	}
	p, err := e.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret)
	if err != nil || p.Status != store.AuthApproved || p.Member != "lee-claude" || len(p.Tokens) != 2 {
		t.Fatalf("应交付两个令牌：%+v %v", p, err)
	}
	who, err := store.LookupToken(ctx, e.svc.Pool, p.Tokens["codex"])
	if err != nil || who.DeviceID == nil || *who.DeviceID != p.DeviceID {
		t.Fatalf("令牌应挂在设备上：%+v %v", who, err)
	}
	if p, _ := e.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret); p.Status != store.AuthDelivered || p.Tokens != nil {
		t.Fatalf("只交付一次：%+v", p)
	}

	// 已配对设备补签工具：同一设备同一工具的旧令牌被替换
	more, err := e.svc.AddDeviceTools(ctx, who, []string{"codex"})
	if err != nil || more.DeviceID != p.DeviceID {
		t.Fatalf("补签：%+v %v", more, err)
	}
	if _, err := store.LookupToken(ctx, e.svc.Pool, p.Tokens["codex"]); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("旧令牌应吊销：%v", err)
	}
	if _, err := e.svc.AddDeviceTools(ctx, e.a, []string{"codex"}); !errors.Is(err, core.ErrNoDevice) {
		t.Fatalf("手工令牌不属于设备：%v", err)
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
	pending, err := store.ListAwaitingDeviceAuths(ctx, e.svc.Pool, admin.WorkspaceID)
	if err != nil || len(pending) != 1 || pending[0].MemberName != "lee-claude" {
		t.Fatalf("管理员应看到待审设备：%+v %v", pending, err)
	}
	if _, err := e.svc.DecideDeviceAuth(ctx, &me, a.ID, true); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("成员不能自己批准：%v", err)
	}
	if _, err := e.svc.DecideDeviceAuth(ctx, &admin, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if p, err := e.svc.PollDeviceAuth(ctx, st2.RequestID, st2.PollSecret); err != nil || len(p.Tokens) != 1 {
		t.Fatalf("批准后交付：%+v %v", p, err)
	}

	// 吊销设备：令牌一并失效；成员不能吊销别人的设备
	if err := e.svc.RevokeDevice(ctx, store.Member{ID: 999, WorkspaceID: me.WorkspaceID}, p.DeviceID); err == nil {
		t.Fatal("不能吊销别人的设备")
	}
	if err := e.svc.RevokeDevice(ctx, me, more.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupToken(ctx, e.svc.Pool, more.Tokens["codex"]); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("吊销设备后令牌失效：%v", err)
	}
}

func TestDeviceAuthDenyAndExpire(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	me, _ := store.MemberByID(ctx, e.svc.Pool, e.a.MemberID)
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
	sess, err := e.svc.Login(ctx, "admin", pw, "")
	if err != nil || !sess.Member.IsAdmin() {
		t.Fatalf("初始管理员应能登录：%v", err)
	}
	if st, _ := e.svc.PasswordStatus(ctx, sess.Member.ID); !st.MustChange {
		t.Fatal("首次登录必须改密码")
	}
	if again, err := e.svc.EnsureInitialAdmin(ctx); err != nil || again != "" {
		t.Fatalf("已有管理员时不再生成：%q %v", again, err)
	}
	other, err := e.svc.Login(ctx, "admin", pw, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ChangePassword(ctx, sess.Member, sess.ID, pw, goodPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("改密码后应删除初始密码文件：%v", err)
	}
	if _, _, err := e.svc.SessionMember(ctx, other.ID); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("改密码后其他会话失效：%v", err)
	}
	if _, _, err := e.svc.SessionMember(ctx, sess.ID); err != nil {
		t.Fatalf("当前会话保留：%v", err)
	}

	newPw, err := e.svc.ResetPassword(ctx, sess.Member.WorkspaceID, "admin")
	if err != nil || len(newPw) != 20 {
		t.Fatalf("重置密码：%v", err)
	}
	if _, _, err := e.svc.SessionMember(ctx, sess.ID); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("重置后全部会话失效：%v", err)
	}
	if _, err := e.svc.Login(ctx, "admin", newPw, ""); err != nil {
		t.Fatalf("新密码可登录：%v", err)
	}
}

func TestWebLoginCodeAndAmbiguousLogin(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	code, err := e.svc.CreateWebLoginCode(ctx, e.a)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := e.svc.UseWebLoginCode(ctx, code)
	if err != nil || sess.Member.Name != "lee-claude" {
		t.Fatalf("免密登录：%+v %v", sess, err)
	}
	if _, err := e.svc.UseWebLoginCode(ctx, code); !errors.Is(err, core.ErrWebLoginCode) {
		t.Fatalf("只能用一次：%v", err)
	}

	// 同名成员在两个工作空间、密码相同：需要选择工作空间
	ws2, _ := store.CreateWorkspace(ctx, e.svc.Pool, "payments")
	for _, ws := range []int64{e.a.WorkspaceID, ws2.ID} {
		m, err := store.CreateMember(ctx, e.svc.Pool, ws, "zed", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := e.svc.ChangePassword(ctx, m, "", "", goodPassword); err != nil {
			t.Fatal(err)
		}
	}
	var amb *core.AmbiguousLoginError
	if _, err := e.svc.Login(ctx, "zed", goodPassword, ""); !errors.As(err, &amb) || len(amb.Workspaces) != 2 {
		t.Fatalf("应要求选工作空间：%v", err)
	}
	if s, err := e.svc.Login(ctx, "zed", goodPassword, "payments"); err != nil || s.Member.WorkspaceID != ws2.ID {
		t.Fatalf("指定工作空间登录：%v", err)
	}
}
