package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// ago 把问题单的计时起点往前拨 hours 小时（模拟时间流逝；用数据库时间）。
func ago(t *testing.T, e env, issueID int64, hours int) {
	t.Helper()
	if _, err := e.svc.Pool.Exec(context.Background(),
		`UPDATE issues SET status_since = now() - make_interval(hours => $2) - interval '1 second' WHERE id = $1`, issueID, hours); err != nil {
		t.Fatal(err)
	}
}

func remindLevel(t *testing.T, e env, issueID int64) int {
	t.Helper()
	var l int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT remind_level FROM issues WHERE id = $1`, issueID).Scan(&l); err != nil {
		t.Fatal(err)
	}
	return l
}

func runReminders(t *testing.T, e env) int {
	t.Helper()
	n, err := e.svc.QueueReminders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// overdueSent 返回问题单的催办通知（事件与收件人）。
func overdueSent(t *testing.T, e env, issueID int64) []sent {
	t.Helper()
	var out []sent
	for _, n := range pending(t, e, issueID) {
		if n.event == core.EventOverdue || n.event == core.EventOverdueAdmin {
			out = append(out, n)
		}
	}
	return out
}

func overdueAlerts(t *testing.T, e env, issueID int64) []int64 {
	t.Helper()
	rows, err := e.svc.Pool.Query(context.Background(), `SELECT member_id FROM member_alerts WHERE issue_id = $1 AND kind = 'overdue' ORDER BY id`, issueID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

// 待接单：到点再 @ 负责人；再过一个周期同时 @ 管理员；之后不再发。同一档不重复。
func TestRemindersTwoLevelsThenStop(t *testing.T) {
	e := newEnv(t)
	enableNotify(t, e)
	ctx := context.Background()
	if err := store.SetMemberRole(ctx, e.svc.Pool, e.a.MemberID, store.RoleAdmin); err != nil { // 发起人兼管理员
		t.Fatal(err)
	}
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)

	ago(t, e, is.ID, 3) // 默认 4 小时，还没到
	if n := runReminders(t, e); n != 0 || len(overdueSent(t, e, is.ID)) != 0 {
		t.Fatalf("没到 4 小时不催：%d %v", n, overdueSent(t, e, is.ID))
	}
	ago(t, e, is.ID, 4)
	runReminders(t, e)
	runReminders(t, e) // 同一档不重复
	got := overdueSent(t, e, is.ID)
	if len(got) != 1 || got[0].event != core.EventOverdue || !sameIDs(got[0].recipients, []int64{e.b.MemberID}) || remindLevel(t, e, is.ID) != 1 {
		t.Fatalf("到点再 @ 负责人一次：%v", got)
	}
	ago(t, e, is.ID, 8)
	runReminders(t, e)
	got = overdueSent(t, e, is.ID)
	if len(got) != 2 || got[1].event != core.EventOverdueAdmin || !sameIDs(got[1].recipients, []int64{e.b.MemberID, e.a.MemberID}) {
		t.Fatalf("再过一个周期同时 @ 管理员（当事人在前）：%v", got)
	}
	ago(t, e, is.ID, 100)
	if n := runReminders(t, e); n != 0 || len(overdueSent(t, e, is.ID)) != 2 {
		t.Fatalf("第 2 档之后不再发：%v", overdueSent(t, e, is.ID))
	}
	// 会话内提醒：两档都提醒当事人
	if a := overdueAlerts(t, e, is.ID); !sameIDs(a, []int64{e.b.MemberID, e.b.MemberID}) {
		t.Fatalf("会话内提醒当事人两次：%v", a)
	}
}

// 停机很久后一次就到第 2 档：只发一条（同时 @ 管理员），不会第 1、2 档连发。
func TestRemindersJumpLevel(t *testing.T) {
	e := newEnv(t)
	enableNotify(t, e)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	ago(t, e, is.ID, 30)
	runReminders(t, e)
	runReminders(t, e)
	if got := overdueSent(t, e, is.ID); len(got) != 1 || got[0].event != core.EventOverdueAdmin {
		t.Fatalf("应只发一条第 2 档：%v", got)
	}
}

// 状态一变就重新计时；“修复中”不计时；待验证催的是发起人；重开后再进待验证重新计时。
func TestRemindersRestartOnStatusChange(t *testing.T) {
	e := newEnv(t)
	enableNotify(t, e)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	ago(t, e, is.ID, 5)
	runReminders(t, e) // 待接单第 1 档
	is = e.act(t, e.b, is, issue.ActAccept)
	if remindLevel(t, e, is.ID) != 0 {
		t.Fatal("状态一变档位归零")
	}
	is = e.act(t, e.b, is, issue.ActStart)
	ago(t, e, is.ID, 500)
	if n := runReminders(t, e); n != 0 {
		t.Fatal("修复中不计时")
	}
	is = e.act(t, e.b, is, issue.ActRequestVerification)
	ago(t, e, is.ID, 23)
	if n := runReminders(t, e); n != 0 {
		t.Fatal("刚进待验证，计时从头算（默认 24 小时）")
	}
	ago(t, e, is.ID, 24)
	runReminders(t, e)
	got := overdueSent(t, e, is.ID)
	if len(got) != 2 || !sameIDs(got[1].recipients, []int64{e.a.MemberID}) {
		t.Fatalf("待验证催发起人：%v", got)
	}
	ago(t, e, is.ID, 30) // 待验证已等了 30 小时（第 1 档发过，第 2 档还没扫到）
	is = e.act(t, e.a, is, issue.ActReopen)
	is = e.act(t, e.b, is, issue.ActRequestVerification)
	if remindLevel(t, e, is.ID) != 0 {
		t.Fatal("重新进入待验证，重新计时")
	}
	if n := runReminders(t, e); n != 0 {
		t.Fatal("重新进入待验证，计时从头算，不沿用上一轮的 30 小时")
	}
	ago(t, e, is.ID, 24)
	runReminders(t, e)
	if got := overdueSent(t, e, is.ID); len(got) != 3 || got[2].event != core.EventOverdue {
		t.Fatalf("新一轮从第 1 档开始：%v", got)
	}
}

// 转交给别人（仍是待接单）、管理员改派发起人（仍是待验证）：当事人换了，重新计时。
func TestRemindersRestartOnPartyChange(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	enableNotify(t, e)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	ago(t, e, is.ID, 5)
	runReminders(t, e)
	is, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActTransfer, ExpectedVersion: is.Version, Assignee: "lee-claude", Note: "前端的问题"})
	if err != nil {
		t.Fatal(err)
	}
	if remindLevel(t, e, is.ID) != 0 {
		t.Fatal("转交后重新计时")
	}
	if n := runReminders(t, e); n != 0 { // 计时起点也要重来，不能沿用转交前已等的 5 小时
		t.Fatal("转交后还没到点，不应马上又催")
	}
	// 改派发起人：直接用存储层（业务层的改派另有用例覆盖权限）
	ago(t, e, is.ID, 5)
	if err := store.SetIssueSource(ctx, e.svc.Pool, is.ID, e.b.MemberID); err != nil {
		t.Fatal(err)
	}
	if remindLevel(t, e, is.ID) != 0 {
		t.Fatal("发起人换了，重新计时")
	}
	if n := runReminders(t, e); n != 0 {
		t.Fatal("重新计时后还没到点")
	}
	// 同一个人“改派”给自己不算换人
	ago(t, e, is.ID, 5)
	runReminders(t, e)
	lvl := remindLevel(t, e, is.ID)
	if err := store.SetIssueSource(ctx, e.svc.Pool, is.ID, e.b.MemberID); err != nil || remindLevel(t, e, is.ID) != lvl || lvl == 0 {
		t.Fatalf("发起人没变不重新计时：%d %v", lvl, err)
	}
}

// 时长为 0 表示关闭；时长改了按新设置算；工作空间没配群：不写群通知，会话内提醒照发、档位照走（以后配群不会补发一批）。
func TestRemindersSettings(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	zero, one := 0, 1
	if err := e.svc.SetRemindHours(ctx, core.ServerCLI(), e.a.WorkspaceID, &zero, nil); err != nil {
		t.Fatal(err)
	}
	ago(t, e, is.ID, 100)
	if n := runReminders(t, e); n != 0 {
		t.Fatal("待接单时长为 0 时不催")
	}
	if err := e.svc.SetRemindHours(ctx, core.ServerCLI(), e.a.WorkspaceID, &one, nil); err != nil {
		t.Fatal(err)
	}
	ago(t, e, is.ID, 1)
	runReminders(t, e)
	if len(pending(t, e, is.ID)) != 0 {
		t.Fatal("没配群时不写群通知")
	}
	if a := overdueAlerts(t, e, is.ID); !sameIDs(a, []int64{e.b.MemberID}) || remindLevel(t, e, is.ID) != 1 {
		t.Fatalf("会话内提醒照发、档位照走：%v", a)
	}

	bad := 721
	var ie *core.InputError
	if err := e.svc.SetRemindHours(ctx, core.ServerCLI(), e.a.WorkspaceID, nil, &bad); !errors.As(err, &ie) {
		t.Fatalf("超过 720 小时应拒绝：%v", err)
	}
	if err := e.svc.SetRemindHours(ctx, core.WebActor(store.Account{ID: e.b.AccountID}), e.a.WorkspaceID, &one, nil); err == nil {
		t.Fatal("不是管理员不能改")
	}
	h := 6
	if err := e.svc.UpdateWorkspace(ctx, core.ServerCLI(), e.a.WorkspaceID, core.WorkspaceSettings{ShowTitle: true, RemindVerifyHours: &h}); err != nil {
		t.Fatal(err)
	}
	ws, _ := store.GetWorkspace(ctx, e.svc.Pool, e.a.WorkspaceID)
	if ws.Remind != (store.RemindConfig{OpenHours: 1, VerifyHours: 6}) {
		t.Fatalf("网页保存只改给出的项：%+v", ws.Remind)
	}
}
