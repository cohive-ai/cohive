package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// 作废（#12）的测试准备：c 是第三个成员、本工作空间管理员，网页上用账号密码登录。
type cancelEnv struct {
	env
	c   store.Identity
	acc store.Account // c 的账号
}

func newCancelEnv(t *testing.T) cancelEnv {
	t.Helper()
	ctx := context.Background()
	e := newEnv(t)
	enableNotify(t, e)
	q := e.svc.Pool
	m, err := store.CreateMember(ctx, q, e.a.WorkspaceID, "lee-admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetMemberRole(ctx, q, m.ID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	acc, err := store.AccountByID(ctx, q, m.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	c := store.Identity{AccountID: m.AccountID, MemberID: m.ID, WorkspaceID: e.a.WorkspaceID, MemberName: m.Name, Tool: "web"}
	return cancelEnv{env: e, c: c, acc: acc}
}

func (e cancelEnv) web(t *testing.T, who store.Identity) (core.Actor, store.Identity) {
	t.Helper()
	a, err := store.AccountByID(context.Background(), e.svc.Pool, who.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	who.Tool = "web"
	return core.WebActor(a), who
}

func lastEventOf(t *testing.T, e env, id int64) store.Event {
	t.Helper()
	_, evs, err := e.svc.Get(context.Background(), e.a, id)
	if err != nil {
		t.Fatal(err)
	}
	return evs[len(evs)-1]
}

// 发起人在编程工具里作废自己还没被接单的单：必须写理由；理由进动态；负责人收到群通知与会话提醒；
// 作废之后只能留言，留言照常通知另一方；不再进收件箱。
func TestReporterCancelsOpenIssue(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActCancel, ExpectedVersion: is.Version}); err == nil {
		t.Fatal("作废必须写理由")
	}
	if _, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActCancel, ExpectedVersion: is.Version, Note: "不归我"}); err == nil {
		t.Fatal("负责人不能作废")
	}
	got, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActCancel, ExpectedVersion: is.Version, Note: "重复提交，见 #1"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != issue.StatusCancelled {
		t.Fatalf("应为已作废：%s", got.Status)
	}
	if ev := lastEventOf(t, e.env, is.ID); ev.Action != issue.ActCancel || ev.Note != "重复提交，见 #1" || ev.Payload["by_admin"] != nil {
		t.Fatalf("动态里应记下作废与理由：%+v", ev)
	}
	ns := pending(t, e.env, is.ID)
	if last := ns[len(ns)-1]; last.event != core.EventCancelled || !sameIDs(last.recipients, []int64{e.b.MemberID}) {
		t.Fatalf("应告知负责人：%+v", ns)
	}
	as := alertsOf(t, e.env, is.ID)
	if last := as[len(as)-1]; last.kind != core.AlertCancelled || last.to != e.b.MemberID {
		t.Fatalf("负责人的会话提醒：%+v", as)
	}
	// 之后只能留言
	for _, a := range []issue.Action{issue.ActAccept, issue.ActDecline, issue.ActCancel} {
		who := e.b
		if a == issue.ActCancel {
			who = e.a
		}
		if _, err := e.svc.Act(ctx, who, core.ActInput{IssueID: is.ID, Action: a, ExpectedVersion: got.Version, Note: "x", Assignee: "lee-admin"}); err == nil {
			t.Fatalf("已作废的单不能 %s", a)
		}
	}
	if _, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActComment, Note: "为什么作废？"}); err != nil {
		t.Fatalf("已作废的单可以留言：%v", err)
	}
	ns = pending(t, e.env, is.ID)
	if last := ns[len(ns)-1]; last.event != core.EventCommented || !sameIDs(last.recipients, []int64{e.a.MemberID}) {
		t.Fatalf("留言照常通知另一方：%+v", ns)
	}
	inbox, err := e.svc.Inbox(ctx, e.b)
	if err != nil || len(inbox) != 0 {
		t.Fatalf("已作废的单不进收件箱：%v %v", inbox, err)
	}
}

// 已接单之后发起人不能再作废（网页上也不行）；管理员可以作废，双方都收到通知，并记一条管理操作记录；
// 免密登录的网页会话不能走管理员作废。
func TestAdminCancelsAcceptedIssue(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	is := e.act(t, e.b, e.act(t, e.a, e.draft(t), issue.ActSubmit), issue.ActAccept)
	act, who := e.web(t, e.a)
	var te *issue.TransitionError
	if _, err := e.svc.CancelIssue(ctx, act, who, core.CancelInput{IssueID: is.ID, ExpectedVersion: is.Version, Reason: "不要了"}); !errors.As(err, &te) {
		t.Fatalf("已接单后发起人不能作废：%v", err)
	}
	if _, err := e.svc.CancelIssue(ctx, core.WebActor(e.acc), e.c, core.CancelInput{IssueID: is.ID, ExpectedVersion: is.Version,
		Reason: "需求取消", PartyOnly: true}); !errors.As(err, &te) {
		t.Fatalf("免密会话不能走管理员作废：%v", err)
	}
	if !e.svc.CanCancel(ctx, &e.acc, e.c, is) || e.svc.CanCancel(ctx, nil, e.c, is) || e.svc.CanCancel(ctx, act.Account, who, is) {
		t.Fatal("CanCancel：管理员能、免密会话与发起人（已接单）不能")
	}
	var ie *core.InputError
	if _, err := e.svc.CancelIssue(ctx, core.WebActor(e.acc), e.c, core.CancelInput{IssueID: is.ID, ExpectedVersion: is.Version, Reason: "  "}); !errors.As(err, &ie) {
		t.Fatalf("网页、命令行作废也必须写理由：%v", err)
	}
	before := len(allEvents(t, e.svc))
	got, err := e.svc.CancelIssue(ctx, core.WebActor(e.acc), e.c, core.CancelInput{IssueID: is.ID, ExpectedVersion: is.Version, Reason: "需求取消"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != issue.StatusCancelled || got.Version != is.Version+1 {
		t.Fatalf("应为已作废、版本加一：%+v", got)
	}
	if ev := lastEventOf(t, e.env, is.ID); ev.ActorMemberID != e.c.MemberID || ev.Note != "需求取消" || ev.Payload["by_admin"] != true ||
		ev.FromStatus != issue.StatusAccepted {
		t.Fatalf("动态：%+v", ev)
	}
	ns := pending(t, e.env, is.ID)
	if last := ns[len(ns)-1]; last.event != core.EventCancelled || !sameIDs(last.recipients, []int64{e.a.MemberID, e.b.MemberID}) {
		t.Fatalf("管理员作废告知双方：%+v", ns)
	}
	var to []int64
	for _, a := range alertsOf(t, e.env, is.ID) {
		if a.kind == core.AlertCancelled {
			to = append(to, a.to)
		}
	}
	if !sameIDs(to, []int64{e.a.MemberID, e.b.MemberID}) {
		t.Fatalf("双方的会话提醒：%v", to)
	}
	es := allEvents(t, e.svc)
	if len(es) != before+1 || es[len(es)-1].Action != core.ActIssueCancel {
		t.Fatalf("应记一条管理操作记录：%d → %d", before, len(es))
	}
	// 普通成员不是管理员：报状态机的原因
	if _, err := e.svc.CancelIssue(ctx, act, who, core.CancelInput{IssueID: is.ID, ExpectedVersion: got.Version, Reason: "x"}); !errors.As(err, &te) {
		t.Fatalf("已作废的单不能再作废：%v", err)
	}
}

// 管理员也不能作废已关闭、已退回的单（已经有了结论）；版本不对按版本冲突处理。
func TestAdminCannotCancelFinished(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	if _, err := e.svc.CancelIssue(ctx, core.WebActor(e.acc), e.c, core.CancelInput{IssueID: is.ID, ExpectedVersion: is.Version + 5, Reason: "x"}); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("版本不对应报冲突：%v", err)
	}
	declined, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActDecline, ExpectedVersion: is.Version, Note: "不归我"})
	if err != nil {
		t.Fatal(err)
	}
	var se *core.StateError
	if _, err := e.svc.CancelIssue(ctx, core.WebActor(e.acc), e.c, core.CancelInput{IssueID: is.ID, ExpectedVersion: declined.Version, Reason: "x"}); !errors.As(err, &se) {
		t.Fatalf("已退回的单不能作废：%v", err)
	}
	if e.svc.CanCancel(ctx, &e.acc, e.c, declined) {
		t.Fatal("已退回的单不显示作废")
	}
}

// 发起人作废自己发给自己的单：没有别人可告知，群里照样留一条（只写名字），不写会话提醒。
func TestCancelSelfIssue(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "自己的单", Owner: "lee-claude", Actual: "x"})
	if err != nil {
		t.Fatal(err)
	}
	is := e.act(t, e.a, d, issue.ActSubmit)
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActCancel, ExpectedVersion: is.Version, Note: "不用了"}); err != nil {
		t.Fatal(err)
	}
	ns := pending(t, e.env, is.ID)
	if last := ns[len(ns)-1]; last.event != core.EventCancelled || !sameIDs(last.recipients, []int64{e.a.MemberID}) {
		t.Fatalf("群里照样留一条：%+v", ns)
	}
	if as := alertsOf(t, e.env, is.ID); len(as) != 0 {
		t.Fatalf("操作者本人不收会话提醒：%+v", as)
	}
}

// 已作废的单不再参与超时催办。
func TestCancelledNotReminded(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActCancel, ExpectedVersion: is.Version, Note: "重复"}); err != nil {
		t.Fatal(err)
	}
	ago(t, e.env, is.ID, 100)
	if n := runReminders(t, e.env); n != 0 || len(overdueSent(t, e.env, is.ID)) != 0 {
		t.Fatalf("已作废的单不催办：%d", n)
	}
}

// 看板上有已作废的单（网页默认隐藏），状态大类是已结束、没有处理人。
func TestBoardKeepsCancelled(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActCancel, ExpectedVersion: is.Version, Note: "重复"}); err != nil {
		t.Fatal(err)
	}
	_, items, err := e.svc.Board(ctx, e.b)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Status != issue.StatusCancelled || items[0].HolderID != nil || items[0].Needs == "" {
		t.Fatalf("看板：%+v", items)
	}
	// 和已关闭一样，只留最近 7 天的
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE issues SET updated_at = now() - interval '8 days' WHERE id = $1`, is.ID); err != nil {
		t.Fatal(err)
	}
	if _, items, err = e.svc.Board(ctx, e.b); err != nil || len(items) != 0 {
		t.Fatalf("8 天前作废的单不上看板：%+v %v", items, err)
	}
}

// 删除草稿：只有发起人能删自己的草稿，连同留言与幂等记录一起删掉、不留痕；已发出的单不能删。
func TestDeleteDraft(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "草稿", Actual: "x", IdempotencyKey: "k-draft"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: d.ID, Action: issue.ActComment, Note: "备注"}); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteDraft(ctx, e.b, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("别人看不见我的草稿，也删不了：%v", err)
	}
	if err := e.svc.DeleteDraft(ctx, e.a, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Get(ctx, e.a, d.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("删掉后查不到：%v", err)
	}
	var n int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM issue_events WHERE issue_id = $1) + (SELECT count(*) FROM idempotency WHERE issue_id = $1)`, d.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("附属行应一并删除：%d %v", n, err)
	}
	// 同一个幂等键可以再起草一张（旧记录已删）
	if _, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "再来", Actual: "x", IdempotencyKey: "k-draft"}); err != nil {
		t.Fatal(err)
	}
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	var se *core.StateError
	if err := e.svc.DeleteDraft(ctx, e.a, is.ID); !errors.As(err, &se) {
		t.Fatalf("已发出的单不能删：%v", err)
	}
}
