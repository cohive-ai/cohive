package mcpserver

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// hostGuard 是带白名单的 DNS 重绑定防护：从本机地址进来的请求，Host 必须是 localhost、
// 白名单里的域名（COHIVE_ALLOWED_HOSTS），或服务器级对外地址的主机名（dynamic 返回，可为空），否则返回 403。
// 它替代 MCP 开发包自带的同类防护，后者不支持白名单。
func hostGuard(allowed []string, dynamic func() string, next http.Handler) http.Handler {
	ok := map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}
	for _, h := range allowed {
		if h = normalizeHost(h); h != "" {
			ok[h] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := normalizeHost(r.Host)
		if viaLoopback(r) && !ok[h] && (dynamic == nil || h == "" || h != normalizeHost(dynamic())) {
			http.Error(w, fmt.Sprintf("Forbidden: invalid Host header %q (when accessing through a tunnel, set the external URL under Server admin > Settings in the web UI, or add the domain to COHIVE_ALLOWED_HOSTS)", r.Host), http.StatusForbidden)
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
