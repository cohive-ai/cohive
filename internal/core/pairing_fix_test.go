package core_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/store"
)

// 吊销全部设备后再配对不算首台，仍需管理员批准（离职成员不能靠密码免审重新配对）。
func TestFirstDeviceIgnoresRevoked(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	me, _ := store.MemberByID(ctx, e.svc.Pool, e.a.MemberID)
	st, _ := e.svc.StartDeviceAuth(ctx, []string{"codex"}, "a")
	if a, err := e.svc.AuthorizeDevice(ctx, me, st.Nonce, true); err != nil || a.Status != store.AuthApproved {
		t.Fatalf("首台应免审：%+v %v", a, err)
	}
	p, _ := e.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret)
	if err := e.svc.RevokeDevice(ctx, me, p.DeviceID); err != nil {
		t.Fatal(err)
	}
	st2, _ := e.svc.StartDeviceAuth(ctx, []string{"codex"}, "b")
	if v, _ := e.svc.ViewDeviceAuth(ctx, me, st2.Nonce); v.FirstDevice {
		t.Fatal("吊销过设备的成员不应再显示为首台")
	}
	if a, err := e.svc.AuthorizeDevice(ctx, me, st2.Nonce, true); err != nil || a.Status != store.AuthAwaitingAdmin {
		t.Fatalf("吊销全部设备后再配对应等管理员：%+v %v", a, err)
	}
}

// 同一成员并发多次授权，只有一次按首台免审。
func TestFirstDeviceConcurrent(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	me, _ := store.MemberByID(ctx, e.svc.Pool, e.a.MemberID)
	const n = 6
	nonces := make([]string, n)
	for i := range nonces {
		st, err := e.svc.StartDeviceAuth(ctx, []string{"codex"}, "x")
		if err != nil {
			t.Fatal(err)
		}
		nonces[i] = st.Nonce
	}
	// 先锁住设备表的写入，让各次授权都停在“已统计、待建设备”处，再一起放行，确定性地制造竞争
	hold, err := e.svc.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(ctx, `LOCK TABLE devices IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	statuses := make(chan string, n)
	var wg sync.WaitGroup
	for _, nonce := range nonces {
		wg.Add(1)
		go func(nonce string) {
			defer wg.Done()
			a, err := e.svc.AuthorizeDevice(ctx, me, nonce, true)
			if err != nil {
				t.Error(err)
				return
			}
			statuses <- a.Status
		}(nonce)
	}
	time.Sleep(300 * time.Millisecond)
	_ = hold.Rollback(ctx)
	wg.Wait()
	close(statuses)
	approved := 0
	for s := range statuses {
		if s == store.AuthApproved {
			approved++
		}
	}
	if approved != 1 {
		t.Fatalf("并发授权只能有一次免审，实际 %d 次", approved)
	}
}
