package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

func (e env) comment(t *testing.T, who store.Identity, id int64, note string) store.Issue {
	t.Helper()
	got, err := e.svc.Act(context.Background(), who, core.ActInput{IssueID: id, Action: issue.ActComment, Note: note})
	if err != nil {
		t.Fatalf("%s 评论失败：%v", who.MemberName, err)
	}
	return got
}

// 计划 3.3 第 2 组：读到版本 v 后对方补了评论，用 v 接单照样成功（人刚批准的确认不作废）。
func TestCommentDoesNotInvalidateApproval(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	read := is.Version

	after := e.comment(t, e.a, is.ID, "补充：只在库存为零时出现")
	if after.Version != read {
		t.Fatalf("评论不应改变版本号：%d → %d", read, after.Version)
	}
	got, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActAccept, ExpectedVersion: read})
	if err != nil {
		t.Fatalf("评论之后用读到的版本接单应成功：%v", err)
	}
	if got.Status != issue.StatusAccepted || got.Version != read+1 {
		t.Fatalf("接单后应为已接单、版本加一：%s v%d", got.Status, got.Version)
	}

	// 草稿上的评论同样不影响随后的发出
	d := e.draft(t)
	e.comment(t, e.a, d.ID, "发出前补一句")
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version}); err != nil {
		t.Fatalf("草稿评论后用原版本发出应成功：%v", err)
	}
}

// 计划 3.3 第 1 组：评论与接单同时发起，两者都成功。
func TestConcurrentCommentAndAccept(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	for round := 0; round < 20; round++ {
		is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, 2)
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, errs[0] = e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActComment, Note: "补充日志"})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, errs[1] = e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActAccept, ExpectedVersion: is.Version})
		}()
		close(start)
		wg.Wait()
		if errs[0] != nil || errs[1] != nil {
			t.Fatalf("第 %d 轮：评论与接单都应成功：评论 %v，接单 %v", round, errs[0], errs[1])
		}
		got, evs, err := e.svc.Get(ctx, e.a, is.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != issue.StatusAccepted || got.Version != is.Version+1 {
			t.Fatalf("第 %d 轮：应为已接单、版本 %d，得到 %s v%d", round, is.Version+1, got.Status, got.Version)
		}
		var n int
		for _, ev := range evs {
			if ev.Action == issue.ActComment || ev.Action == issue.ActAccept {
				n++
			}
		}
		if n != 2 {
			t.Fatalf("第 %d 轮：评论与接单两条事件都应在，得到 %d 条", round, n)
		}
	}
}

// race 让两个动作基于同一版本同时执行，返回成功数与版本冲突数。
func race(t *testing.T, fns ...func() error) (ok, conflict int) {
	t.Helper()
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, len(fns))
	for i, fn := range fns {
		wg.Add(1)
		go func(i int, fn func() error) {
			defer wg.Done()
			<-start
			errs[i] = fn()
		}(i, fn)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		var he *core.HandoverError
		switch {
		case err == nil:
			ok++
		case errors.Is(err, store.ErrVersionConflict), errors.As(err, &he):
			conflict++
		default:
			t.Errorf("意外错误：%v", err)
		}
	}
	return ok, conflict
}

// 计划 3.3 第 3 组：真正的并发状态冲突仍被拦住。
func TestConcurrentStateChangesStillConflict(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	act := func(who store.Identity, is store.Issue, a issue.Action, note string) func() error {
		return func() error {
			_, err := e.svc.Act(ctx, who, core.ActInput{IssueID: is.ID, Action: a, ExpectedVersion: is.Version, Note: note})
			return err
		}
	}
	for round := 0; round < 10; round++ {
		is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
		if ok, c := race(t, act(e.b, is, issue.ActAccept, ""), act(e.b, is, issue.ActDecline, "不归我")); ok != 1 || c != 1 {
			t.Fatalf("接单与退回同时发起应一成功一冲突：成功 %d 冲突 %d", ok, c)
		}

		// 开始修复后仍可请求验证：只有版本比对能拦住“基于旧状态”的第二个操作
		w := e.act(t, e.a, e.draft(t), issue.ActSubmit)
		w = e.act(t, e.b, w, issue.ActAccept)
		if ok, c := race(t, act(e.b, w, issue.ActStart, ""), act(e.b, w, issue.ActRequestVerification, "")); ok != 1 || c != 1 {
			t.Fatalf("开始修复与请求验证基于同一版本同时发起应一成功一冲突：成功 %d 冲突 %d", ok, c)
		}

		v := e.act(t, e.a, e.draft(t), issue.ActSubmit)
		v = e.act(t, e.b, v, issue.ActAccept)
		v = e.act(t, e.b, v, issue.ActRequestVerification)
		if ok, c := race(t, act(e.a, v, issue.ActClose, ""), act(e.a, v, issue.ActReopen, "")); ok != 1 || c != 1 {
			t.Fatalf("关闭与重开同时发起应一成功一冲突：成功 %d 冲突 %d", ok, c)
		}
	}
}

// 计划 3.3 第 3 组：管理员改派与负责人接单同时发起，恰好一个成功。
func TestConcurrentReassignAndAccept(t *testing.T) {
	ctx := context.Background()
	for round := 0; round < 5; round++ {
		h := newHandover(t)
		p, svc := h.p, h.p.e.svc
		ann := tokenFor(t, svc, p.annPay)
		accept := func() error {
			_, err := svc.Act(ctx, ann, core.ActInput{IssueID: h.asOwner.ID, Action: issue.ActAccept, ExpectedVersion: h.asOwner.Version})
			return err
		}
		reassign := func() error {
			return svc.ApplyHandover(ctx, &p.payAdmin, p.pay, p.annPay.ID, h.fullPlan(h.bob.ID), false)
		}
		if ok, c := race(t, accept, reassign); ok != 1 || c != 1 {
			t.Fatalf("改派与接单同时发起应一成功一冲突：成功 %d 冲突 %d", ok, c)
		}
	}
}

// 计划 3.3 第 4、5 组：评论可不填版本号；权限按行锁下的当前状态判定；幂等重试不重复写。
func TestCommentRulesAndIdempotency(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	// 版本已因转交前进，评论不填版本号照样成功
	moved, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActTransfer,
		ExpectedVersion: is.Version, Assignee: "lee-claude", Note: "前端的问题"})
	if err != nil {
		t.Fatal(err)
	}
	e.comment(t, e.a, is.ID, "收到，我来看")
	// 原负责人已经转交出去，不再是发起人或负责人，评论被拒
	if _, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActComment, Note: "还想说一句"}); err == nil {
		t.Fatal("转交出去的原负责人不能再评论")
	}

	key := "comment-retry-1"
	in := core.ActInput{IssueID: is.ID, Action: issue.ActComment, Note: "幂等评论", IdempotencyKey: key}
	first, err := e.svc.Act(ctx, e.a, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.Act(ctx, e.a, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Version != moved.Version || second.Version != moved.Version {
		t.Fatalf("评论与重试都不应改变版本号：%d %d %d", moved.Version, first.Version, second.Version)
	}
	_, evs, err := e.svc.Get(ctx, e.a, is.ID)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range evs {
		if ev.Note == "幂等评论" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("同一幂等键的评论只应写一次，得到 %d 次", n)
	}
}
