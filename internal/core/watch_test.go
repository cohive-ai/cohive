package core_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// 关注者（CC-1）的测试准备：在作废测试的环境上（a 发起人、b 负责人、c 管理员、已配群）再加
// w（拿来当关注者）和 o（不相干的成员，有昵称 Otto）。
type watchEnv struct {
	cancelEnv
	w, o store.Identity
}

func newWatchEnv(t *testing.T) watchEnv {
	t.Helper()
	ctx := context.Background()
	ce := newCancelEnv(t)
	mk := func(name, display string) store.Identity {
		m, err := store.CreateMember(ctx, ce.svc.Pool, ce.a.WorkspaceID, name, display)
		if err != nil {
			t.Fatal(err)
		}
		return store.Identity{AccountID: m.AccountID, MemberID: m.ID, WorkspaceID: m.WorkspaceID, MemberName: m.Name, Tool: "claude-code"}
	}
	return watchEnv{cancelEnv: ce, w: mk("lee-watch", ""), o: mk("lee-other", "Otto")}
}

// submit 起草并发出一张给 b 的单，cc 是发出时抄送的登录名。
func (e watchEnv) submit(t *testing.T, cc ...string) store.Issue {
	t.Helper()
	d := e.draft(t)
	got, err := e.svc.Act(context.Background(), e.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version, CC: cc})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func (e watchEnv) watchers(t *testing.T, id int64) []string {
	t.Helper()
	ms, err := e.svc.Watchers(context.Background(), e.a, id)
	if err != nil {
		t.Fatal(err)
	}
	return memberNamesOf(ms)
}

func memberNamesOf(ms []store.Member) []string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

func notifsOf(t *testing.T, e env, id int64) []store.Notification {
	t.Helper()
	list, err := store.ListNotificationsForIssue(context.Background(), e.svc.Pool, id)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

// lastNotif 返回最新一条群通知；先断言自 before 条以来恰好新增了一条（关注者不另起一行）。
func lastNotif(t *testing.T, e env, id int64, before int) store.Notification {
	t.Helper()
	list := notifsOf(t, e, id)
	if len(list) != before+1 {
		t.Fatalf("应新增恰好一条群通知：原有 %d，现有 %d", before, len(list))
	}
	return list[len(list)-1]
}

type alertFull struct {
	to    int64
	kind  string
	about string
	actor int64
}

func alertsFullOf(t *testing.T, e env, id int64) []alertFull {
	t.Helper()
	rows, err := e.svc.Pool.Query(context.Background(),
		`SELECT member_id, kind, about, actor_member_id FROM member_alerts WHERE issue_id = $1 ORDER BY id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []alertFull
	for rows.Next() {
		var r alertFull
		if err := rows.Scan(&r.to, &r.kind, &r.about, &r.actor); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func alertsFor(t *testing.T, e env, id, member int64) []alertFull {
	t.Helper()
	var out []alertFull
	for _, a := range alertsFullOf(t, e, id) {
		if a.to == member {
			out = append(out, a)
		}
	}
	return out
}

func ids(v ...int64) string { return fmt.Sprint(append([]int64{}, v...)) }

func sameList(got, want []int64) bool { return ids(got...) == ids(want...) }

// 起草时抄送：成为关注者，动态里记下；昵称、重名返回候选，不存在报错；抄送自己忽略；重复忽略。
func TestCCOnDraft(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "抄送", Owner: "lee-codex", CC: []string{"lee-watch", "lee-claude", "lee-watch"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := e.watchers(t, d.ID); strings.Join(got, ",") != "lee-watch" {
		t.Fatalf("抄送的人成为关注者，自己与重复忽略：%v", got)
	}
	_, evs, _ := e.svc.Get(ctx, e.a, d.ID)
	if cc, _ := evs[0].Payload["cc"].([]any); len(cc) != 1 || cc[0] != "lee-watch" {
		t.Fatalf("起草动态里应记下抄送：%v", evs[0].Payload)
	}

	var mce *core.MemberChoiceError
	if _, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "x", Owner: "lee-codex", CC: []string{"Otto"}}); !errors.As(err, &mce) || mce.Code != core.CodeMemberNickname {
		t.Fatalf("昵称应返回 MemberChoiceError：%v", err)
	}
	if _, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "x", Owner: "lee-codex", CC: []string{"nobody"}}); err == nil {
		t.Fatal("不存在的成员应报错")
	}
	// 报错的起草整个回滚，不留下半张单
	list, _ := e.svc.List(ctx, e.a, "x", "")
	if len(list) != 0 {
		t.Fatalf("报错的起草不应留下问题单：%d", len(list))
	}
}

// 发出时抄送：进 submit 动态；抄送的人和负责人同在一条群消息里（关注者挂在主通知上）；
// 非发出动作带 CC 报参数错。
func TestCCOnSubmit(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	is := e.submit(t, "lee-watch", "lee-claude")
	if got := e.watchers(t, is.ID); strings.Join(got, ",") != "lee-watch" {
		t.Fatalf("发出时抄送：%v", got)
	}
	if ev := lastEventOf(t, e.env, is.ID); ev.Action != issue.ActSubmit {
		t.Fatalf("最后一条应是发出：%+v", ev)
	} else if cc, _ := ev.Payload["cc"].([]any); len(cc) != 1 || cc[0] != "lee-watch" {
		t.Fatalf("submit 动态里应记下抄送：%v", ev.Payload)
	}
	list := notifsOf(t, e.env, is.ID)
	if len(list) != 1 || list[0].Event != core.EventAssigned || !sameList(list[0].Recipients, []int64{e.b.MemberID}) ||
		!sameList(list[0].Watchers, []int64{e.w.MemberID}) || len(list[0].Mentions) != 0 {
		t.Fatalf("发出只有一条群通知：收件人 b，关注者 w：%+v", list)
	}
	if got := alertsFor(t, e.env, is.ID, e.w.MemberID); len(got) != 1 || got[0] != (alertFull{e.w.MemberID, core.AlertWatching, core.EventAssigned, e.a.MemberID}) {
		t.Fatalf("关注者的会话提醒应为 watching、about=assigned：%v", got)
	}

	if _, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActAccept, ExpectedVersion: is.Version, CC: []string{"lee-other"}}); err == nil {
		t.Fatal("接单带抄送应报参数错")
	}
	// 发出时的名字问题：昵称、不存在、停用
	d := e.draft(t)
	var mce *core.MemberChoiceError
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version, CC: []string{"Otto"}}); !errors.As(err, &mce) {
		t.Fatalf("昵称应返回 MemberChoiceError：%v", err)
	}
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version, CC: []string{"nobody"}}); err == nil {
		t.Fatal("不存在的成员应报错")
	}
	got, _, _ := e.svc.Get(ctx, e.a, d.ID)
	if got.Status != issue.StatusDraft {
		t.Fatalf("抄送报错时发出整个回滚：%s", got.Status)
	}
}

// 关注者在整个闭环里每一步都收到告知，和原收件人在同一条群消息里，不另起一行。
func TestWatchersAlongTheLoop(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	w := []int64{e.w.MemberID}
	is := e.submit(t, "lee-watch")
	n := len(notifsOf(t, e.env, is.ID)) // 1

	step := func(name string, who store.Identity, act func() store.Issue, to []int64, event string) {
		t.Helper()
		act()
		got := lastNotif(t, e.env, is.ID, n)
		n++
		if got.Event != event || !sameList(got.Recipients, to) || !sameList(got.Watchers, w) {
			t.Fatalf("%s：应为 %s 收件人 %v 关注者 %v，得到 %s %v %v", name, event, to, w, got.Event, got.Recipients, got.Watchers)
		}
		_ = who
	}
	cur := is
	do := func(who store.Identity, a issue.Action) func() store.Issue {
		return func() store.Issue {
			cur = e.act(t, who, cur, a)
			return cur
		}
	}
	a, b := e.a.MemberID, e.b.MemberID
	step("接单", e.b, do(e.b, issue.ActAccept), []int64{a}, core.EventAccepted)
	step("开始修复", e.b, do(e.b, issue.ActStart), []int64{a}, core.EventStarted)
	step("留言", e.b, func() store.Issue {
		return e.comment(t, e.b, cur.ID, "进展")
	}, []int64{a}, core.EventCommented)
	step("请验证", e.b, do(e.b, issue.ActRequestVerification), []int64{a}, core.EventVerify)
	step("重开", e.a, do(e.a, issue.ActReopen), []int64{b}, core.EventReopened)
	step("再请验证", e.b, do(e.b, issue.ActRequestVerification), []int64{a}, core.EventVerify)
	step("关闭", e.a, do(e.a, issue.ActClose), []int64{b}, core.EventClosed)
	step("关闭后留言", e.a, func() store.Issue {
		return e.comment(t, e.a, cur.ID, "谢谢")
	}, []int64{b}, core.EventCommented)
	_ = ctx
}

// 退回、转交、作废：关注者同样收到；转交时关注者挂在“交给你”那条主通知上，告知发起人的那条不带。
func TestWatchersOnDeclineTransferCancel(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	w := []int64{e.w.MemberID}

	is := e.submit(t, "lee-watch")
	n := len(notifsOf(t, e.env, is.ID))
	if _, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActDecline, ExpectedVersion: is.Version, Note: "不归我"}); err != nil {
		t.Fatal(err)
	}
	if got := lastNotif(t, e.env, is.ID, n); got.Event != core.EventDeclined || !sameList(got.Recipients, []int64{e.a.MemberID}) || !sameList(got.Watchers, w) {
		t.Fatalf("退回：%+v", got)
	}

	is = e.submit(t, "lee-watch")
	n = len(notifsOf(t, e.env, is.ID))
	if _, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActTransfer, ExpectedVersion: is.Version, Assignee: "lee-other", Note: "给 o"}); err != nil {
		t.Fatal(err)
	}
	list := notifsOf(t, e.env, is.ID)
	if len(list) != n+2 {
		t.Fatalf("转交两条通知（交给你、告知发起人）：%d", len(list)-n)
	}
	if x := list[n]; x.Event != core.EventAssigned || !sameList(x.Recipients, []int64{e.o.MemberID}) || !sameList(x.Watchers, w) {
		t.Fatalf("转交的主通知带关注者：%+v", x)
	}
	if x := list[n+1]; x.Event != core.EventTransferred || len(x.Watchers) != 0 {
		t.Fatalf("告知发起人那条不带关注者：%+v", x)
	}

	// 发起人作废
	is = e.submit(t, "lee-watch")
	n = len(notifsOf(t, e.env, is.ID))
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActCancel, ExpectedVersion: is.Version, Note: "重复"}); err != nil {
		t.Fatal(err)
	}
	if got := lastNotif(t, e.env, is.ID, n); got.Event != core.EventCancelled || !sameList(got.Recipients, []int64{e.b.MemberID}) || !sameList(got.Watchers, w) {
		t.Fatalf("作废（发起人）：%+v", got)
	}

	// 管理员作废：管理员是操作者，双方收到，关注者在列
	is = e.submit(t, "lee-watch")
	n = len(notifsOf(t, e.env, is.ID))
	act, who := e.web(t, e.c)
	if _, err := e.svc.CancelIssue(ctx, act, who, core.CancelInput{IssueID: is.ID, ExpectedVersion: is.Version, Reason: "管理员作废"}); err != nil {
		t.Fatal(err)
	}
	if got := lastNotif(t, e.env, is.ID, n); got.Event != core.EventCancelled || !sameList(got.Watchers, w) || len(got.Recipients) != 2 {
		t.Fatalf("作废（管理员）：%+v", got)
	}
}

// 操作者本人是关注者：不在 Watchers、不收提醒；原收件人本人也是关注者时不重复。
func TestWatcherActorNotNotified(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	// b（负责人）被抄送，又是接单的人
	is := e.submit(t, "lee-codex", "lee-watch")
	acc := e.act(t, e.b, is, issue.ActAccept)
	n := lastNotif(t, e.env, is.ID, 1)
	if n.Event != core.EventAccepted || !sameList(n.Recipients, []int64{e.a.MemberID}) || !sameList(n.Watchers, []int64{e.w.MemberID}) {
		t.Fatalf("操作者 b 自己是关注者：Watchers 里不应有 b：%+v", n)
	}
	for _, a := range alertsFor(t, e.env, is.ID, e.b.MemberID) {
		if a.kind == core.AlertWatching {
			t.Fatalf("操作者不收关注提醒：%v", a)
		}
	}
	// 发起人 a 也被加成关注者后留言：收件人 b、关注者 w，没有 a
	if _, err := e.svc.SetWatch(ctx, core.Actor{}, e.a, core.WatchInput{IssueID: is.ID, Watch: true}); err != nil {
		t.Fatal(err)
	}
	e.comment(t, e.a, is.ID, "补充")
	last := notifsOf(t, e.env, is.ID)
	got := last[len(last)-1]
	if !sameList(got.Recipients, []int64{e.b.MemberID}) || !sameList(got.Watchers, []int64{e.w.MemberID}) {
		t.Fatalf("留言：收件人 b，关注者只有 w：%+v", got)
	}
	// b 本来就是原收件人，不会再以关注者收一遍
	if al := alertsFor(t, e.env, is.ID, e.b.MemberID); len(al) == 0 || al[len(al)-1].kind != core.AlertCommented {
		t.Fatalf("b 以原收件人收到留言提醒：%v", al)
	}
	_ = acc
}

// 没有任何原收件人（自己发给自己的单）时留言：新建一条只有关注者的通知。
func TestWatchersOnlyNotice(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "自己的单", Owner: "lee-claude", Actual: "x", CC: []string{"lee-watch"}})
	if err != nil {
		t.Fatal(err)
	}
	is := e.act(t, e.a, d, issue.ActSubmit)
	// 发出：负责人就是自己，主通知收件人是自己，关注者挂在上面
	e.comment(t, e.a, is.ID, "自言自语")
	list := notifsOf(t, e.env, is.ID)
	got := list[len(list)-1]
	if got.Event != core.EventCommented || len(got.Recipients) != 0 || !sameList(got.Watchers, []int64{e.w.MemberID}) {
		t.Fatalf("没有原收件人时只通知关注者：%+v", got)
	}
	if al := alertsFor(t, e.env, is.ID, e.w.MemberID); al[len(al)-1] != (alertFull{e.w.MemberID, core.AlertWatching, core.EventCommented, e.a.MemberID}) {
		t.Fatalf("关注者的提醒 about=commented：%v", al)
	}
}

// 改派：关注者挂在主通知上。
func TestWatchersOnReassign(t *testing.T) {
	ctx := context.Background()
	h := newHandover(t)
	p, svc := h.p, h.p.e.svc
	cara, err := store.CreateMember(ctx, svc.Pool, p.pay, "cara", "")
	if err != nil {
		t.Fatal(err)
	}
	dave, err := store.CreateMember(ctx, svc.Pool, p.pay, "dave", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.InsertWatcher(ctx, svc.Pool, h.asOwner.ID, dave.ID, dave.ID); err != nil {
		t.Fatal(err)
	}
	before := len(notifsOf(t, h.p.e, h.asOwner.ID))
	if err := svc.ApplyHandover(ctx, core.WebActor(p.payAdmin), p.pay, p.annPay.ID, h.fullPlan(cara.ID), true); err != nil {
		t.Fatal(err)
	}
	list := notifsOf(t, h.p.e, h.asOwner.ID)[before:]
	if len(list) < 2 || list[0].Event != core.EventAssigned || !sameList(list[0].Recipients, []int64{cara.ID}) || !sameList(list[0].Watchers, []int64{dave.ID}) {
		t.Fatalf("改派的主通知带关注者：%+v", list)
	}
	for _, x := range list[1:] {
		if len(x.Watchers) != 0 {
			t.Fatalf("只有主通知带关注者：%+v", x)
		}
	}
	found := false
	for _, a := range alertsFor(t, h.p.e, h.asOwner.ID, dave.ID) {
		found = found || (a.kind == core.AlertWatching && a.about == core.EventAssigned)
	}
	if !found {
		t.Fatal("关注者应收到 watching 提醒")
	}
}

// 超时催办：第 1 档不带关注者，第 2 档带上并写 watching 提醒（about=overdue_admin，不给出操作者）。
func TestWatchersOnOverdueAdmin(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	is := e.submit(t, "lee-watch")
	before := len(notifsOf(t, e.env, is.ID))
	ago(t, e.env, is.ID, 4)
	runReminders(t, e.env)
	if got := lastNotif(t, e.env, is.ID, before); got.Event != core.EventOverdue || len(got.Watchers) != 0 {
		t.Fatalf("第 1 档不带关注者：%+v", got)
	}
	ago(t, e.env, is.ID, 8)
	runReminders(t, e.env)
	list := notifsOf(t, e.env, is.ID)
	got := list[len(list)-1]
	if got.Event != core.EventOverdueAdmin || !sameList(got.Watchers, []int64{e.w.MemberID}) {
		t.Fatalf("第 2 档带关注者：%+v", got)
	}
	al := alertsFor(t, e.env, is.ID, e.w.MemberID)
	if len(al) != 2 || al[1].kind != core.AlertWatching || al[1].about != core.EventOverdueAdmin {
		t.Fatalf("关注者的提醒：%v", al)
	}
	res, err := e.svc.Alerts(ctx, e.w, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, it := range res.Items {
		if it.Kind == core.AlertWatching && it.About == core.EventOverdueAdmin {
			seen = true
			if it.Actor != "" {
				t.Fatalf("催办的关注提醒不给出操作者：%+v", it)
			}
		}
	}
	if !seen {
		t.Fatal("会话开始应附上刚写入的催办关注提醒（about=overdue_admin）")
	}
}

// 权限：关注者可留言，非相关成员不能；先关注再留言。
func TestWatcherCanCommentOthersCannot(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	is := e.submit(t, "lee-watch")
	in := core.ActInput{IssueID: is.ID, Action: issue.ActComment, Note: "我也遇到了"}
	if _, err := e.svc.Act(ctx, e.o, in); err == nil {
		t.Fatal("非相关成员不能留言")
	} else if !strings.Contains(err.Error(), "关注") {
		t.Fatalf("报错应提示先关注：%v", err)
	}
	if _, err := e.svc.Act(ctx, e.w, in); err != nil {
		t.Fatalf("关注者可以留言：%v", err)
	}
	// 关注者不能做别的动作
	for _, a := range []issue.Action{issue.ActAccept, issue.ActClose, issue.ActCancel} {
		if _, err := e.svc.Act(ctx, e.w, core.ActInput{IssueID: is.ID, Action: a, ExpectedVersion: is.Version, Note: "x"}); err == nil {
			t.Fatalf("关注者不能 %s", a)
		}
	}
	// o 自己关注之后可以留言，取消关注后又不行
	if _, err := e.svc.SetWatch(ctx, core.Actor{}, e.o, core.WatchInput{IssueID: is.ID, Watch: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Act(ctx, e.o, in); err != nil {
		t.Fatalf("先关注再留言：%v", err)
	}
	if _, err := e.svc.SetWatch(ctx, core.Actor{}, e.o, core.WatchInput{IssueID: is.ID, Watch: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Act(ctx, e.o, in); err == nil {
		t.Fatal("取消关注后不能再留言")
	}
	// 关注、取消关注不改版本号、不写动态
	got, evs, _ := e.svc.Get(ctx, e.a, is.ID)
	for _, ev := range evs {
		if ev.Action == "watch" || ev.Action == "unwatch" {
			t.Fatalf("关注不写动态：%+v", ev)
		}
	}
	if got.Version != is.Version {
		t.Fatalf("关注与留言不改版本：%d → %d", is.Version, got.Version)
	}
}

// 加别人只有发起人、负责人；移除别人只有发起人与账号登录的管理员；自己随时可取消；名字问题。
func TestSetWatchPermissions(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	is := e.submit(t)
	none := core.Actor{}
	set := func(who store.Identity, act core.Actor, watch bool, members ...string) ([]store.Member, error) {
		return e.svc.SetWatch(ctx, act, who, core.WatchInput{IssueID: is.ID, Members: members, Watch: watch})
	}
	if _, err := set(e.o, none, true, "lee-watch"); err == nil || !strings.Contains(err.Error(), "发起人或负责人") {
		t.Fatalf("非相关成员不能加别人：%v", err)
	}
	if got, err := set(e.w, none, true, "lee-watch"); err != nil || strings.Join(memberNamesOf(got), ",") != "lee-watch" {
		t.Fatalf("成员名写成自己等于关注自己：%v %v", memberNamesOf(got), err)
	}
	if got, err := set(e.b, none, true, "lee-other"); err != nil || strings.Join(memberNamesOf(got), ",") != "lee-watch,lee-other" {
		t.Fatalf("负责人可以加别人，按关注时间排序：%v %v", memberNamesOf(got), err)
	}
	if _, err := set(e.w, none, false, "lee-other"); err == nil || !strings.Contains(err.Error(), "移除") {
		t.Fatalf("关注者不能移除别人：%v", err)
	}
	if _, err := set(e.b, none, false, "lee-other"); err == nil {
		t.Fatal("负责人不能移除别人")
	}
	// 非管理员账号的网页会话也不行
	acc, _ := store.AccountByID(ctx, e.svc.Pool, e.o.AccountID)
	if _, err := set(e.w, core.WebActor(acc), false, "lee-other"); err == nil {
		t.Fatal("普通成员账号不能移除别人")
	}
	if got, err := set(e.a, none, false, "lee-other"); err != nil || strings.Join(memberNamesOf(got), ",") != "lee-watch" {
		t.Fatalf("发起人可以移除别人：%v %v", memberNamesOf(got), err)
	}
	// 管理员（账号登录）移除
	if _, err := set(e.b, none, true, "lee-other"); err != nil {
		t.Fatal(err)
	}
	act, who := e.web(t, e.c)
	if got, err := set(who, act, false, "lee-other"); err != nil || strings.Join(memberNamesOf(got), ",") != "lee-watch" {
		t.Fatalf("管理员可以移除别人：%v %v", memberNamesOf(got), err)
	}
	// 取消一个本来没关注的人：静默成功
	if _, err := set(e.a, none, false, "lee-other"); err != nil {
		t.Fatalf("取消没关注的人应静默成功：%v", err)
	}
	// 自己随时可以取消
	if got, err := set(e.w, none, false); err != nil || len(got) != 0 {
		t.Fatalf("取消自己：%v %v", memberNamesOf(got), err)
	}
	// 昵称、不存在
	var mce *core.MemberChoiceError
	if _, err := set(e.a, none, true, "Otto"); !errors.As(err, &mce) {
		t.Fatalf("昵称应返回 MemberChoiceError：%v", err)
	}
	if _, err := set(e.a, none, true, "nobody"); err == nil {
		t.Fatal("不存在的成员应报错")
	}
	// 别人的草稿看不见
	d := e.draft(t)
	if _, err := e.svc.SetWatch(ctx, none, e.o, core.WatchInput{IssueID: d.ID, Watch: true}); err == nil {
		t.Fatal("别人的草稿不能关注")
	}
	if _, err := e.svc.Watchers(ctx, e.o, d.ID); err == nil {
		t.Fatal("别人的草稿看不到关注者")
	}
}

// WatchPerms 给网页详情用。
func TestWatchPerms(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	is := e.submit(t, "lee-watch")
	acc := e.acc
	cases := []struct {
		name string
		who  store.Identity
		acc  *store.Account
		want core.WatchPerm
	}{
		{"发起人", e.a, nil, core.WatchPerm{CanWatch: true, CanAdd: true, CanRemoveOthers: true}},
		{"负责人", e.b, nil, core.WatchPerm{CanWatch: true, CanAdd: true}},
		{"关注者", e.w, nil, core.WatchPerm{Watching: true, CanWatch: true}},
		{"无关成员", e.o, nil, core.WatchPerm{CanWatch: true}},
		{"管理员", e.c, &acc, core.WatchPerm{CanWatch: true, CanRemoveOthers: true}},
	}
	for _, c := range cases {
		if got := e.svc.WatchPerms(ctx, c.acc, c.who, is); got != c.want {
			t.Errorf("%s：应为 %+v，得到 %+v", c.name, c.want, got)
		}
	}
	// 别人的草稿：什么都不能
	d := e.draft(t)
	if got := e.svc.WatchPerms(ctx, nil, e.o, d); got != (core.WatchPerm{}) {
		t.Errorf("别人的草稿：%+v", got)
	}
	if got := e.svc.WatchPerms(ctx, nil, e.a, d); !got.CanWatch || !got.CanAdd {
		t.Errorf("自己的草稿可以抄送：%+v", got)
	}
	// 归档后都不能操作，Watching 如实给出
	e.archive(t)
	if got := e.svc.WatchPerms(ctx, nil, e.w, is); got != (core.WatchPerm{Watching: true}) {
		t.Errorf("归档：%+v", got)
	}
}

// 归档后不能关注、取消关注；已关闭、已作废的单仍可。
func TestWatchArchivedAndFinished(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	closed := e.submit(t)
	closed = e.act(t, e.b, closed, issue.ActAccept)
	closed = e.act(t, e.b, closed, issue.ActStart)
	closed = e.act(t, e.b, closed, issue.ActRequestVerification)
	closed = e.act(t, e.a, closed, issue.ActClose)
	cancelled := e.submit(t)
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: cancelled.ID, Action: issue.ActCancel, ExpectedVersion: cancelled.Version, Note: "重复"}); err != nil {
		t.Fatal(err)
	}
	declined := e.submit(t)
	if _, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: declined.ID, Action: issue.ActDecline, ExpectedVersion: declined.Version, Note: "不归我"}); err != nil {
		t.Fatal(err)
	}
	for _, is := range []store.Issue{closed, cancelled, declined} {
		if _, err := e.svc.SetWatch(ctx, core.Actor{}, e.w, core.WatchInput{IssueID: is.ID, Watch: true}); err != nil {
			t.Fatalf("%s 的单仍可关注：%v", is.Status, err)
		}
		if _, err := e.svc.Act(ctx, e.w, core.ActInput{IssueID: is.ID, Action: issue.ActComment, Note: "后续"}); err != nil {
			t.Fatalf("%s 的单关注者仍可留言：%v", is.Status, err)
		}
		if _, err := e.svc.SetWatch(ctx, core.Actor{}, e.w, core.WatchInput{IssueID: is.ID, Watch: false}); err != nil {
			t.Fatalf("%s 的单仍可取消关注：%v", is.Status, err)
		}
	}
	if _, err := e.svc.SetWatch(ctx, core.Actor{}, e.w, core.WatchInput{IssueID: closed.ID, Watch: true}); err != nil {
		t.Fatal(err)
	}
	e.archive(t)
	if _, err := e.svc.SetWatch(ctx, core.Actor{}, e.w, core.WatchInput{IssueID: closed.ID, Watch: false}); !archivedErr(err) {
		t.Fatalf("归档后不能取消关注：%v", err)
	}
	if _, err := e.svc.SetWatch(ctx, core.Actor{}, e.o, core.WatchInput{IssueID: closed.ID, Watch: true}); !archivedErr(err) {
		t.Fatalf("归档后不能关注：%v", err)
	}
}

// 移出成员、停用账号的关注者不再收通知；行不删，读的时候过滤。
func TestInactiveWatchersSkipped(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	is := e.submit(t, "lee-watch", "lee-other")
	if got := e.watchers(t, is.ID); len(got) != 2 {
		t.Fatalf("两个关注者：%v", got)
	}
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE members SET status = 'removed' WHERE id = $1`, e.w.MemberID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE accounts SET disabled_at = now() WHERE id = $1`, e.o.AccountID); err != nil {
		t.Fatal(err)
	}
	if got := e.watchers(t, is.ID); len(got) != 0 {
		t.Fatalf("移出与停用的不算关注者：%v", got)
	}
	acc := e.act(t, e.b, is, issue.ActAccept)
	got := lastNotif(t, e.env, is.ID, 1)
	if len(got.Watchers) != 0 {
		t.Fatalf("不再通知已移出 / 停用的关注者：%+v", got)
	}
	for _, m := range []int64{e.w.MemberID, e.o.MemberID} {
		if al := alertsFor(t, e.env, is.ID, m); len(al) != 1 { // 只有发出时（尚在册）收到的那一条
			t.Fatalf("成员 %d 不应再收提醒：%v", m, al)
		}
	}
	var rows int
	_ = e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM issue_watchers WHERE issue_id = $1`, is.ID).Scan(&rows)
	if rows != 2 {
		t.Fatalf("关注行不删：%d", rows)
	}
	_ = acc
}

// 留言 @：只认登录名（不区分大小写），只就这一条通知，不加关注；昵称、重名、不存在记为没识别的 @。
func TestCommentMentions(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	is := e.submit(t)
	comment := func(who store.Identity, note string) (store.Notification, core.ActInfo) {
		t.Helper()
		before := len(notifsOf(t, e.env, is.ID))
		_, info, err := e.svc.ActDetail(ctx, who, core.ActInput{IssueID: is.ID, Action: issue.ActComment, Note: note})
		if err != nil {
			t.Fatal(err)
		}
		return lastNotif(t, e.env, is.ID, before), info
	}

	n, info := comment(e.a, "请 @Lee-Watch 看一下")
	if !sameList(n.Recipients, []int64{e.b.MemberID}) || !sameList(n.Mentions, []int64{e.w.MemberID}) || len(n.Watchers) != 0 || len(info.UnknownMentions) != 0 {
		t.Fatalf("@登录名（大小写不敏感）：%+v %+v", n, info)
	}
	if al := alertsFor(t, e.env, is.ID, e.w.MemberID); len(al) != 1 || al[0] != (alertFull{e.w.MemberID, core.AlertMentioned, core.EventCommented, e.a.MemberID}) {
		t.Fatalf("被 @ 的人收到 mentioned 提醒：%v", al)
	}
	if got := e.watchers(t, is.ID); len(got) != 0 {
		t.Fatalf("@ 不加关注：%v", got)
	}
	// 只就这一条：下一条留言不再通知 w
	n, _ = comment(e.a, "没人被提到")
	if len(n.Mentions) != 0 || len(n.Watchers) != 0 {
		t.Fatalf("@ 只就那一条：%+v", n)
	}

	// 未识别：昵称、不存在；邮箱不算；自己不算；重复去重、保留写法与顺序
	n, info = comment(e.a, "@Otto @nobody 联系 foo@lee-other.com，@lee-claude 自己 @Nobody @zzz")
	if len(n.Mentions) != 0 || strings.Join(info.UnknownMentions, ",") != "@Otto,@nobody,@zzz" {
		t.Fatalf("没识别的 @：%+v %+v", n, info)
	}
	// 邮箱形式、点号后的 @ 都不算
	if _, info = comment(e.a, "mail a@b.com x.@lee-other"); len(info.UnknownMentions) != 0 {
		t.Fatalf("邮箱不算 @：%v", info)
	}
	if n2, _ := comment(e.a, "(@lee-other)"); !sameList(n2.Mentions, []int64{e.o.MemberID}) {
		t.Fatalf("括号后的 @ 算：%+v", n2)
	}

	// 重名：登录名同时是别人的昵称 → 不通知
	if err := store.SetAccountDisplayName(ctx, e.svc.Pool, e.o.AccountID, "lee-watch"); err != nil {
		t.Fatal(err)
	}
	n, info = comment(e.a, "@lee-watch")
	if len(n.Mentions) != 0 || strings.Join(info.UnknownMentions, ",") != "@lee-watch" {
		t.Fatalf("重名不通知：%+v %+v", n, info)
	}
}

// 被 @ 的关注者只在 mentions 里，不重复在 watchers；被 @ 的原收件人不重复；草稿上的留言不通知也不 @。
func TestMentionedWatcherAndDraft(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	is := e.submit(t, "lee-watch", "lee-other")
	before := len(notifsOf(t, e.env, is.ID))
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActComment, Note: "@lee-watch @lee-codex"}); err != nil {
		t.Fatal(err)
	}
	n := lastNotif(t, e.env, is.ID, before)
	if !sameList(n.Recipients, []int64{e.b.MemberID}) || !sameList(n.Mentions, []int64{e.w.MemberID}) || !sameList(n.Watchers, []int64{e.o.MemberID}) {
		t.Fatalf("被 @ 的关注者只按被 @ 收；被 @ 的原收件人不重复：%+v", n)
	}
	var kinds []string
	for _, a := range alertsFor(t, e.env, is.ID, e.w.MemberID) {
		kinds = append(kinds, a.kind)
	}
	if strings.Join(kinds, ",") != "watching,mentioned" {
		t.Fatalf("w：发出时是 watching，被 @ 是 mentioned（不再重复 watching）：%v", kinds)
	}

	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "草稿", Owner: "lee-codex", CC: []string{"lee-watch"}})
	if err != nil {
		t.Fatal(err)
	}
	_, info, err := e.svc.ActDetail(ctx, e.a, core.ActInput{IssueID: d.ID, Action: issue.ActComment, Note: "@lee-watch @nobody"})
	if err != nil {
		t.Fatal(err)
	}
	if len(info.UnknownMentions) != 0 || len(notifsOf(t, e.env, d.ID)) != 0 || len(alertsFullOf(t, e.env, d.ID)) != 0 {
		t.Fatalf("草稿上的留言不通知任何人，也不解析 @：%+v", info)
	}
}

// 删除草稿、彻底删除工作空间时先删关注者，不因外键失败。
func TestDeleteDraftAndPurgeWithWatchers(t *testing.T) {
	ctx := context.Background()
	e := newWatchEnv(t)
	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "待删", Owner: "lee-codex", CC: []string{"lee-watch"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.DeleteDraft(ctx, e.a, d.ID); err != nil {
		t.Fatalf("带关注者的草稿应能删除：%v", err)
	}

	done := e.submit(t, "lee-watch")
	done = e.act(t, e.b, done, issue.ActAccept)
	done = e.act(t, e.b, done, issue.ActStart)
	done = e.act(t, e.b, done, issue.ActRequestVerification)
	e.act(t, e.a, done, issue.ActClose)
	if _, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "草稿", Owner: "lee-codex", CC: []string{"lee-other"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetServerAdmin(ctx, e.svc.Pool, e.acc.ID, true); err != nil {
		t.Fatal(err)
	}
	ws, _ := store.GetWorkspace(ctx, e.svc.Pool, e.a.WorkspaceID)
	if _, err := e.svc.PurgeWorkspace(ctx, core.WebActor(e.acc), ws.Name, ws.Name); err != nil {
		t.Fatalf("带关注者的工作空间应能彻底删除：%v", err)
	}
	var n int
	if err := e.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM issue_watchers`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("关注者应已清空：%d %v", n, err)
	}
}
