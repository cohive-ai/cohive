package store_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

func TestListBoard(t *testing.T) {
	ctx := context.Background()
	pool, ws := setup(t)
	a := mustMember(t, pool, ws, "a")
	b := mustMember(t, pool, ws, "b")
	mk := func(source int64, st issue.Status) int64 {
		is := newIssue(ws, source, &b.ID, st)
		if err := store.InsertIssue(ctx, pool, is); err != nil {
			t.Fatal(err)
		}
		return is.ID
	}
	draftOfA := mk(a.ID, issue.StatusDraft)
	open := mk(a.ID, issue.StatusOpen)
	recentClosed := mk(a.ID, issue.StatusClosed)
	oldClosed := mk(a.ID, issue.StatusClosed)
	if _, err := pool.Exec(ctx, `UPDATE issues SET updated_at = now() - interval '8 days' WHERE id = $1`, oldClosed); err != nil {
		t.Fatal(err)
	}

	ids := func(viewer int64) map[int64]bool {
		list, err := store.ListBoard(ctx, pool, ws, viewer)
		if err != nil {
			t.Fatal(err)
		}
		m := map[int64]bool{}
		for _, x := range list {
			m[x.ID] = true
		}
		return m
	}
	forB := ids(b.ID)
	if forB[draftOfA] || !forB[open] || !forB[recentClosed] || forB[oldClosed] {
		t.Fatalf("b 的看板应有待接单与 7 天内关闭的单，不含 a 的草稿与 8 天前关闭的单：%v", forB)
	}
	if forA := ids(a.ID); !forA[draftOfA] {
		t.Fatalf("a 的看板应包含自己的草稿：%v", forA)
	}
}
