package core_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// 归档与彻底删除（WS-1）的测试准备：在作废测试的环境上，把 lee-admin 的账号再授予服务器管理员用于彻底删除。
func (e cancelEnv) archive(t *testing.T) {
	t.Helper()
	open, err := store.CountUnfinishedIssues(context.Background(), e.svc.Pool, e.a.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ArchiveWorkspace(context.Background(), core.WebActor(e.acc), e.a.WorkspaceID, open); err != nil {
		t.Fatal(err)
	}
}

func archivedErr(err error) bool {
	var ae *core.ArchivedError
	return errors.As(err, &ae)
}

// 归档要带未结束数确认：不符时报错并附实际数量；符合才归档；重复归档、非管理员都被拒；留一条记录。
func TestArchiveNeedsOpenIssueCount(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	e.act(t, e.a, e.draft(t), issue.ActSubmit) // 一张待接单
	_ = e.draft(t)                              // 一张草稿，不算未结束
	ws := e.a.WorkspaceID

	var oe *core.OpenIssuesError
	if _, err := e.svc.ArchiveWorkspace(ctx, core.WebActor(e.acc), ws, 0); !errors.As(err, &oe) || oe.Count != 1 {
		t.Fatalf("确认数 0 与实际 1 不符应报 OpenIssuesError：%v", err)
	}
	if oe.ErrorCode() != "open_issues_unconfirmed" {
		t.Errorf("错误码：%s", oe.ErrorCode())
	}
	member, _ := store.AccountByID(ctx, e.svc.Pool, e.a.AccountID)
	if _, err := e.svc.ArchiveWorkspace(ctx, core.WebActor(member), ws, 1); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("普通成员不能归档：%v", err)
	}
	if _, err := e.svc.ArchiveWorkspace(ctx, core.ServerCLI(), ws, 1); err == nil {
		t.Fatal("命令行不归档")
	}
	got, err := e.svc.ArchiveWorkspace(ctx, core.WebActor(e.acc), ws, 1)
	if err != nil || got.ArchivedAt == nil || got.ArchivedBy == nil || *got.ArchivedBy != e.acc.ID {
		t.Fatalf("归档应成功并记下时间与操作人：%+v %v", got, err)
	}
	var se *core.StateError
	if _, err := e.svc.ArchiveWorkspace(ctx, core.WebActor(e.acc), ws, 1); !errors.As(err, &se) {
		t.Fatalf("重复归档应报状态冲突：%v", err)
	}
	var ev store.AdminEvent
	for _, x := range allEvents(t, e.svc) {
		if x.Action == core.ActWorkspaceArchive {
			ev = x
		}
	}
	if ev.Action == "" || num(ev, "open_issues") != 1 || ev.ActorName != e.acc.Name {
		t.Fatalf("应留下归档记录并附未结束数：%+v", ev)
	}
	if _, err := e.svc.UnarchiveWorkspace(ctx, core.WebActor(member), ws); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("普通成员不能取消归档：%v", err)
	}
	if _, err := e.svc.UnarchiveWorkspace(ctx, core.WebActor(e.acc), ws); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UnarchiveWorkspace(ctx, core.WebActor(e.acc), ws); !errors.As(err, &se) {
		t.Fatalf("没归档就取消归档应报状态冲突：%v", err)
	}
}

func num(e store.AdminEvent, k string) int {
	f, _ := e.Detail[k].(float64)
	return int(f)
}

// 已作废、已关闭、已退回的不算未结束，不需要确认。
func TestArchiveIgnoresFinishedIssues(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActCancel, ExpectedVersion: is.Version, Note: "撤回"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.ArchiveWorkspace(ctx, core.WebActor(e.acc), e.a.WorkspaceID, 0); err != nil {
		t.Fatalf("只有已作废的单子时确认数 0 应通过：%v", err)
	}
}

// 归档后所有写操作被拒（409 的 ArchivedError），读照常；吊销类操作仍允许；取消归档后恢复。
func TestArchivedWorkspaceIsReadOnly(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	ws := e.a.WorkspaceID
	adm := core.WebActor(e.acc)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	draft := e.draft(t)
	inv, err := e.svc.CreateInvite(ctx, adm, ws, core.InviteSpec{Kind: store.InvitePersonal})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := e.svc.IssueToken(ctx, adm, ws, "lee-codex", "codex")
	if err != nil {
		t.Fatal(err)
	}
	e.archive(t)

	bad := func(name string, err error) {
		t.Helper()
		if !archivedErr(err) {
			t.Errorf("%s：归档后应被拒绝（workspace_archived），得到 %v", name, err)
		}
	}
	_, err = e.svc.Draft(ctx, e.a, core.DraftInput{Title: "新单"})
	bad("起草", err)
	_, err = e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActAccept, ExpectedVersion: is.Version})
	bad("接单", err)
	_, err = e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActComment, Note: "留言"})
	bad("留言", err)
	_, err = e.svc.CancelIssue(ctx, adm, e.c, core.CancelInput{IssueID: is.ID, ExpectedVersion: is.Version, Reason: "x"})
	bad("作废", err)
	bad("删草稿", e.svc.DeleteDraft(ctx, e.a, draft.ID))
	bad("改设置", e.svc.UpdateWorkspace(ctx, adm, ws, core.WorkspaceSettings{ShowTitle: true}))
	bad("改显示名", e.svc.SetWorkspaceDisplayName(ctx, adm, ws, "新名字"))
	bad("改语言", e.svc.SetWorkspaceLanguage(ctx, adm, ws, "en"))
	h := 5
	bad("改催办", e.svc.SetRemindHours(ctx, adm, ws, &h, nil))
	_, err = e.svc.CreateInvite(ctx, adm, ws, core.InviteSpec{Kind: store.InviteTeam})
	bad("建邀请", err)
	_, err = e.svc.AddMember(ctx, adm, ws, "lee-codex", store.RoleMember)
	bad("加成员", err)
	_, err = e.svc.IssueToken(ctx, adm, ws, "lee-codex", "codex")
	bad("签发令牌", err)
	_, err = e.svc.SetServiceOwner(ctx, adm, ws, "web", "lee-codex")
	bad("登记服务", err)
	bad("改角色", e.svc.SetRole(ctx, adm, ws, e.b.MemberID, store.RoleAdmin))
	_, err = e.svc.StartChatBind(ctx, adm, ws, "dingtalk")
	bad("群绑定码", err)
	bad("解除群", e.svc.UnbindChat(ctx, adm, ws, "telegram"))
	_, err = e.svc.Register(ctx, core.RegisterInput{Code: inv.Code, Name: "newbie", Password: "pw-newbie-0123456789"})
	bad("凭邀请注册", err)
	acc := store.Account{ID: e.c.AccountID}
	_, err = e.svc.AcceptInvite(ctx, acc, inv.Code)
	bad("接受邀请", err)

	// 读照常：凭令牌仍能读单子与列表
	if got, _, err := e.svc.Get(ctx, e.a, is.ID); err != nil || got.ID != is.ID {
		t.Fatalf("归档后仍能读单子：%v", err)
	}
	if list, err := e.svc.List(ctx, e.a, "", ""); err != nil || len(list) == 0 {
		t.Fatalf("归档后仍能列单子：%v", err)
	}
	// 收权永远要能做
	if err := e.svc.RevokeToken(ctx, adm, ws, tok.ID); err != nil {
		t.Fatalf("归档后应仍能吊销令牌：%v", err)
	}
	if err := e.svc.RevokeInvite(ctx, adm, ws, inv.Invite.ID); err != nil {
		t.Fatalf("归档后应仍能作废邀请：%v", err)
	}

	if _, err := e.svc.UnarchiveWorkspace(ctx, adm, ws); err != nil {
		t.Fatal(err)
	}
	e.act(t, e.b, is, issue.ActAccept) // 取消归档后恢复读写
}

// 归档后：到点的单子不再催办；待发的群通知被跳过；收件箱与会话提醒为空；取消归档后重新计时，不一下子补催。
func TestArchivedStopsRemindersAndNotifications(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	ws := e.a.WorkspaceID
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)

	if items, err := e.svc.Inbox(ctx, e.b); err != nil || len(items) != 1 {
		t.Fatalf("归档前负责人收件箱应有 1 张：%d %v", len(items), err)
	}
	if before, err := e.svc.Alerts(ctx, e.b, nil); err != nil || before.PendingCount != 1 {
		t.Fatalf("归档前会话待办应为 1：%+v %v", before, err)
	}
	pend, _, err := store.CountNotifications(ctx, e.svc.Pool, ws)
	if err != nil || pend == 0 {
		t.Fatalf("归档前应有待发通知：%d %v", pend, err)
	}
	ago(t, e.env, is.ID, 5) // 已超过默认 4 小时
	e.archive(t)

	if n := runReminders(t, e.env); n != 0 {
		t.Fatalf("归档后不应催办：%d", n)
	}
	if got := overdueSent(t, e.env, is.ID); len(got) != 0 {
		t.Fatalf("归档后不应写催办通知：%v", got)
	}
	pend, _, err = store.CountNotifications(ctx, e.svc.Pool, ws)
	if err != nil || pend != 0 {
		t.Fatalf("归档应把待发通知跳过：%d %v", pend, err)
	}
	var skipped int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications WHERE workspace_id = $1 AND status = 'skipped'`, ws).Scan(&skipped); err != nil || skipped == 0 {
		t.Fatalf("待发通知应标为 skipped：%d %v", skipped, err)
	}
	if items, err := e.svc.Inbox(ctx, e.b); err != nil || len(items) != 0 {
		t.Fatalf("归档后收件箱应为空：%d %v", len(items), err)
	}
	if a, err := e.svc.Alerts(ctx, e.b, nil); err != nil || a.PendingCount != 0 || len(a.PendingTop) != 0 || len(a.Items) != 0 {
		t.Fatalf("归档后会话提醒应为空：%+v %v", a, err)
	}
	if a, err := e.svc.Alerts(ctx, e.b, new(int64)); err != nil || len(a.Items) != 0 {
		t.Fatalf("归档后补查提醒应为空：%+v %v", a, err)
	}

	if _, err := e.svc.UnarchiveWorkspace(ctx, core.WebActor(e.acc), ws); err != nil {
		t.Fatal(err)
	}
	if n := runReminders(t, e.env); n != 0 {
		t.Fatalf("取消归档后应重新计时，不补催：%d", n)
	}
	if items, err := e.svc.Inbox(ctx, e.b); err != nil || len(items) != 1 {
		t.Fatalf("取消归档后收件箱恢复：%d %v", len(items), err)
	}
}

// 成员资格带上归档标记；服务器管理页的工作空间列表带归档时间与问题单数。
func TestArchivedFlagsInListings(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	root := e.acc
	if err := store.SetServerAdmin(ctx, e.svc.Pool, root.ID, true); err != nil {
		t.Fatal(err)
	}
	root.ServerAdmin = true
	e.act(t, e.a, e.draft(t), issue.ActSubmit)
	_ = e.draft(t)
	e.archive(t)

	ms, err := e.svc.Memberships(ctx, e.acc.ID)
	if err != nil || len(ms) != 1 || !ms[0].Archived {
		t.Fatalf("成员资格应标已归档：%+v %v", ms, err)
	}
	list, err := e.svc.ServerWorkspaces(ctx, &root)
	if err != nil || len(list) != 1 {
		t.Fatal(list, err)
	}
	w := list[0]
	if !w.Archived() || w.Issues != 2 || w.Unfinished != 1 {
		t.Fatalf("列表应带归档、总单数 2（含草稿）、未结束 1：archived=%v issues=%d unfinished=%d", w.Archived(), w.Issues, w.Unfinished)
	}
	if n, err := e.svc.UnfinishedIssues(ctx, &root, e.a.WorkspaceID); err != nil || n != 1 {
		t.Fatalf("未结束数：%d %v", n, err)
	}
}

// 彻底删除：只有服务器管理员；确认名字要一致；有未结束的单子拒绝；成功后问题单与草稿全删，管理记录保留并新增一条。
func TestPurgeWorkspace(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	ws, err := store.GetWorkspace(ctx, e.svc.Pool, e.a.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	root := e.acc
	adm := core.WebActor(root)

	if _, err := e.svc.PurgeWorkspace(ctx, adm, ws.Name, ws.Name); !errors.Is(err, core.ErrNotServerAdmin) {
		t.Fatalf("工作空间管理员不是服务器管理员，不能彻底删除：%v", err)
	}
	if err := store.SetServerAdmin(ctx, e.svc.Pool, root.ID, true); err != nil {
		t.Fatal(err)
	}
	var pce *core.PurgeConfirmError
	if _, err := e.svc.PurgeWorkspace(ctx, adm, ws.Name, "other"); !errors.As(err, &pce) || pce.ErrorCode() != "purge_confirm_mismatch" {
		t.Fatalf("确认名字不一致应拒绝：%v", err)
	}
	if _, err := e.svc.PurgeWorkspace(ctx, adm, "no-such", "no-such"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("工作空间不存在：%v", err)
	}

	// 一张走完（已关闭）、一张未结束、一张草稿
	done := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	done = e.act(t, e.b, done, issue.ActAccept)
	done = e.act(t, e.b, done, issue.ActStart)
	done = e.act(t, e.b, done, issue.ActRequestVerification)
	e.act(t, e.a, done, issue.ActClose)
	live := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	_ = e.draft(t)

	var se *core.StateError
	if _, err := e.svc.PurgeWorkspace(ctx, adm, ws.Name, ws.Name); !errors.As(err, &se) {
		t.Fatalf("有未结束的单子应拒绝：%v", err)
	}
	if _, err := e.svc.CancelIssue(ctx, adm, e.c, core.CancelInput{IssueID: live.ID, ExpectedVersion: live.Version, Reason: "不做了"}); err != nil {
		t.Fatal(err)
	}
	// 归档的工作空间也能彻底删除
	e.archive(t)
	before := len(allEvents(t, e.svc))

	res, err := e.svc.PurgeWorkspace(ctx, adm, ws.Name, ws.Name)
	if err != nil {
		t.Fatal(err)
	}
	if res.Issues != 2 || res.Drafts != 1 {
		t.Fatalf("应删 2 张问题单（关闭的与作废的）和 1 张草稿：%+v", res)
	}
	if _, err := store.GetWorkspace(ctx, e.svc.Pool, ws.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("工作空间应已删除：%v", err)
	}
	for _, tbl := range []string{"issues", "issue_events", "notifications", "member_alerts", "members", "tokens", "idempotency"} {
		var n int
		if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM `+tbl).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s 应已清空：%d %v", tbl, n, err)
		}
	}
	es := allEvents(t, e.svc)
	if len(es) != before+1 {
		t.Fatalf("管理记录只追加：原有 %d 条应保留，另加 1 条，得到 %d", before, len(es))
	}
	last := es[len(es)-1]
	if last.Action != core.ActWorkspacePurge || last.WorkspaceName != ws.Name || num(last, "issues") != 2 || num(last, "drafts") != 1 {
		t.Fatalf("应记下彻底删除与问题单、草稿数：%+v", last)
	}
	// 之前的归档记录仍在
	found := false
	for _, x := range es {
		found = found || x.Action == core.ActWorkspaceArchive
	}
	if !found {
		t.Fatal("归档记录应保留")
	}
}

// 彻底删除与进行中的写操作互斥：写操作先拿到工作空间共享锁，彻底删除排队等它结束，不会死锁。
func TestPurgeWaitsForWriters(t *testing.T) {
	ctx := context.Background()
	e := newCancelEnv(t)
	ws, _ := store.GetWorkspace(ctx, e.svc.Pool, e.a.WorkspaceID)
	if err := store.SetServerAdmin(ctx, e.svc.Pool, e.acc.ID, true); err != nil {
		t.Fatal(err)
	}
	// 手动开一个“写操作”：持有工作空间共享锁
	tx, err := e.svc.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := store.WorkspaceForShare(ctx, tx, ws.ID); err != nil {
		t.Fatal(err)
	}
	done := goErr(func() error {
		_, err := e.svc.PurgeWorkspace(ctx, core.WebActor(e.acc), ws.Name, ws.Name)
		return err
	})
	select {
	case err := <-done:
		t.Fatalf("写操作未结束时彻底删除应等待：%v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("写操作结束后彻底删除应成功：%v", err)
	}
}
