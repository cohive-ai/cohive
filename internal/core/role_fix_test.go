package core_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

func countAdmins(t *testing.T, e env) int {
	t.Helper()
	var n int
	if err := e.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM members WHERE workspace_id = $1 AND role = 'admin'`,
		e.a.WorkspaceID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// 唯一的管理员不能把自己降为成员。
func TestCannotDemoteLastAdmin(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	admin := adminOf(t, e)
	var ie *core.InputError
	if err := e.svc.SetRole(ctx, &admin, admin.ID, store.RoleMember); !errors.As(err, &ie) {
		t.Fatalf("降级最后一名管理员应被拒：%v", err)
	}
	if n := countAdmins(t, e); n != 1 {
		t.Fatalf("应仍有 1 名管理员，实际 %d", n)
	}
	// 有两名管理员时可以降级其中一名
	if err := e.svc.SetRole(ctx, &admin, e.a.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetRole(ctx, &admin, e.a.MemberID, store.RoleMember); err != nil {
		t.Fatalf("还有别的管理员时可以降级：%v", err)
	}
}

// 两名管理员同时互相降级：最终至少保留一名管理员。
func TestConcurrentMutualDemotion(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	a := adminOf(t, e)
	if err := e.svc.SetRole(ctx, &a, e.a.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	b, _ := store.MemberByID(ctx, e.svc.Pool, e.a.MemberID)

	// 先锁住成员表的写入，让两次降级都停在同一起跑线，再一起放行
	hold, err := e.svc.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(ctx, `LOCK TABLE members IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, pair := range [][2]store.Member{{a, b}, {b, a}} {
		wg.Add(1)
		go func(actor, target store.Member) {
			defer wg.Done()
			_ = e.svc.SetRole(ctx, &actor, target.ID, store.RoleMember)
		}(pair[0], pair[1])
	}
	time.Sleep(300 * time.Millisecond)
	_ = hold.Rollback(ctx)
	wg.Wait()
	if n := countAdmins(t, e); n != 1 {
		t.Fatalf("互相降级后应恰好剩 1 名管理员，实际 %d", n)
	}
}
