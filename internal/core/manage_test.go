package core_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// tokenFor 为成员资格签发令牌并返回身份。
func tokenFor(t *testing.T, svc *core.Service, m store.Member) store.Identity {
	t.Helper()
	ctx := context.Background()
	plain, _, err := store.IssueToken(ctx, svc.Pool, m.ID, "codex")
	if err != nil {
		t.Fatal(err)
	}
	who, err := store.LookupToken(ctx, svc.Pool, plain)
	if err != nil {
		t.Fatal(err)
	}
	return who
}

type handoverFixture struct {
	p              people
	bob            store.Member
	asOwner        store.Issue // ann 是负责人（待接单）
	asSource       store.Issue // ann 是发起人（待验证）
	draft          store.Issue // ann 的草稿：不需要转交
	payAdminMember store.Member
}

func newHandover(t *testing.T) handoverFixture {
	t.Helper()
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	if err := store.SetWorkspaceNotify(ctx, svc.Pool, p.pay, store.NotifyConfig{Channel: "telegram", Target: "-1001"}, ""); err != nil {
		t.Fatal(err)
	}
	bob, err := store.CreateMember(ctx, svc.Pool, p.pay, "bob", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertService(ctx, svc.Pool, p.pay, "api", p.annPay.ID); err != nil {
		t.Fatal(err)
	}
	ann, bobID := tokenFor(t, svc, p.annPay), tokenFor(t, svc, bob)
	act := func(who store.Identity, is store.Issue, a issue.Action) store.Issue {
		t.Helper()
		out, err := svc.Act(ctx, who, core.ActInput{IssueID: is.ID, Action: a, ExpectedVersion: is.Version})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	d1, err := svc.Draft(ctx, bobID, core.DraftInput{Title: "给 ann 的单", Owner: "ann"})
	if err != nil {
		t.Fatal(err)
	}
	h := handoverFixture{p: p, bob: bob, payAdminMember: p.payAdminMember}
	h.asOwner = act(bobID, d1, issue.ActSubmit)
	d2, err := svc.Draft(ctx, ann, core.DraftInput{Title: "ann 发给 bob 的单", Owner: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	x := act(ann, d2, issue.ActSubmit)
	x = act(bobID, x, issue.ActAccept)
	h.asSource = act(bobID, x, issue.ActRequestVerification)
	if h.draft, err = svc.Draft(ctx, ann, core.DraftInput{Title: "ann 的草稿"}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h handoverFixture) fullPlan(to int64) core.HandoverPlan {
	return core.HandoverPlan{
		Issues: []core.PlanIssue{
			{IssueID: h.asOwner.ID, As: store.AsOwner, ToMemberID: to, ExpectedVersion: h.asOwner.Version},
			{IssueID: h.asSource.ID, As: store.AsSource, ToMemberID: to, ExpectedVersion: h.asSource.Version},
		},
		Services: []core.PlanService{{Name: "api", ToMemberID: to}},
	}
}

// 6.2 第 8 条：移出与转交。
func TestRemoveWithHandover(t *testing.T) {
	ctx := context.Background()
	h := newHandover(t)
	p, svc := h.p, h.p.e.svc
	got, err := svc.GetHandover(ctx, &p.payAdmin, p.pay, p.annPay.ID)
	if err != nil || len(got.Issues) != 2 || len(got.Services) != 1 {
		t.Fatalf("转交清单：两张单（负责人、发起人各一）与一个服务，不含草稿：%+v %v", got, err)
	}
	unchanged := func(why string) {
		t.Helper()
		is, _ := store.GetIssue(ctx, svc.Pool, h.asOwner.ID)
		m, _ := store.MemberByID(ctx, svc.Pool, p.annPay.ID)
		if is.OwnerID == nil || *is.OwnerID != p.annPay.ID || m.Status != store.MemberActive {
			t.Fatalf("%s：应整体不做：%+v %+v", why, is, m)
		}
	}
	var he *core.HandoverError
	// 清单不完整
	partial := h.fullPlan(h.bob.ID)
	partial.Issues = partial.Issues[:1]
	if err := svc.ApplyHandover(ctx, &p.payAdmin, p.pay, p.annPay.ID, partial, true); !errors.As(err, &he) || len(he.Handover.Issues) != 2 {
		t.Fatalf("清单不完整应 409 并附最新清单：%v", err)
	}
	unchanged("清单不完整")
	noSvc := h.fullPlan(h.bob.ID)
	noSvc.Services = nil
	if err := svc.ApplyHandover(ctx, &p.payAdmin, p.pay, p.annPay.ID, noSvc, true); !errors.As(err, &he) {
		t.Fatalf("服务没有转交应 409：%v", err)
	}
	// 版本已变化
	stale := h.fullPlan(h.bob.ID)
	stale.Issues[1].ExpectedVersion--
	if err := svc.ApplyHandover(ctx, &p.payAdmin, p.pay, p.annPay.ID, stale, true); !errors.As(err, &he) {
		t.Fatalf("版本已变应 409：%v", err)
	}
	unchanged("版本已变")
	// 接手人不合格：被移出的人自己、别的工作空间的人、已停用的人
	for _, bad := range []int64{p.annPay.ID, p.opsMember.ID} {
		if err := svc.ApplyHandover(ctx, &p.payAdmin, p.pay, p.annPay.ID, h.fullPlan(bad), true); !isInput(err) {
			t.Fatalf("接手人 %d 不合格应 400：%v", bad, err)
		}
	}
	if err := svc.DisableAccount(ctx, &p.root, h.bob.AccountID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ApplyHandover(ctx, &p.payAdmin, p.pay, p.annPay.ID, h.fullPlan(h.bob.ID), true); !isInput(err) {
		t.Fatalf("已停用的接手人应 400：%v", err)
	}
	unchanged("接手人不合格")

	// 成功：全部转给 pay-admin
	annTok := tokenFor(t, svc, p.annPay)
	to := h.payAdminMember.ID
	if err := svc.ApplyHandover(ctx, &p.payAdmin, p.pay, p.annPay.ID, h.fullPlan(to), true); err != nil {
		t.Fatal(err)
	}
	o, _ := store.GetIssue(ctx, svc.Pool, h.asOwner.ID)
	s, _ := store.GetIssue(ctx, svc.Pool, h.asSource.ID)
	if *o.OwnerID != to || o.Version != h.asOwner.Version+1 || o.Status != issue.StatusOpen {
		t.Fatalf("负责人应改派、版本加一、状态不变：%+v", o)
	}
	if s.SourceMemberID != to || s.Status != issue.StatusReadyForVerification {
		t.Fatalf("发起人应改派：%+v", s)
	}
	if owner, err := store.ServiceOwner(ctx, svc.Pool, p.pay, "api"); err != nil || owner.ID != to {
		t.Fatalf("服务负责人应改派：%+v %v", owner, err)
	}
	evs, _ := store.ListEvents(ctx, svc.Pool, h.asOwner.ID)
	last := evs[len(evs)-1]
	if last.Action != issue.ActReassign || last.ActorMemberID != to || last.Payload["owner_to"] != float64(to) {
		t.Fatalf("应写改派事件：%+v", last)
	}
	ns, _ := store.ListNotificationsForIssue(ctx, svc.Pool, h.asOwner.ID)
	if n := ns[len(ns)-1]; n.Event != core.EventAssigned || n.Recipients[0] != to {
		t.Fatalf("应给新负责人入队“交给你”的群通知：%+v", n)
	}
	ns, _ = store.ListNotificationsForIssue(ctx, svc.Pool, h.asSource.ID)
	if n := ns[len(ns)-1]; n.Event != core.EventVerify || n.Recipients[0] != to {
		t.Fatalf("应给新发起人入队“请验证”的群通知：%+v", n)
	}
	if m, _ := store.MemberByID(ctx, svc.Pool, p.annPay.ID); m.Status != store.MemberRemoved {
		t.Fatalf("应已移出：%+v", m)
	}
	assertRevoked(t, svc, annTok)
	if d, _ := store.GetIssue(ctx, svc.Pool, h.draft.ID); d.SourceMemberID != p.annPay.ID {
		t.Fatal("草稿不动")
	}
}

// assertRevoked 确认令牌行已标为吊销（第一道防线）。
func assertRevoked(t *testing.T, svc *core.Service, who store.Identity) {
	t.Helper()
	var revoked bool
	if err := svc.Pool.QueryRow(context.Background(), `SELECT revoked_at IS NOT NULL FROM tokens WHERE id = $1`, who.TokenID).Scan(&revoked); err != nil {
		t.Fatal(err)
	}
	if !revoked {
		t.Fatal("令牌应已吊销")
	}
}

// 不能移出最后一名管理员，也不能移出自己。
func TestRemoveLastAdminOrSelf(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	if err := svc.ApplyHandover(ctx, &p.payAdmin, p.pay, p.payAdminMember.ID, core.HandoverPlan{}, true); !isInput(err) {
		t.Fatalf("不能移出自己：%v", err)
	}
	// 服务器管理员作为普通成员加入 ops，再移出 ops 唯一的管理员
	if _, err := svc.AddMember(ctx, &p.root, p.ops, "root", store.RoleMember); err != nil {
		t.Fatal(err)
	}
	if err := svc.ApplyHandover(ctx, &p.root, p.ops, p.opsMember.ID, core.HandoverPlan{}, true); !isInput(err) {
		t.Fatalf("不能移出最后一名管理员：%v", err)
	}
	if m, _ := store.MemberByID(ctx, svc.Pool, p.opsMember.ID); m.Status != store.MemberActive {
		t.Fatal("最后一名管理员应仍在册")
	}
}

// 6.2 第 9 条：停用与启用。
func TestDisableAndEnable(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	if err := svc.ChangePassword(ctx, p.ann, "", "", goodPassword); err != nil {
		t.Fatal(err)
	}
	d := pair(t, svc, p.ann, "codex")
	sess, err := svc.Login(ctx, "ann", goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	uid := int64(777)
	if err := store.SetAccountTelegram(ctx, svc.Pool, p.ann.ID, &uid, "ann_tg"); err != nil {
		t.Fatal(err)
	}
	// 一条等管理员批准的、一条已批准未领取的设备申请（批准前先造好第二台）
	waiting, _ := svc.StartDeviceAuth(ctx, []string{"codex"}, "w")
	if a, err := svc.AuthorizeDevice(ctx, p.ann, waiting.Nonce, true); err != nil || a.Status != store.AuthAwaitingAdmin {
		t.Fatalf("%+v %v", a, err)
	}
	approved, _ := svc.StartDeviceAuth(ctx, []string{"codex"}, "a")
	a2, _ := svc.AuthorizeDevice(ctx, p.ann, approved.Nonce, true)
	if _, err := svc.DecideDeviceAuth(ctx, &p.payAdmin, p.pay, a2.ID, true); err != nil {
		t.Fatal(err)
	}

	// 工作空间管理员不能停用；不能停用自己；不能停用最后一名服务器管理员
	if err := svc.DisableAccount(ctx, &p.payAdmin, p.ann.ID); !errors.Is(err, core.ErrNotServerAdmin) {
		t.Fatalf("工作空间管理员不能停用：%v", err)
	}
	if err := svc.DisableAccount(ctx, &p.root, p.root.ID); !isInput(err) {
		t.Fatalf("不能停用自己：%v", err)
	}
	if err := svc.DisableAccount(ctx, nil, p.root.ID); !isInput(err) {
		t.Fatalf("不能停用最后一名服务器管理员：%v", err)
	}

	if err := svc.DisableAccount(ctx, &p.root, p.ann.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "ann", goodPassword); !errors.Is(err, store.ErrAccountDisabled) {
		t.Fatalf("停用后登录被拒：%v", err)
	}
	if _, err := store.LookupToken(ctx, svc.Pool, tokenOf(d, "pay", "codex")); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("令牌失效：%v", err)
	}
	if _, _, err := svc.AuthenticateDevice(ctx, d.DeviceSecret); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("设备密钥失效：%v", err)
	}
	if _, _, err := svc.SessionAccount(ctx, sess.ID); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("会话失效：%v", err)
	}
	if a, _ := store.AccountByID(ctx, svc.Pool, p.ann.ID); a.TelegramUserID != nil || a.TelegramUsername != "" {
		t.Fatalf("Telegram 应断开：%+v", a)
	}
	if x, _ := store.GetDeviceAuth(ctx, svc.Pool, waiting.RequestID, false); x.Status != store.AuthDenied {
		t.Fatalf("等待中的设备申请应拒绝：%+v", x)
	}
	if got, err := svc.PollDeviceAuth(ctx, approved.RequestID, approved.PollSecret); err != nil || got.Status != store.AuthDenied || got.DeviceSecret != "" {
		t.Fatalf("已批准未领取的授权领取时应拒绝且不交付：%+v %v", got, err)
	}
	var live int
	if err := svc.Pool.QueryRow(ctx, `SELECT count(*) FROM devices WHERE account_id = $1 AND revoked_at IS NULL`, p.ann.ID).Scan(&live); err != nil || live != 0 {
		t.Fatalf("全部设备应吊销：%d %v", live, err)
	}
	// 停用后仍是在册成员（看板标停用），不能被指派
	if m, _ := store.MemberByID(ctx, svc.Pool, p.annPay.ID); m.Status != store.MemberActive || !m.AccountDisabled {
		t.Fatalf("停用不影响成员资格：%+v", m)
	}
	adminWho := tokenFor(t, svc, p.payAdminMember)
	if _, err := svc.Draft(ctx, adminWho, core.DraftInput{Title: "x", Owner: "ann"}); !isInput(err) {
		t.Fatalf("不能指派给已停用的人：%v", err)
	}

	// 启用：设备不恢复，再配对需管理员批准
	if err := svc.EnableAccount(ctx, &p.root, p.ann.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Login(ctx, "ann", goodPassword); err != nil {
		t.Fatalf("启用后可登录：%v", err)
	}
	st, _ := svc.StartDeviceAuth(ctx, []string{"codex"}, "again")
	if a, err := svc.AuthorizeDevice(ctx, p.ann, st.Nonce, true); err != nil || a.Status != store.AuthAwaitingAdmin {
		t.Fatalf("启用后再配对需管理员批准：%+v %v", a, err)
	}
}

// 停用的人可以只改派、不移出（remove 为假）。
func TestHandoverWithoutRemoving(t *testing.T) {
	ctx := context.Background()
	h := newHandover(t)
	p, svc := h.p, h.p.e.svc
	if err := svc.DisableAccount(ctx, &p.root, p.ann.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ApplyHandover(ctx, &p.payAdmin, p.pay, p.annPay.ID, h.fullPlan(h.bob.ID), false); err != nil {
		t.Fatal(err)
	}
	if m, _ := store.MemberByID(ctx, svc.Pool, p.annPay.ID); m.Status != store.MemberActive {
		t.Fatal("只改派时不移出")
	}
	if o, _ := store.GetIssue(ctx, svc.Pool, h.asOwner.ID); *o.OwnerID != h.bob.ID {
		t.Fatal("应已改派")
	}
}

// 6.2 第 12 条：直接加入已有账号。
func TestAddExistingAccount(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	if l, err := svc.LookupAccount(ctx, &p.payAdmin, p.pay, "ops-admin"); err != nil || l.MemberStatus != "none" {
		t.Fatalf("按登录名精确查找：%+v %v", l, err)
	}
	if _, err := svc.LookupAccount(ctx, &p.payAdmin, p.pay, "ops"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不支持前缀查找：%v", err)
	}
	if _, err := svc.AddMember(ctx, &p.ann, p.pay, "ops-admin", ""); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("普通成员不能加人：%v", err)
	}
	m, err := svc.AddMember(ctx, &p.payAdmin, p.pay, "ops-admin", store.RoleAdmin)
	if err != nil || m.Role != store.RoleAdmin || m.AccountID != p.opsAdmin.ID {
		t.Fatalf("直接加入：%+v %v", m, err)
	}
	var se *core.StateError
	if _, err := svc.AddMember(ctx, &p.payAdmin, p.pay, "ops-admin", ""); !errors.As(err, &se) {
		t.Fatalf("已是成员应 409：%v", err)
	}
	zoe, _ := store.CreateAccount(ctx, svc.Pool, "zoe", "")
	if err := svc.DisableAccount(ctx, &p.root, zoe.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddMember(ctx, &p.payAdmin, p.pay, "zoe", ""); !isInput(err) {
		t.Fatalf("不能加入已停用的账号：%v", err)
	}
}

// 6.2 第 13 条：只能删除没有任何问题单的工作空间，删除时清掉邀请、成员资格、令牌、群绑定。
func TestDeleteWorkspace(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	if err := svc.DeleteWorkspace(ctx, &p.payAdmin, "ops"); !errors.Is(err, core.ErrNotServerAdmin) {
		t.Fatalf("工作空间管理员不能删：%v", err)
	}
	// ops 有一张草稿：不能删
	opsWho := tokenFor(t, svc, p.annOps)
	if _, err := svc.Draft(ctx, opsWho, core.DraftInput{Title: "草稿"}); err != nil {
		t.Fatal(err)
	}
	var se *core.StateError
	if err := svc.DeleteWorkspace(ctx, &p.root, "ops"); !errors.As(err, &se) {
		t.Fatalf("有草稿也算有问题单：%v", err)
	}
	// 新建一个空工作空间，带邀请、成员、令牌、群绑定，删掉
	ws, err := svc.CreateWorkspace(ctx, &p.root, "tmp", "ann")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateWorkspace(ctx, &p.root, ws.ID, core.WorkspaceSettings{TelegramChat: "-1009"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateInvite(ctx, &p.ann, ws.ID, core.InviteSpec{Kind: store.InviteTeam}); err != nil {
		t.Fatal(err)
	}
	m, _ := store.MemberOf(ctx, svc.Pool, p.ann.ID, ws.ID)
	tok := tokenFor(t, svc, m)
	if err := svc.DeleteWorkspace(ctx, &p.root, "tmp"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.WorkspaceByName(ctx, svc.Pool, "tmp"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("应已删除")
	}
	var n int
	if err := svc.Pool.QueryRow(ctx, `SELECT count(*) FROM tokens WHERE id = $1`, tok.TokenID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("令牌应一并删除：%d %v", n, err)
	}
	if _, err := svc.WorkspaceByGroup(ctx, -1009); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("群绑定应一并清掉")
	}
	if ms, _ := svc.Memberships(ctx, p.ann.ID); len(ms) != 2 {
		t.Fatalf("ann 的其他工作空间不受影响：%+v", ms)
	}
}

// 一个群只能绑一个工作空间。
func TestGroupBoundOnce(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	if err := svc.UpdateWorkspace(ctx, &p.payAdmin, p.pay, core.WorkspaceSettings{TelegramChat: "-1001"}); err != nil {
		t.Fatal(err)
	}
	var se *core.StateError
	if err := svc.UpdateWorkspace(ctx, &p.opsAdmin, p.ops, core.WorkspaceSettings{TelegramChat: "-1001"}); !errors.As(err, &se) ||
		!strings.Contains(err.Error(), "pay") {
		t.Fatalf("同一个群不能绑第二个工作空间，并说明已绑给谁：%v", err)
	}
	if err := svc.UpdateWorkspace(ctx, &p.payAdmin, p.pay, core.WorkspaceSettings{TelegramChat: "-1001", ShowTitle: true}); err != nil {
		t.Fatalf("重存自己的群编号不受影响：%v", err)
	}
	if ws, err := svc.WorkspaceByGroup(ctx, -1001); err != nil || ws.ID != p.pay {
		t.Fatalf("由群反查工作空间：%+v %v", ws, err)
	}
}

// 两名管理员同时互相移出：恰好一个成功，至少保留一名管理员，另一个得到明确的拒绝而不是内部错误。
func TestConcurrentMutualRemoval(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	b, err := svc.AddMember(ctx, &p.payAdmin, p.pay, "ops-admin", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	hold, err := svc.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(ctx, `LOCK TABLE members IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	type job struct {
		actor  store.Account
		target int64
	}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, j := range []job{{p.payAdmin, b.ID}, {p.opsAdmin, p.payAdminMember.ID}} {
		wg.Add(1)
		go func(j job) {
			defer wg.Done()
			errs <- svc.ApplyHandover(ctx, &j.actor, p.pay, j.target, core.HandoverPlan{}, true)
		}(j)
	}
	time.Sleep(300 * time.Millisecond)
	_ = hold.Rollback(ctx)
	wg.Wait()
	close(errs)
	ok, refused := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case isInput(err) || errors.Is(err, core.ErrNotAdmin):
			refused++
		default:
			t.Fatalf("不应返回内部错误：%v", err)
		}
	}
	if ok != 1 || refused != 1 {
		t.Fatalf("应恰好一个成功：成功 %d，拒绝 %d", ok, refused)
	}
	if n, _ := store.CountAdmins(ctx, svc.Pool, p.pay); n != 1 {
		t.Fatalf("应保留一名管理员：%d", n)
	}
}
