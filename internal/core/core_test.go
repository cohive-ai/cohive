package core_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
)

type env struct {
	svc  *core.Service
	a, b store.Identity // a 用 Claude Code 写前端，b 用 Codex 负责 order-api
}

func newEnv(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	ws, err := store.DefaultWorkspace(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	ident := func(name, tool string) store.Identity {
		m, err := store.CreateMember(ctx, pool, ws, name, "")
		if err != nil {
			t.Fatal(err)
		}
		plain, _, err := store.IssueToken(ctx, pool, m.ID, tool)
		if err != nil {
			t.Fatal(err)
		}
		id, err := store.LookupToken(ctx, pool, plain)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	e := env{svc: &core.Service{Pool: pool}, a: ident("lee-claude", "claude-code"), b: ident("lee-codex", "codex")}
	if err := store.UpsertService(ctx, pool, ws, "order-api", e.b.MemberID); err != nil {
		t.Fatal(err)
	}
	return e
}

func (e env) draft(t *testing.T) store.Issue {
	t.Helper()
	is, err := e.svc.Draft(context.Background(), e.a, core.DraftInput{
		Title: "下单接口返回 500", Expected: "200 与订单号", Actual: "500 internal error",
		ReproScript: "curl -sf http://localhost:8080/orders -d '{}'", ReproExitCode: intp(0),
		TargetService: "order-api", Commit: "abc1234",
	})
	if err != nil {
		t.Fatal(err)
	}
	return is
}

func intp(v int) *int { return &v }

func (e env) act(t *testing.T, who store.Identity, is store.Issue, a issue.Action) store.Issue {
	t.Helper()
	got, err := e.svc.Act(context.Background(), who, core.ActInput{IssueID: is.ID, Action: a, ExpectedVersion: is.Version})
	if err != nil {
		t.Fatalf("%s 执行 %s 失败：%v", who.MemberName, a, err)
	}
	return got
}

func TestFullLoop(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)

	is := e.draft(t)
	if is.Status != issue.StatusDraft || is.OwnerID == nil || *is.OwnerID != e.b.MemberID {
		t.Fatalf("草稿应按服务登记自动带上负责人：%+v", is)
	}
	is = e.act(t, e.a, is, issue.ActSubmit)
	is = e.act(t, e.b, is, issue.ActAccept)
	is = e.act(t, e.b, is, issue.ActStart)
	is = e.act(t, e.b, is, issue.ActRequestVerification)
	is = e.act(t, e.a, is, issue.ActReopen)
	is = e.act(t, e.b, is, issue.ActRequestVerification)
	is = e.act(t, e.a, is, issue.ActClose)

	if is.Status != issue.StatusClosed || is.ReopenCount != 1 {
		t.Fatalf("最终应为已关闭且重开 1 次：%s %d", is.Status, is.ReopenCount)
	}
	_, evs, err := e.svc.Get(ctx, e.a, is.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 8 { // draft + 7 个动作
		t.Fatalf("应有 8 条事件，得到 %d", len(evs))
	}
	if evs[0].Action != "draft" || evs[0].ActorTool != "claude-code" {
		t.Errorf("首条事件应为 Claude Code 起草：%+v", evs[0])
	}
	if evs[2].Action != issue.ActAccept || evs[2].ActorTool != "codex" || evs[2].ActorName != "lee-codex" {
		t.Errorf("接单事件应记录 Codex 身份：%+v", evs[2])
	}
}

func TestConcurrentAcceptOnlyOneWins(t *testing.T) {
	e := newEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = e.svc.Act(context.Background(), e.b, core.ActInput{
				IssueID: is.ID, Action: issue.ActAccept, ExpectedVersion: is.Version})
		}(i)
	}
	wg.Wait()

	ok, conflict := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, store.ErrVersionConflict):
			conflict++
			if !strings.Contains(err.Error(), "当前是") {
				t.Errorf("冲突错误应说明当前版本：%v", err)
			}
		default:
			t.Errorf("意外错误：%v", err)
		}
	}
	if ok != 1 || conflict != 1 {
		t.Fatalf("应一成功一冲突，得到成功 %d 冲突 %d", ok, conflict)
	}
}

func TestSubmitIdempotent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	is := e.draft(t)
	in := core.ActInput{IssueID: is.ID, Action: issue.ActSubmit, ExpectedVersion: is.Version, IdempotencyKey: "submit-1"}

	first, err := e.svc.Act(ctx, e.a, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.Act(ctx, e.a, in)
	if err != nil {
		t.Fatalf("同一幂等键重试不应报错：%v", err)
	}
	if second.ID != first.ID || second.Version != first.Version {
		t.Fatalf("重试应返回同一结果：%+v %+v", first, second)
	}
	_, evs, err := e.svc.Get(ctx, e.a, is.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("重试不应产生新事件，事件数 %d", len(evs))
	}
}

func TestIdempotencyKeyReuseAcrossIssuesRejected(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	one, two := e.draft(t), e.draft(t)
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: one.ID, Action: issue.ActSubmit,
		ExpectedVersion: one.Version, IdempotencyKey: "k"}); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: two.ID, Action: issue.ActSubmit,
		ExpectedVersion: two.Version, IdempotencyKey: "k"})
	if err == nil || !strings.Contains(err.Error(), "幂等键") {
		t.Fatalf("同一个键用于另一张单应明确报错，得到 %v", err)
	}
	got, _, err := e.svc.Get(ctx, e.a, two.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != issue.StatusDraft {
		t.Fatalf("第二张单不应被改动，状态 %s", got.Status)
	}
}

func TestIdempotencyKeyReuseAcrossActionsRejected(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "x", TargetService: "order-api", IdempotencyKey: "same"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Act(ctx, e.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit,
		ExpectedVersion: d.Version, IdempotencyKey: "same"})
	if err == nil || !strings.Contains(err.Error(), "幂等键") {
		t.Fatalf("起草用过的键再用于发出应明确报错，得到 %v", err)
	}
}

// 两个同键请求都在行锁上等待（此时都已错过事务外的幂等检查），放开锁后都应拿到首次结果。
func TestConcurrentSameKeyRetryReturnsFirstResult(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	is := e.draft(t)
	in := core.ActInput{IssueID: is.ID, Action: issue.ActSubmit, ExpectedVersion: is.Version, IdempotencyKey: "retry"}

	blocker, err := e.svc.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := blocker.Exec(ctx, "SELECT 1 FROM issues WHERE id = $1 FOR UPDATE", is.ID); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make([]store.Issue, 2)
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = e.svc.Act(ctx, e.a, in)
		}(i)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		if err := e.svc.Pool.QueryRow(ctx,
			`SELECT count(*) FROM pg_stat_activity WHERE wait_event_type = 'Lock' AND datname = current_database()`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("两个请求未能同时卡在行锁上（当前 %d 个）", waiting)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := blocker.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("同一个键并发重试都应成功，第 %d 个失败：%v", i, err)
		}
		if results[i].Status != issue.StatusOpen {
			t.Fatalf("第 %d 个结果状态应为 OPEN，得到 %s", i, results[i].Status)
		}
	}
	_, evs, err := e.svc.Get(context.Background(), e.a, is.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 2 {
		t.Fatalf("应只有起草与一次发出两条事件，得到 %d", len(evs))
	}
}

func TestDeclineRequiresReason(t *testing.T) {
	e := newEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	_, err := e.svc.Act(context.Background(), e.b, core.ActInput{IssueID: is.ID, Action: issue.ActDecline, ExpectedVersion: is.Version})
	if err == nil || !strings.Contains(err.Error(), "理由") {
		t.Fatalf("退回不填理由应报错，得到 %v", err)
	}
}

func TestDeclinedVisibleToSource(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	if _, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActDecline,
		ExpectedVersion: is.Version, Note: "这是网关超时，不是订单服务"}); err != nil {
		t.Fatal(err)
	}
	items, err := e.svc.Inbox(ctx, e.a)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != is.ID {
		t.Fatalf("被退回的单应出现在发起人收件箱：%+v", items)
	}
	if !strings.Contains(items[0].NeedsMe, "退回") || !strings.Contains(items[0].NeedsMe, "网关超时") {
		t.Fatalf("应提示被退回并附理由：%q", items[0].NeedsMe)
	}
}

func TestDraftIdempotent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	in := core.DraftInput{Title: "x", TargetService: "order-api", IdempotencyKey: "d1"}
	first, err := e.svc.Draft(ctx, e.a, in)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.Draft(ctx, e.a, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("同一幂等键不应创建第二张草稿：%d %d", first.ID, second.ID)
	}
}

func TestPermissionDenied(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)

	_, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActAccept, ExpectedVersion: is.Version})
	var te *issue.TransitionError
	if !errors.As(err, &te) || !strings.Contains(err.Error(), "负责人") {
		t.Fatalf("发起人接单应被拒绝并说明需负责人：%v", err)
	}

	is = e.act(t, e.b, is, issue.ActAccept)
	is = e.act(t, e.b, is, issue.ActRequestVerification)
	_, err = e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActClose, ExpectedVersion: is.Version})
	if !errors.As(err, &te) || !strings.Contains(err.Error(), "发起人") {
		t.Fatalf("负责人关闭应被拒绝并说明需发起人：%v", err)
	}
}

func TestDraftHiddenFromOthers(t *testing.T) {
	e := newEnv(t)
	is := e.draft(t)
	if _, _, err := e.svc.Get(context.Background(), e.b, is.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("别人的草稿应不可见，得到 %v", err)
	}
}

func TestVersionRequired(t *testing.T) {
	e := newEnv(t)
	is := e.draft(t)
	_, err := e.svc.Act(context.Background(), e.a, core.ActInput{IssueID: is.ID, Action: issue.ActSubmit})
	if err == nil || !strings.Contains(err.Error(), "expected_version") {
		t.Fatalf("缺少版本号应报错，得到 %v", err)
	}
}

func TestReopenThreeTimesEscalates(t *testing.T) {
	e := newEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	is = e.act(t, e.b, is, issue.ActAccept)
	for i := 0; i < 3; i++ {
		is = e.act(t, e.b, is, issue.ActRequestVerification)
		is = e.act(t, e.a, is, issue.ActReopen)
	}
	if is.ReopenCount != 3 || is.EscalatedAt == nil || is.EscalationReason == "" {
		t.Fatalf("重开 3 次应自动标记升级：%d %v %q", is.ReopenCount, is.EscalatedAt, is.EscalationReason)
	}
	if is.Status != issue.StatusReopened {
		t.Fatalf("升级不应改变状态，得到 %s", is.Status)
	}
}

func TestSubmitWithoutOwnerFails(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	is, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "未知服务的问题", TargetService: "unknown-svc"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActSubmit, ExpectedVersion: is.Version})
	if err == nil || !strings.Contains(err.Error(), "负责人") {
		t.Fatalf("没有负责人时发出应失败并提示指定负责人：%v", err)
	}
	got, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActSubmit,
		ExpectedVersion: is.Version, Assignee: "lee-codex"})
	if err != nil {
		t.Fatalf("指定负责人后应能发出：%v", err)
	}
	if got.OwnerName != "lee-codex" || got.Status != issue.StatusOpen {
		t.Fatalf("发出结果不符：%+v", got)
	}
}

func TestTransfer(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	got, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActTransfer,
		ExpectedVersion: is.Version, Assignee: "lee-claude", Note: "这是前端参数问题"})
	if err != nil {
		t.Fatal(err)
	}
	if got.OwnerName != "lee-claude" || got.Status != issue.StatusOpen {
		t.Fatalf("转交后负责人应变更且仍待接单：%+v", got)
	}
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActTransfer,
		ExpectedVersion: got.Version}); err == nil {
		t.Fatal("转交必须指定接收人")
	}
}

func TestInboxNeedsMe(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	d := e.draft(t)
	items, err := e.svc.Inbox(ctx, e.a)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != d.ID || !strings.Contains(items[0].NeedsMe, "发出") {
		t.Fatalf("a 的收件箱应提示草稿待确认发出：%+v", items)
	}
	e.act(t, e.a, d, issue.ActSubmit)
	items, err = e.svc.Inbox(ctx, e.b)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || !strings.Contains(items[0].NeedsMe, "接单") {
		t.Fatalf("b 的收件箱应提示待接单：%+v", items)
	}
}

func TestWhoOwns(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	m, err := e.svc.WhoOwns(ctx, e.a, "order-api")
	if err != nil || m.Name != "lee-codex" {
		t.Fatalf("order-api 负责人应为 lee-codex：%+v %v", m, err)
	}
	if _, err := e.svc.WhoOwns(ctx, e.a, "nope"); err == nil || !strings.Contains(err.Error(), "未登记") {
		t.Fatalf("未登记服务应说明未登记：%v", err)
	}
}
