package core_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// alertsAfter 返回编号大于 last 的全部提醒（不分问题单），用来看“这一步”写了什么。
func alertsAfter(t *testing.T, e env, last int64) ([]alertRow, int64) {
	t.Helper()
	rows, err := e.svc.Pool.Query(context.Background(),
		`SELECT id, member_id, kind, actor_member_id FROM member_alerts WHERE id > $1 ORDER BY id`, last)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []alertRow
	for rows.Next() {
		var r alertRow
		if err := rows.Scan(&last, &r.to, &r.kind, &r.actor); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out, last
}

// 每一类动作各自提醒谁、不提醒谁：发起人 a、负责人 b、第三人 c 三个人，逐步执行并逐条断言这一步写下的提醒。
// 发起人与负责人是不同的人，操作者本人不收这条规则不会把收件人写错掩盖掉。
func TestAlertRecipientsByAction(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	q := e.svc.Pool
	ws, _ := store.DefaultWorkspace(ctx, q)
	cm, err := store.CreateMember(ctx, q, ws, "lee-third", "")
	if err != nil {
		t.Fatal(err)
	}
	plain, _, err := store.IssueToken(ctx, q, cm.ID, "codex")
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.LookupToken(ctx, q, plain)
	if err != nil {
		t.Fatal(err)
	}
	a, b := e.a, e.b
	name := map[int64]string{a.MemberID: "发起人", b.MemberID: "负责人", c.MemberID: "第三人"}
	var last int64
	step := func(what string, want []alertRow, do func()) {
		t.Helper()
		do()
		var got []alertRow
		got, last = alertsAfter(t, e, last)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			show := func(rs []alertRow) string {
				s := ""
				for _, r := range rs {
					s += fmt.Sprintf("[给%s %s 操作者%s]", name[r.to], r.kind, name[r.actor])
				}
				return s
			}
			t.Fatalf("%s：应写 %s，实际 %s", what, show(want), show(got))
		}
	}
	act := func(who store.Identity, is *store.Issue, in core.ActInput) {
		t.Helper()
		in.IssueID, in.ExpectedVersion = is.ID, is.Version
		out, err := e.svc.Act(ctx, who, in)
		if err != nil {
			t.Fatalf("%s 执行 %s：%v", who.MemberName, in.Action, err)
		}
		*is = out
	}
	do := func(who store.Identity, is *store.Issue, action issue.Action) func() {
		return func() { act(who, is, core.ActInput{Action: action}) }
	}

	// 第一张单：发出、退回
	x := e.draft(t) // a 起草，负责人按服务是 b
	step("起草", nil, func() {})
	step("发出 → 负责人", []alertRow{{b.MemberID, core.AlertAssigned, a.MemberID}}, do(a, &x, issue.ActSubmit))
	step("退回 → 发起人", []alertRow{{a.MemberID, core.AlertDeclined, b.MemberID}},
		func() { act(b, &x, core.ActInput{Action: issue.ActDecline, Note: "不是我的"}) })

	// 第二张单：转交、接单、开始修复、评论、请求验证、重开、关闭
	y := e.draft(t)
	step("发出 → 负责人", []alertRow{{b.MemberID, core.AlertAssigned, a.MemberID}}, do(a, &y, issue.ActSubmit))
	step("转交 → 新负责人（原负责人、发起人不收）", []alertRow{{c.MemberID, core.AlertAssigned, b.MemberID}},
		func() { act(b, &y, core.ActInput{Action: issue.ActTransfer, Assignee: "lee-third", Note: "归你"}) })
	step("接单 → 发起人", []alertRow{{a.MemberID, core.AlertAccepted, c.MemberID}}, do(c, &y, issue.ActAccept))
	step("开始修复：不提醒", nil, do(c, &y, issue.ActStart))
	step("评论：不提醒", nil, func() {
		if _, err := e.svc.Act(ctx, c, core.ActInput{IssueID: y.ID, Action: issue.ActComment, Note: "进展"}); err != nil {
			t.Fatal(err)
		}
	})
	step("请求验证 → 发起人", []alertRow{{a.MemberID, core.AlertVerify, c.MemberID}}, do(c, &y, issue.ActRequestVerification))
	step("重开 → 负责人", []alertRow{{c.MemberID, core.AlertReopened, a.MemberID}}, do(a, &y, issue.ActReopen))
	step("请求验证 → 发起人", []alertRow{{a.MemberID, core.AlertVerify, c.MemberID}}, do(c, &y, issue.ActRequestVerification))
	step("关闭 → 负责人", []alertRow{{c.MemberID, core.AlertClosed, a.MemberID}}, do(a, &y, issue.ActClose))

	// 第三张单：重开到阈值时升级，提醒负责人；发起人是操作者，不收
	z := e.draft(t)
	step("发出", []alertRow{{b.MemberID, core.AlertAssigned, a.MemberID}}, do(a, &z, issue.ActSubmit))
	step("接单", []alertRow{{a.MemberID, core.AlertAccepted, b.MemberID}}, do(b, &z, issue.ActAccept))
	for i := 1; i <= core.EscalateReopenCount; i++ {
		step("请求验证", []alertRow{{a.MemberID, core.AlertVerify, b.MemberID}}, do(b, &z, issue.ActRequestVerification))
		want := []alertRow{{b.MemberID, core.AlertReopened, a.MemberID}}
		if i == core.EscalateReopenCount {
			want = append(want, alertRow{b.MemberID, core.AlertEscalated, a.MemberID})
		}
		step(fmt.Sprintf("第 %d 次重开", i), want, do(a, &z, issue.ActReopen))
	}
	step("再请求验证", []alertRow{{a.MemberID, core.AlertVerify, b.MemberID}}, do(b, &z, issue.ActRequestVerification))
	step("升级过后再重开不再升级", []alertRow{{b.MemberID, core.AlertReopened, a.MemberID}}, do(a, &z, issue.ActReopen))
}

// 管理员改派：只提醒新接手人；被移出的人、做改派的管理员都不收。
func TestAlertRecipientsOnReassign(t *testing.T) {
	ctx := context.Background()
	h := newHandover(t)
	p, svc := h.p, h.p.e.svc
	e := env{svc: svc}
	_, last := alertsAfter(t, e, 0)
	if err := svc.ApplyHandover(ctx, core.WebActor(p.payAdmin), p.pay, p.annPay.ID, h.fullPlan(h.bob.ID), true); err != nil {
		t.Fatal(err)
	}
	got, _ := alertsAfter(t, e, last)
	want := []alertRow{
		{h.bob.ID, core.AlertAssigned, h.payAdminMember.ID}, // ann 负责的单改给 bob
		{h.bob.ID, core.AlertVerify, h.payAdminMember.ID},   // ann 发起、待验证的单改给 bob
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("改派应只提醒新接手人：%v，实际 %v（ann=%d 管理员=%d）", want, got, p.annPay.ID, h.payAdminMember.ID)
	}

	// 管理员把单改派给自己：自己不收
	h2 := newHandover(t)
	e2 := env{svc: h2.p.e.svc}
	_, last2 := alertsAfter(t, e2, 0)
	if err := h2.p.e.svc.ApplyHandover(ctx, core.WebActor(h2.p.payAdmin), h2.p.pay, h2.p.annPay.ID, h2.fullPlan(h2.payAdminMember.ID), true); err != nil {
		t.Fatal(err)
	}
	if got, _ := alertsAfter(t, e2, last2); len(got) != 0 {
		t.Fatalf("管理员改派给自己不应提醒自己：%v", got)
	}
}
