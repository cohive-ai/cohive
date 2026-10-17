package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
	"github.com/jackc/pgx/v5"
)

// 下面的用例用一道“闸门”控制两个事务的交错：闸门事务以 SHARE 模式锁住 issue_events，
// 任何写问题单事件的事务都会停在 INSERT 上——此时它已持有问题单行锁，但还没因外键引用去碰成员行。
// 再让另一个事务停在它的下一把锁上，然后开闸。加锁顺序不一致时数据库必然判死锁，所以能稳定复现。

func closeGate(t *testing.T, svc *core.Service) pgx.Tx {
	t.Helper()
	ctx := context.Background()
	gate, err := svc.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gate.Exec(ctx, `LOCK TABLE issue_events IN SHARE MODE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gate.Rollback(context.Background()) })
	return gate
}

func goErr(fn func() error) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- fn() }()
	return ch
}

// 负责人接单已锁住问题单、正要写事件；管理员此时对他改派（含移出）。
// 修复前：改派先锁成员行再等问题单，接单写事件时按外键要成员行的共享锁，两边互等，数据库判死锁。
func TestReassignWhileAcceptHoldsIssue(t *testing.T) {
	ctx := context.Background()
	for _, remove := range []bool{false, true} {
		h := newHandover(t)
		p, svc := h.p, h.p.e.svc
		ann := tokenFor(t, svc, p.annPay)
		gate := closeGate(t, svc)
		accept := goErr(func() error {
			_, err := svc.Act(ctx, ann, core.ActInput{IssueID: h.asOwner.ID, Action: issue.ActAccept, ExpectedVersion: h.asOwner.Version})
			return err
		})
		testutil.WaitLockWaiters(t, svc.Pool, 1) // 接单停在写事件前
		handover := goErr(func() error {
			return svc.ApplyHandover(ctx, core.WebActor(p.payAdmin), p.pay, p.annPay.ID, h.fullPlan(h.bob.ID), remove)
		})
		testutil.WaitLockWaiters(t, svc.Pool, 2) // 改派停在问题单行锁上
		if err := gate.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		if err := <-accept; err != nil {
			t.Fatalf("移出=%v：先拿到问题单的接单应成功：%v", remove, err)
		}
		var he *core.HandoverError
		if err := <-handover; !errors.As(err, &he) {
			t.Fatalf("移出=%v：改派应报“已有新进展，请刷新清单”，得到：%v", remove, err)
		}
	}
}

// 两名管理员同时转交两个人，两人名下的单交叉（ann 是 X 的负责人、Y 的发起人，bob 反之），
// 两份清单的顺序相反。修复前各按清单顺序逐张加锁，交叉互等成死锁；修复后都按问题单编号升序先锁齐。
func TestCrossHandoversLockIssuesInOrder(t *testing.T) {
	ctx := context.Background()
	h := newHandover(t)
	p, svc := h.p, h.p.e.svc
	carol, err := store.CreateMember(ctx, svc.Pool, p.pay, "carol", "")
	if err != nil {
		t.Fatal(err)
	}
	annPlan := h.fullPlan(carol.ID) // 先 asOwner 后 asSource
	bobH, err := svc.GetHandover(ctx, &p.payAdmin, p.pay, h.bob.ID)
	if err != nil || len(bobH.Issues) != 2 {
		t.Fatalf("bob 名下应有两张单：%+v %v", bobH, err)
	}
	var bobPlan core.HandoverPlan
	for i := len(bobH.Issues) - 1; i >= 0; i-- { // 倒序：先 asSource 后 asOwner
		it := bobH.Issues[i]
		bobPlan.Issues = append(bobPlan.Issues, core.PlanIssue{IssueID: it.IssueID, As: it.As, ToMemberID: carol.ID, ExpectedVersion: it.Version})
	}
	if bobPlan.Issues[0].IssueID != h.asSource.ID {
		t.Fatalf("bob 的清单应先列 #%d：%+v", h.asSource.ID, bobPlan.Issues)
	}

	gate := closeGate(t, svc)
	first := goErr(func() error {
		return svc.ApplyHandover(ctx, core.WebActor(p.payAdmin), p.pay, p.annPay.ID, annPlan, false)
	})
	testutil.WaitLockWaiters(t, svc.Pool, 1)
	second := goErr(func() error {
		return svc.ApplyHandover(ctx, core.WebActor(p.payAdmin), p.pay, h.bob.ID, bobPlan, false)
	})
	testutil.WaitLockWaiters(t, svc.Pool, 2)
	if err := gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-first; err != nil {
		t.Fatalf("先开始的转交应成功：%v", err)
	}
	var he *core.HandoverError
	if err := <-second; !errors.As(err, &he) {
		t.Fatalf("后开始的转交应报“已有新进展，请刷新清单”，得到：%v", err)
	}
}

// 转交 ann 正要写事件（会按外键引用操作人，即管理员的成员行），同时另一名管理员把 ann 提为管理员
// （锁全部管理员行，再改 ann 的成员行）。修复前转交对 ann 加的是 FOR UPDATE、提权锁管理员行也是 FOR UPDATE，
// 两边都挡住对方需要的锁；修复后成员行改用 FOR NO KEY UPDATE，不再挡外键引用，两边都成功。
func TestHandoverAndSetRoleDoNotDeadlock(t *testing.T) {
	ctx := context.Background()
	h := newHandover(t)
	p, svc := h.p, h.p.e.svc
	gate := closeGate(t, svc)
	handover := goErr(func() error {
		return svc.ApplyHandover(ctx, core.WebActor(p.payAdmin), p.pay, p.annPay.ID, h.fullPlan(h.bob.ID), false)
	})
	testutil.WaitLockWaiters(t, svc.Pool, 1) // 转交已锁住 ann，停在写事件前
	promote := goErr(func() error {
		return svc.SetRole(ctx, core.WebActor(p.payAdmin), p.pay, p.annPay.ID, store.RoleAdmin)
	})
	testutil.WaitLockWaiters(t, svc.Pool, 2) // 提权已锁管理员行，等 ann 的成员行
	if err := gate.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-handover; err != nil {
		t.Fatalf("转交应成功：%v", err)
	}
	if err := <-promote; err != nil {
		t.Fatalf("提权应在转交之后成功：%v", err)
	}
	m, err := store.MemberByID(ctx, svc.Pool, p.annPay.ID)
	if err != nil || !m.IsAdmin() {
		t.Fatalf("ann 应已是管理员：%+v %v", m, err)
	}
}
