package core_test

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// stepNotices 返回编号大于 lastN、lastA 的群通知与会话内提醒，按“事件:收件人”写成可比较的字符串。
func stepNotices(t *testing.T, e env, lastN, lastA int64) (group, alerts []string, actor int64, nN, nA int64) {
	t.Helper()
	ctx := context.Background()
	nN, nA = lastN, lastA
	rows, err := e.svc.Pool.Query(ctx, `SELECT id, event, recipients, actor_member_id FROM notifications WHERE id > $1 ORDER BY id`, lastN)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var ev string
		var to []int64
		if err := rows.Scan(&nN, &ev, &to, &actor); err != nil {
			t.Fatal(err)
		}
		for _, id := range to {
			group = append(group, fmt.Sprintf("%s:%d", ev, id))
		}
	}
	rows.Close()
	rows, err = e.svc.Pool.Query(ctx, `SELECT id, kind, member_id FROM member_alerts WHERE id > $1 ORDER BY id`, lastA)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var kind string
		var to int64
		if err := rows.Scan(&nA, &kind, &to); err != nil {
			t.Fatal(err)
		}
		alerts = append(alerts, fmt.Sprintf("%s:%d", kind, to))
	}
	rows.Close()
	sort.Strings(group)
	sort.Strings(alerts)
	return
}

// 信息链完整（N-4）：整轮里每一步，群通知与会话内提醒用同一份收件人清单——会话内提醒 = 群通知收件人去掉操作者本人；
// 每一步的期望收件人逐条写出，覆盖发出、转交、接单、开始修复、双方留言、请求验证、重开、关单、关单后留言、草稿留言。
func TestNoticesEveryStepBothPaths(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	enableNotify(t, e)
	q := e.svc.Pool
	ws, _ := store.DefaultWorkspace(ctx, q)
	cm, err := store.CreateMember(ctx, q, ws, "lee-third", "")
	if err != nil {
		t.Fatal(err)
	}
	plain, _, _ := store.IssueToken(ctx, q, cm.ID, "codex")
	c, err := store.LookupToken(ctx, q, plain)
	if err != nil {
		t.Fatal(err)
	}
	a, b := e.a, e.b
	var lastN, lastA int64
	check := func(what string, wantGroup []string, do func()) {
		t.Helper()
		do()
		var group, alerts []string
		var actor int64
		group, alerts, actor, lastN, lastA = stepNotices(t, e, lastN, lastA)
		sort.Strings(wantGroup)
		if strings.Join(group, ",") != strings.Join(wantGroup, ",") {
			t.Fatalf("%s：群通知应为 %v，实际 %v", what, wantGroup, group)
		}
		var wantAlerts []string
		for _, g := range group {
			if !strings.HasSuffix(g, fmt.Sprintf(":%d", actor)) {
				wantAlerts = append(wantAlerts, g)
			}
		}
		if strings.Join(alerts, ",") != strings.Join(wantAlerts, ",") {
			t.Fatalf("%s：会话内提醒应与群通知同口径（去掉操作者本人）%v，实际 %v", what, wantAlerts, alerts)
		}
	}
	ev := func(event string, id int64) string { return fmt.Sprintf("%s:%d", event, id) }
	is := e.draft(t) // a 起草，负责人 b
	comment := func(who store.Identity, note string) func() {
		return func() {
			if _, err := e.svc.Act(ctx, who, core.ActInput{IssueID: is.ID, Action: issue.ActComment, Note: note}); err != nil {
				t.Fatal(err)
			}
		}
	}
	act := func(who store.Identity, in core.ActInput) func() {
		return func() {
			in.IssueID = is.ID
			in.ExpectedVersion = is.Version
			out, err := e.svc.Act(ctx, who, in)
			if err != nil {
				t.Fatalf("%s %s：%v", who.MemberName, in.Action, err)
			}
			is = out
		}
	}
	check("草稿上留言：只有发起人自己看得见，不发", nil, comment(a, "备注"))
	check("发出 → 负责人", []string{ev("assigned", b.MemberID)}, act(a, core.ActInput{Action: issue.ActSubmit}))
	check("发起人留言 → 负责人", []string{ev("commented", b.MemberID)}, comment(a, "补充"))
	check("转交 → 新负责人；告知发起人", []string{ev("assigned", c.MemberID), ev("transferred", a.MemberID)},
		act(b, core.ActInput{Action: issue.ActTransfer, Assignee: "lee-third", Note: "归你"}))
	check("接单 → 告知发起人", []string{ev("accepted", a.MemberID)}, act(c, core.ActInput{Action: issue.ActAccept}))
	check("开始修复 → 告知发起人", []string{ev("started", a.MemberID)}, act(c, core.ActInput{Action: issue.ActStart}))
	check("负责人留言 → 发起人", []string{ev("commented", a.MemberID)}, comment(c, "进展"))
	is, _, _ = e.svc.Get(ctx, c, is.ID)
	check("请求验证 → 发起人", []string{ev("verify", a.MemberID)}, act(c, core.ActInput{Action: issue.ActRequestVerification}))
	check("重开 → 负责人", []string{ev("reopened", c.MemberID)}, act(a, core.ActInput{Action: issue.ActReopen}))
	check("再请求验证", []string{ev("verify", a.MemberID)}, act(c, core.ActInput{Action: issue.ActRequestVerification}))
	check("关单 → 告知负责人", []string{ev("closed", c.MemberID)}, act(a, core.ActInput{Action: issue.ActClose}))
	check("关单后留言照样通知另一方", []string{ev("commented", c.MemberID)}, comment(a, "谢谢"))
}

// 发起人与负责人是同一人时留言：没有别人可告知，不发；他给自己发的单开始修复：照常发、群里只写名字（N-3）。
func TestNoticesSelfIssue(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	enableNotify(t, e)
	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "自己的单", Owner: "lee-claude", Actual: "x"})
	if err != nil {
		t.Fatal(err)
	}
	is := e.act(t, e.a, d, issue.ActSubmit)
	is = e.act(t, e.a, is, issue.ActAccept)
	is = e.act(t, e.a, is, issue.ActStart)
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActComment, Note: "自言自语"}); err != nil {
		t.Fatal(err)
	}
	var events []string
	for _, n := range pending(t, e, is.ID) {
		events = append(events, n.event)
	}
	if strings.Join(events, ",") != "assigned,accepted,started" {
		t.Fatalf("自己的单：留言不发，其余照常：%v", events)
	}
	if got := alertsOf(t, e, is.ID); len(got) != 0 {
		t.Fatalf("操作者本人不收会话内提醒：%v", got)
	}
}

// 管理员改派（移出 ann）：新接手人收“交给你”“请验证”；被换下的 ann 只在群里 @、不写会话内提醒；
// 另一方是新接手人本人时不重复告知。
func TestNoticesOnReassign(t *testing.T) {
	ctx := context.Background()
	h := newHandover(t)
	p, svc := h.p, h.p.e.svc
	if err := store.SetWorkspaceNotify(ctx, svc.Pool, p.pay, store.NotifyConfig{Channel: "telegram", Target: "-100", ShowTitle: true}); err != nil {
		t.Fatal(err)
	}
	// 另找一个接手人 cara，这样 bob（两张单的另一方）是“没换的那一方”
	cara, err := store.CreateMember(ctx, svc.Pool, p.pay, "cara", "")
	if err != nil {
		t.Fatal(err)
	}
	e := env{svc: svc}
	lastN, lastA := int64(0), int64(0)
	_, _, _, lastN, lastA = stepNotices(t, e, lastN, lastA)
	if err := svc.ApplyHandover(ctx, core.WebActor(p.payAdmin), p.pay, p.annPay.ID, h.fullPlan(cara.ID), true); err != nil {
		t.Fatal(err)
	}
	group, alerts, _, _, _ := stepNotices(t, e, lastN, lastA)
	ann, bob, c := p.annPay.ID, h.bob.ID, cara.ID
	want := []string{
		fmt.Sprintf("assigned:%d", c), fmt.Sprintf("reassigned:%d", ann), fmt.Sprintf("reassigned:%d", bob), // ann 负责的单改给 cara
		fmt.Sprintf("verify:%d", c), fmt.Sprintf("reassigned:%d", ann), fmt.Sprintf("reassigned:%d", bob), // ann 发起、待验证的单改给 cara
	}
	sort.Strings(want)
	if strings.Join(group, ",") != strings.Join(want, ",") {
		t.Fatalf("改派的群通知：应为 %v，实际 %v", want, group)
	}
	wantAlerts := []string{fmt.Sprintf("assigned:%d", c), fmt.Sprintf("reassigned:%d", bob), fmt.Sprintf("verify:%d", c), fmt.Sprintf("reassigned:%d", bob)}
	sort.Strings(wantAlerts)
	if strings.Join(alerts, ",") != strings.Join(wantAlerts, ",") {
		t.Fatalf("改派的会话内提醒（被移出的 ann 不写）：应为 %v，实际 %v", wantAlerts, alerts)
	}
}
