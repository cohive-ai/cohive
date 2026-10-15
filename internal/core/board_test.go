package core_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

func TestHolderRules(t *testing.T) {
	src, own := int64(1), int64(2)
	cases := map[issue.Status]int64{
		issue.StatusDraft: src, issue.StatusReadyForVerification: src, issue.StatusDeclined: src,
		issue.StatusOpen: own, issue.StatusAccepted: own, issue.StatusFixing: own, issue.StatusReopened: own,
	}
	for st, want := range cases {
		got, ok := core.Holder(store.Issue{SourceMemberID: src, OwnerID: &own, Status: st})
		if !ok || got != want {
			t.Errorf("%s 的处理人应为 %d，得到 %d %v", st, want, got, ok)
		}
	}
	if _, ok := core.Holder(store.Issue{SourceMemberID: src, OwnerID: &own, Status: issue.StatusClosed}); ok {
		t.Error("已关闭的单没有处理人")
	}
	if got, ok := core.Holder(store.Issue{SourceMemberID: src, Status: issue.StatusDraft}); !ok || got != src {
		t.Error("没有负责人的草稿，处理人是发起人")
	}
}

func TestBoard(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.draft(t) // a 的草稿，b 不应看到
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)

	members, items, err := e.svc.Board(ctx, e.b)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("应返回 2 位成员，得到 %d", len(members))
	}
	if len(items) != 1 || items[0].ID != is.ID {
		t.Fatalf("b 的看板应只有已发出的那张单：%+v", items)
	}
	if items[0].HolderName != "lee-codex" || items[0].Needs == "" {
		t.Fatalf("待接单的处理人应为负责人 lee-codex 并附说明：%+v", items[0])
	}
}
