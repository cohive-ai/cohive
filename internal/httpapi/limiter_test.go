package httpapi

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 并发请求不能绕过上限：先占名额再查库，同一窗口内进入查库的失败请求不超过上限。
func TestLimiterConcurrentAttemptsBounded(t *testing.T) {
	l := &failureLimiter{max: 30, window: time.Minute}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if l.begin() {
				admitted.Add(1)
				time.Sleep(5 * time.Millisecond) // 模拟查库
				l.end(true)
			}
		}()
	}
	close(start)
	wg.Wait()
	if n := admitted.Load(); n > 30 {
		t.Fatalf("并发时进入查库的请求不应超过 30 个，实际 %d 个", n)
	}
}

func TestLimiterSuccessDoesNotCount(t *testing.T) {
	l := &failureLimiter{max: 2, window: time.Minute}
	for i := 0; i < 10; i++ {
		if !l.begin() {
			t.Fatal("成功的换取不应占用失败名额")
		}
		l.end(false)
	}
}

func TestLimiterWindowRolls(t *testing.T) {
	l := &failureLimiter{max: 1, window: 20 * time.Millisecond}
	if !l.begin() {
		t.Fatal("第一次应放行")
	}
	l.end(true)
	if l.begin() {
		t.Fatal("达到上限后应拒绝")
	}
	time.Sleep(30 * time.Millisecond)
	if !l.begin() {
		t.Fatal("窗口过去后应重新放行")
	}
	l.end(true)
}
