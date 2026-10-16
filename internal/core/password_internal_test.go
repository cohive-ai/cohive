package core

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 同时进行的密码哈希不超过上限。
func TestHashSlotsBounded(t *testing.T) {
	var cur, peak int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = withHashSlot(context.Background(), func() {
				n := atomic.AddInt32(&cur, 1)
				for {
					p := atomic.LoadInt32(&peak)
					if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
						break
					}
				}
				time.Sleep(20 * time.Millisecond)
				atomic.AddInt32(&cur, -1)
			})
		}()
	}
	wg.Wait()
	if peak > 4 || peak < 2 {
		t.Fatalf("并发峰值应在 2 到 4 之间，得到 %d", peak)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for i := 0; i < cap(hashSlots); i++ {
		hashSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(hashSlots); i++ {
			<-hashSlots
		}
	}()
	if err := withHashSlot(ctx, func() {}); err == nil {
		t.Fatal("名额占满且请求已取消时应返回错误，不应一直等")
	}
}
