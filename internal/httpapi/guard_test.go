package httpapi

import (
	"testing"
	"time"
)

func TestLimitKeyGroupsIPv6By64(t *testing.T) {
	if limitKey("2001:db8:1:2:aaaa::1") != limitKey("2001:db8:1:2:bbbb::9") {
		t.Fatal("同一 /64 内换地址应算同一来源")
	}
	if limitKey("2001:db8:1:2::1") == limitKey("2001:db8:1:3::1") {
		t.Fatal("不同 /64 应分开计数")
	}
	if limitKey("203.0.113.7") != "203.0.113.7" || limitKey("203.0.113.7") == limitKey("203.0.113.8") {
		t.Fatal("IPv4 按完整地址计数")
	}
}

func TestLoginGuardBackoff(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	g := newLoginGuard()
	g.now = func() time.Time { return now }
	for i := 0; i < 4; i++ {
		g.failed("Admin")
	}
	if g.locked("admin") != 0 {
		t.Fatal("前 4 次失败不锁定")
	}
	g.failed("admin")
	if d := g.locked("ADMIN"); d != 30*time.Second {
		t.Fatalf("第 5 次失败锁 30 秒（成员名不分大小写），得到 %v", d)
	}
	g.failed("admin")
	if d := g.locked("admin"); d != 60*time.Second {
		t.Fatalf("再失败一次锁定翻倍，得到 %v", d)
	}
	for i := 0; i < 20; i++ {
		g.failed("admin")
	}
	if d := g.locked("admin"); d != 15*time.Minute {
		t.Fatalf("锁定最长 15 分钟，得到 %v", d)
	}
	now = now.Add(16 * time.Minute)
	if g.locked("admin") != 0 {
		t.Fatal("锁定到期后解除")
	}
	g.succeeded("admin")
	g.failed("admin")
	if g.locked("admin") != 0 {
		t.Fatal("成功登录后清零")
	}
	if g.locked("other") != 0 {
		t.Fatal("别的成员名不受影响")
	}
}
