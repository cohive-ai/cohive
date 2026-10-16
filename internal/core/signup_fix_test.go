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

// B4：用两个不同的团队邀请同时申请同一个登录名：一个成功，另一个得到“已有人申请”，不是内部错误。
func TestConcurrentSameNameSignup(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	admin := adminOf(t, e)
	codes := []string{
		invite(t, e, admin, core.InviteSpec{Kind: store.InviteTeam}),
		invite(t, e, admin, core.InviteSpec{Kind: store.InviteTeam}),
	}
	// 锁住注册申请表的写入，让两次注册都通过前面的重名检查后再一起插入
	hold, err := e.svc.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hold.Exec(ctx, `LOCK TABLE signups IN EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, c := range codes {
		wg.Add(1)
		go func(code string) {
			defer wg.Done()
			_, err := e.svc.Register(ctx, core.RegisterInput{Code: code, Name: "dup", Password: goodPassword})
			errs <- err
		}(c)
	}
	time.Sleep(1500 * time.Millisecond) // 两次注册各要算一次密码哈希
	_ = hold.Rollback(ctx)
	wg.Wait()
	close(errs)
	ok, taken := 0, 0
	var ie *core.InputError
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.As(err, &ie):
			taken++
		default:
			t.Fatalf("不应返回内部错误：%v", err)
		}
	}
	if ok != 1 || taken != 1 {
		t.Fatalf("应一个成功、一个提示重名：成功 %d，重名 %d", ok, taken)
	}
}
