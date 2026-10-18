package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

func enableNotify(t *testing.T, e env) {
	t.Helper()
	if err := store.SetWorkspaceNotify(context.Background(), e.svc.Pool, e.a.WorkspaceID,
		store.NotifyConfig{Channel: "telegram", Target: "-100", ShowTitle: true, ShowBrief: true}); err != nil {
		t.Fatal(err)
	}
}

type sent struct {
	event      string
	recipients []int64
}

func pending(t *testing.T, e env, issueID int64) []sent {
	t.Helper()
	list, err := store.ListNotificationsForIssue(context.Background(), e.svc.Pool, issueID)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]sent, 0, len(list))
	for _, n := range list {
		out = append(out, sent{n.Event, n.Recipients})
	}
	return out
}

func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestNoNotificationWithoutGroup(t *testing.T) {
	e := newEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	if got := pending(t, e, is.ID); len(got) != 0 {
		t.Fatalf("工作空间没配置群时不应写待发通知：%v", got)
	}
}

func TestNotificationsAlongTheLoop(t *testing.T) {
	e := newEnv(t)
	enableNotify(t, e)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	is = e.act(t, e.b, is, issue.ActAccept)
	is = e.act(t, e.b, is, issue.ActStart)
	if _, err := e.svc.Act(context.Background(), e.b, core.ActInput{IssueID: is.ID, Action: issue.ActComment,
		ExpectedVersion: is.Version, Note: "进展"}); err != nil {
		t.Fatal(err)
	}
	got, _, err := e.svc.Get(context.Background(), e.b, is.ID)
	if err != nil {
		t.Fatal(err)
	}
	is = e.act(t, e.b, got, issue.ActRequestVerification)
	is = e.act(t, e.a, is, issue.ActReopen)
	is = e.act(t, e.b, is, issue.ActRequestVerification)
	e.act(t, e.a, is, issue.ActClose)

	// 每一步都通知另一方（N-4）：接单、开始修复、留言告知发起人，验证通过关单告知负责人；验证不通过只发重开，不发关单
	want := []sent{
		{"assigned", []int64{e.b.MemberID}},
		{"accepted", []int64{e.a.MemberID}},
		{"started", []int64{e.a.MemberID}},
		{"commented", []int64{e.a.MemberID}},
		{"verify", []int64{e.a.MemberID}},
		{"reopened", []int64{e.b.MemberID}},
		{"verify", []int64{e.a.MemberID}},
		{"closed", []int64{e.b.MemberID}},
	}
	got2 := pending(t, e, is.ID)
	if len(got2) != len(want) {
		t.Fatalf("应写入 %d 条待发通知，得到 %v", len(want), got2)
	}
	for i := range want {
		if got2[i].event != want[i].event || !sameIDs(got2[i].recipients, want[i].recipients) {
			t.Fatalf("第 %d 条应为 %v，得到 %v", i, want[i], got2[i])
		}
	}
}

func TestNotificationsTransferAndDecline(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	enableNotify(t, e)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	is, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActTransfer,
		ExpectedVersion: is.Version, Assignee: "lee-claude", Note: "前端的问题"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActDecline,
		ExpectedVersion: is.Version, Note: "不是前端"}); err != nil {
		t.Fatal(err)
	}
	got := pending(t, e, is.ID)
	if len(got) != 3 || got[1].event != "assigned" || !sameIDs(got[1].recipients, []int64{e.a.MemberID}) ||
		got[2].event != "declined" || !sameIDs(got[2].recipients, []int64{e.a.MemberID}) {
		t.Fatalf("转交应通知新负责人，退回应通知发起人：%v", got)
	}
}

func TestNotificationNotDuplicatedOnReplay(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	enableNotify(t, e)
	is := e.draft(t)
	in := core.ActInput{IssueID: is.ID, Action: issue.ActSubmit, ExpectedVersion: is.Version, IdempotencyKey: "s"}
	for i := 0; i < 2; i++ {
		if _, err := e.svc.Act(ctx, e.a, in); err != nil {
			t.Fatal(err)
		}
	}
	if got := pending(t, e, is.ID); len(got) != 1 {
		t.Fatalf("幂等重放不应重复写通知：%v", got)
	}
}

func TestEscalationNotifiesBoth(t *testing.T) {
	e := newEnv(t)
	enableNotify(t, e)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	is = e.act(t, e.b, is, issue.ActAccept)
	for i := 0; i < 3; i++ {
		is = e.act(t, e.b, is, issue.ActRequestVerification)
		is = e.act(t, e.a, is, issue.ActReopen)
	}
	escalated := 0
	for _, n := range pending(t, e, is.ID) {
		if n.event == "escalated" {
			escalated++
			if !sameIDs(n.recipients, []int64{e.a.MemberID, e.b.MemberID}) {
				t.Fatalf("需要人介入应同时 @ 发起人与负责人：%v", n.recipients)
			}
		}
	}
	if escalated != 1 {
		t.Fatalf("升级通知应只发一次，得到 %d", escalated)
	}
}

// 验证不通过：只有重开，没有关单通知；关单通知只在验证通过时出现，收件人是负责人。
func TestReopenIsNotClose(t *testing.T) {
	e := newEnv(t)
	enableNotify(t, e)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	is = e.act(t, e.b, is, issue.ActAccept)
	is = e.act(t, e.b, is, issue.ActRequestVerification)
	e.act(t, e.a, is, issue.ActReopen)
	for _, n := range pending(t, e, is.ID) {
		if n.event == "closed" {
			t.Fatalf("验证不通过不应产生关单通知：%v", pending(t, e, is.ID))
		}
	}
}

// 发起人与负责人是同一人：接单、关单照常发，收件人是他自己（群通知现在没有“对自己不 @”的做法）。
func TestAcceptCloseSamePerson(t *testing.T) {
	e := newEnv(t)
	enableNotify(t, e)
	ctx := context.Background()
	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "自己的单", Owner: "lee-claude", Actual: "x"})
	if err != nil {
		t.Fatal(err)
	}
	is := e.act(t, e.a, d, issue.ActSubmit)
	is = e.act(t, e.a, is, issue.ActAccept)
	is = e.act(t, e.a, is, issue.ActRequestVerification)
	e.act(t, e.a, is, issue.ActClose)
	got := pending(t, e, is.ID)
	var events []string
	for _, n := range got {
		events = append(events, n.event)
		if !sameIDs(n.recipients, []int64{e.a.MemberID}) {
			t.Fatalf("收件人都是他本人：%v", got)
		}
	}
	if strings.Join(events, ",") != "assigned,accepted,verify,closed" {
		t.Fatalf("同一人时照常发：%v", events)
	}
}
