package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

func setup(t *testing.T) (*pgxpool.Pool, int64) {
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
	return pool, ws
}

func mustMember(t *testing.T, q store.DBTX, ws int64, name string) store.Member {
	t.Helper()
	m, err := store.CreateMember(context.Background(), q, ws, name, "")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDefaultWorkspaceIsStable(t *testing.T) {
	pool, ws := setup(t)
	again, err := store.DefaultWorkspace(context.Background(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if again != ws {
		t.Fatalf("默认工作空间应稳定，得到 %d 和 %d", ws, again)
	}
}

func TestTokenLifecycle(t *testing.T) {
	ctx := context.Background()
	pool, ws := setup(t)
	m := mustMember(t, pool, ws, "lee-claude")

	plain, tokenID, err := store.IssueToken(ctx, pool, m.ID, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(plain, "cohive_") {
		t.Fatalf("令牌应以 cohive_ 开头：%s", plain)
	}

	id, err := store.LookupToken(ctx, pool, plain)
	if err != nil {
		t.Fatal(err)
	}
	if id.MemberID != m.ID || id.Tool != "claude-code" || id.MemberName != "lee-claude" || id.WorkspaceID != ws || id.TokenID != tokenID {
		t.Fatalf("身份不符：%+v", id)
	}

	if _, err := store.LookupToken(ctx, pool, plain+"x"); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("伪造令牌应返回 ErrUnauthorized，得到 %v", err)
	}
	if err := store.RevokeToken(ctx, pool, tokenID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupToken(ctx, pool, plain); !errors.Is(err, store.ErrUnauthorized) {
		t.Fatalf("吊销后应返回 ErrUnauthorized，得到 %v", err)
	}
}

func TestServiceOwner(t *testing.T) {
	ctx := context.Background()
	pool, ws := setup(t)
	a := mustMember(t, pool, ws, "a")
	b := mustMember(t, pool, ws, "b")

	if err := store.UpsertService(ctx, pool, ws, "order-api", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertService(ctx, pool, ws, "order-api", b.ID); err != nil {
		t.Fatal(err)
	}
	got, err := store.ServiceOwner(ctx, pool, ws, "order-api")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != b.ID {
		t.Fatalf("负责人应更新为 b，得到 %s", got.Name)
	}
	if _, err := store.ServiceOwner(ctx, pool, ws, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("未登记的服务应返回 ErrNotFound，得到 %v", err)
	}
}

func newIssue(ws, source int64, owner *int64, status issue.Status) *store.Issue {
	return &store.Issue{
		WorkspaceID: ws, SourceMemberID: source, SourceTool: "claude-code",
		OwnerID: owner, Title: "订单接口返回 500", Status: status, Severity: "normal",
	}
}

func TestUpdateIssueVersionConflict(t *testing.T) {
	ctx := context.Background()
	pool, ws := setup(t)
	a := mustMember(t, pool, ws, "a")
	b := mustMember(t, pool, ws, "b")

	is := newIssue(ws, a.ID, &b.ID, issue.StatusOpen)
	if err := store.InsertIssue(ctx, pool, is); err != nil {
		t.Fatal(err)
	}
	if is.ID == 0 || is.Version != 1 {
		t.Fatalf("插入后应得到编号与版本 1：%+v", is)
	}

	stale := *is
	is.Status = issue.StatusAccepted
	if err := store.UpdateIssue(ctx, pool, is); err != nil {
		t.Fatal(err)
	}
	if is.Version != 2 {
		t.Fatalf("更新后版本应为 2，得到 %d", is.Version)
	}

	stale.Status = issue.StatusDeclined
	if err := store.UpdateIssue(ctx, pool, &stale); !errors.Is(err, store.ErrVersionConflict) {
		t.Fatalf("旧版本更新应返回 ErrVersionConflict，得到 %v", err)
	}

	got, err := store.GetIssue(ctx, pool, is.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != issue.StatusAccepted || got.OwnerName != "b" || got.SourceName != "a" {
		t.Fatalf("读取结果不符：%+v", got)
	}
}

func TestEventsRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool, ws := setup(t)
	a := mustMember(t, pool, ws, "a")
	is := newIssue(ws, a.ID, nil, issue.StatusDraft)
	if err := store.InsertIssue(ctx, pool, is); err != nil {
		t.Fatal(err)
	}
	ev := store.Event{IssueID: is.ID, ActorMemberID: a.ID, ActorTool: "codex",
		Action: issue.ActSubmit, FromStatus: issue.StatusDraft, ToStatus: issue.StatusOpen, Note: "请看"}
	if err := store.InsertEvent(ctx, pool, ev); err != nil {
		t.Fatal(err)
	}
	evs, err := store.ListEvents(ctx, pool, is.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].ActorName != "a" || evs[0].ActorTool != "codex" || evs[0].ToStatus != issue.StatusOpen {
		t.Fatalf("事件不符：%+v", evs)
	}
}

func TestInboxOnlyShowsWhatNeedsMe(t *testing.T) {
	ctx := context.Background()
	pool, ws := setup(t)
	a := mustMember(t, pool, ws, "a")
	b := mustMember(t, pool, ws, "b")

	mk := func(source int64, owner *int64, st issue.Status) int64 {
		is := newIssue(ws, source, owner, st)
		if err := store.InsertIssue(ctx, pool, is); err != nil {
			t.Fatal(err)
		}
		return is.ID
	}
	draftOfA := mk(a.ID, &b.ID, issue.StatusDraft)
	openToB := mk(a.ID, &b.ID, issue.StatusOpen)
	verifyForA := mk(a.ID, &b.ID, issue.StatusReadyForVerification)
	mk(a.ID, &b.ID, issue.StatusClosed)
	fixingByB := mk(a.ID, &b.ID, issue.StatusFixing)

	ids := func(list []store.Issue) map[int64]bool {
		m := map[int64]bool{}
		for _, x := range list {
			m[x.ID] = true
		}
		return m
	}
	inA, err := store.ListInbox(ctx, pool, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotA := ids(inA)
	if len(gotA) != 2 || !gotA[draftOfA] || !gotA[verifyForA] {
		t.Fatalf("a 的收件箱应只有自己的草稿和待验证单，得到 %v", gotA)
	}
	inB, err := store.ListInbox(ctx, pool, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotB := ids(inB)
	if len(gotB) != 2 || !gotB[openToB] || !gotB[fixingByB] {
		t.Fatalf("b 的收件箱应只有待接单和修复中的单，得到 %v", gotB)
	}
}

func TestSearchHidesOthersDrafts(t *testing.T) {
	ctx := context.Background()
	pool, ws := setup(t)
	a := mustMember(t, pool, ws, "a")
	b := mustMember(t, pool, ws, "b")
	d := newIssue(ws, a.ID, nil, issue.StatusDraft)
	d.TargetService = "order-api"
	o := newIssue(ws, a.ID, &b.ID, issue.StatusOpen)
	o.TargetService = "order-api"
	for _, is := range []*store.Issue{d, o} {
		if err := store.InsertIssue(ctx, pool, is); err != nil {
			t.Fatal(err)
		}
	}
	res, err := store.SearchIssues(ctx, pool, ws, b.ID, "500", "order-api", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != o.ID {
		t.Fatalf("b 搜索应只看到已发出的单，得到 %d 条", len(res))
	}
	res, err = store.SearchIssues(ctx, pool, ws, a.ID, "500", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("a 搜索应看到自己的草稿和已发出的单，得到 %d 条", len(res))
	}
}

func TestIdempotencyRecord(t *testing.T) {
	ctx := context.Background()
	pool, ws := setup(t)
	a := mustMember(t, pool, ws, "a")
	_, tokenID, err := store.IssueToken(ctx, pool, a.ID, "codex")
	if err != nil {
		t.Fatal(err)
	}
	is := newIssue(ws, a.ID, nil, issue.StatusDraft)
	if err := store.InsertIssue(ctx, pool, is); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.GetIdempotent(ctx, pool, tokenID, "k1"); err != nil || ok {
		t.Fatalf("未记录前应查不到：%v %v", ok, err)
	}
	if err := store.PutIdempotent(ctx, pool, tokenID, "k1", store.IdemRecord{IssueID: is.ID, Action: "submit"}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := store.GetIdempotent(ctx, pool, tokenID, "k1")
	if err != nil || !ok || got.IssueID != is.ID || got.Action != "submit" {
		t.Fatalf("应查到 #%d 的 submit，得到 %+v %v %v", is.ID, got, ok, err)
	}
}
