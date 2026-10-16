package httpapi

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestKeyedLimiter(t *testing.T) {
	l := &keyedLimiter{max: 2, window: time.Minute}
	if !l.allow("a") || !l.allow("a") || l.allow("a") {
		t.Fatal("同一来源超过上限应拒绝")
	}
	if !l.allow("b") {
		t.Fatal("不同来源互不影响")
	}
}

func TestClientIP(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.RemoteAddr = "127.0.0.1:5000"
	r.Header.Set("CF-Connecting-IP", "203.0.113.7")
	if got := clientIP(r); got != "203.0.113.7" {
		t.Fatalf("经本机隧道转发时应取真实地址：%s", got)
	}
	r.RemoteAddr = "198.51.100.9:5000"
	if got := clientIP(r); got != "198.51.100.9" {
		t.Fatalf("非本机来源不信任转发头：%s", got)
	}
	r.RemoteAddr = "[::1]:5000"
	r.Header.Set("CF-Connecting-IP", "not-an-ip")
	if got := clientIP(r); got != "::1" {
		t.Fatalf("转发头不是地址时忽略：%s", got)
	}
}
