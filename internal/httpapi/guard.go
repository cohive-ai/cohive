package httpapi

import (
	"net"
	"strings"
	"sync"
	"time"
)

// 按登录名的登录失败退避：连续失败达到 loginFreeTries 次后开始锁定，
// 每多失败一次锁定时间翻倍，最长 loginMaxLock。成功登录清零。
const (
	loginFreeTries = 5
	loginBaseLock  = 30 * time.Second
	loginMaxLock   = 15 * time.Minute
	loginGuardMax  = 10000 // 记录的登录名上限，超过时清掉已过期的
)

type loginGuard struct {
	mu    sync.Mutex
	now   func() time.Time
	names map[string]*loginState
}

type loginState struct {
	fails int
	until time.Time
	last  time.Time
}

func newLoginGuard() *loginGuard {
	return &loginGuard{now: time.Now, names: map[string]*loginState{}}
}

func guardKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// locked 返回该登录名还要锁定多久；未锁定时返回 0。
func (g *loginGuard) locked(name string) time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	if st, ok := g.names[guardKey(name)]; ok {
		if d := st.until.Sub(g.now()); d > 0 {
			return d
		}
	}
	return 0
}

// failed 记一次失败，必要时开始或加长锁定。
func (g *loginGuard) failed(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if len(g.names) >= loginGuardMax {
		for k, st := range g.names {
			if now.Sub(st.last) > loginMaxLock && now.After(st.until) {
				delete(g.names, k)
			}
		}
	}
	key := guardKey(name)
	st := g.names[key]
	if st == nil {
		st = &loginState{}
		g.names[key] = st
	}
	st.fails++
	st.last = now
	if st.fails >= loginFreeTries {
		lock := loginBaseLock << (st.fails - loginFreeTries)
		if lock > loginMaxLock || lock <= 0 {
			lock = loginMaxLock
		}
		st.until = now.Add(lock)
	}
}

// succeeded 清除该登录名的失败记录。
func (g *loginGuard) succeeded(name string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.names, guardKey(name))
}

// limitKey 返回限频用的来源键：IPv4 用完整地址；IPv6 按 /64 归并，
// 一个用户通常拥有整个 /64，逐个换地址绕不过限频。
func limitKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.To4() != nil {
		return ip
	}
	return parsed.Mask(net.CIDRMask(64, 128)).String() + "/64"
}
