package mcpserver

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// hostGuard 是带白名单的 DNS 重绑定防护：从本机地址进来的请求，Host 必须是 localhost
// 或白名单里的域名（例如经 Cloudflare 隧道转发来的公网域名），否则返回 403。
// 它替代 MCP 开发包自带的同类防护，后者不支持白名单。
func hostGuard(allowed []string, next http.Handler) http.Handler {
	ok := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	for _, h := range allowed {
		if h = normalizeHost(h); h != "" {
			ok[h] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if viaLoopback(r) && !ok[normalizeHost(r.Host)] {
			http.Error(w, fmt.Sprintf("Forbidden: invalid Host header %q（如经隧道访问，请把该域名加入 COHIVE_ALLOWED_HOSTS）", r.Host), http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func viaLoopback(r *http.Request) bool {
	addr, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return false
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func normalizeHost(h string) string {
	h = strings.TrimSpace(strings.ToLower(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		h = host
	}
	return strings.Trim(h, "[]")
}
