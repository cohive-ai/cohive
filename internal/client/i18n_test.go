package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode"

	"github.com/cohive-ai/cohive/internal/buildinfo"
	"github.com/cohive-ai/cohive/internal/i18n"
)

// TestMain：现有测试断言中文文案，固定用中文跑；英文另有用例。
func TestMain(m *testing.M) {
	i18n.SetDefault(i18n.ZhCN)
	os.Exit(m.Run())
}

func withLang(t *testing.T, l i18n.Lang) {
	t.Helper()
	i18n.SetDefault(l)
	t.Cleanup(func() { i18n.SetDefault(i18n.ZhCN) })
}

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// 发给协作服务 REST 接口的每个请求都带 Accept-Language（命令行的语言），让服务器用同一种语言回报错。
func TestRequestsCarryAcceptLanguage(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		got[r.URL.Path] = r.Header.Get("Accept-Language")
		mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/me":
			_, _ = w.Write([]byte(`{"account":{"Name":"lee"},"workspace":{"Name":"default"},"tool":"codex"}`))
		case "/api/v1/version":
			_, _ = w.Write([]byte(`{"version":"v9.9.9","cli":{"version":"v9.9.9"}}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	for _, l := range []i18n.Lang{i18n.EN, i18n.ZhCN} {
		withLang(t, l)
		clear(got)
		_, _ = Whoami(ctx, srv.URL, "cohive_x")
		_, _ = StartDevice(ctx, srv.URL, []string{"codex"}, "mac")
		_, _ = PollDevice(ctx, srv.URL, 1, "s")
		_, _ = IssueTokens(ctx, srv.URL, "secret", "default", []string{"codex"})
		_, _ = DeviceWorkspaces(ctx, srv.URL, "secret")
		_, _, _ = FetchAlerts(ctx, srv.URL, "default", "cohive_x", nil)
		_, _ = WebLogin(ctx, srv.URL, "secret")
		_ = RevokeOwnDevice(ctx, srv.URL, "secret", 3)
		old := buildinfo.Version
		buildinfo.Version = "v1.0.0"
		_ = VersionWarning(ctx, srv.URL)
		buildinfo.Version = old
		for _, p := range []string{"/api/v1/me", "/api/v1/device/start", "/api/v1/device/poll", "/api/v1/device/tokens",
			"/api/v1/device/workspaces", "/api/v1/w/default/alerts", "/api/v1/auth/web-login", "/api/v1/me/devices/3/revoke", "/api/v1/version"} {
			if got[p] != string(l) {
				t.Errorf("语言 %s：%s 的 Accept-Language = %q", l, p, got[p])
			}
		}
	}
}

// 转发 MCP 的请求不带 Accept-Language：MCP 的语言由服务器决定。
func TestProxyDoesNotSendAcceptLanguage(t *testing.T) {
	withLang(t, i18n.EN)
	var header string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("Accept-Language")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()
	p := &Proxy{Server: srv.URL, Token: "cohive_x"}
	var out strings.Builder
	if err := p.Run(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), &out); err != nil {
		t.Fatal(err)
	}
	if header != "" || !strings.Contains(out.String(), `"result"`) {
		t.Fatalf("Accept-Language = %q，输出 %s", header, out.String())
	}
}

func TestErrorsEnglish(t *testing.T) {
	withLang(t, i18n.EN)
	ctx := context.Background()
	if _, err := CheckServer("cohive.example.com"); err == nil || err.Error() !=
		`Invalid server URL "cohive.example.com": it should look like https://cohive.example.com (with http:// or https://)` {
		t.Fatalf("CheckServer：%v", err)
	}

	status := http.StatusUnauthorized
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/device/poll":
			w.WriteHeader(http.StatusNotFound)
		case "/api/v1/device/tokens":
			w.WriteHeader(http.StatusNotFound)
		case "/api/v1/device/workspaces":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"服务器给的原话"}`))
		case "/api/v1/auth/web-login":
			_, _ = w.Write([]byte(`not json`))
		default:
			w.WriteHeader(status)
		}
	}))
	defer srv.Close()
	if _, err := Whoami(ctx, srv.URL, "cohive_x"); err == nil || err.Error() != "The token is invalid or has been revoked" {
		t.Fatalf("Whoami：%v", err)
	}
	if _, err := PollDevice(ctx, srv.URL, 1, "s"); !errors.Is(err, ErrDeviceGone) {
		t.Fatalf("PollDevice：%v", err)
	}
	if ErrDeviceGone.Error() != "This device authorization is no longer valid; run cohive login again" {
		t.Fatalf("ErrDeviceGone：%v", ErrDeviceGone)
	}
	_, err := IssueTokens(ctx, srv.URL, "secret", "alpha", []string{"codex"})
	if !errors.Is(err, ErrNotMember) || err.Error() != "Workspace alpha: You are not a member of this workspace; contact its admin" {
		t.Fatalf("IssueTokens：%v", err)
	}
	if _, err := DeviceWorkspaces(ctx, srv.URL, "secret"); err == nil || err.Error() != "服务器给的原话" {
		t.Fatalf("服务器自己的报错应原样转达：%v", err)
	}
	if _, err := WebLogin(ctx, srv.URL, "secret"); err == nil || err.Error() != "The Cohive server returned something unrecognizable (HTTP 200); check the server URL" {
		t.Fatalf("WebLogin：%v", err)
	}
	if _, err := post(ctx, srv.URL, "/x", "tok", map[string]any{}, &struct{}{}); err == nil || !strings.HasPrefix(err.Error(), "The device key or token saved on this computer is invalid or revoked") {
		t.Fatalf("401 带令牌：%v", err)
	}
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	if _, err := Whoami(ctx, dead.URL, "cohive_x"); err == nil || !strings.HasPrefix(err.Error(), "Failed to connect to the Cohive server: ") || hasHan(err.Error()) {
		t.Fatalf("连接失败：%v", err)
	}
	// 同一个错误在不同语言里渲染不同
	i18n.SetDefault(i18n.ZhCN)
	if ErrNotMember.Error() != "你还不是这个工作空间的成员，请联系它的管理员" {
		t.Fatalf("中文：%v", ErrNotMember)
	}
}

func TestVersionWarningEnglish(t *testing.T) {
	withLang(t, i18n.EN)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"v1.2.0","cli":{"version":"v1.2.0"}}`))
	}))
	defer srv.Close()
	old := buildinfo.Version
	defer func() { buildinfo.Version = old }()
	buildinfo.Version = "v1.1.0"
	want := "Note: this computer has cohive v1.1.0, but the Cohive server " + srv.URL + " provides v1.2.0. Reinstalling is recommended:\n" +
		"  macOS / Linux: curl -fsSL " + srv.URL + "/install.sh | sh\n  Windows: irm " + srv.URL + "/install.ps1 | iex"
	if got := VersionWarning(context.Background(), srv.URL); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestCredentialErrorsEnglish(t *testing.T) {
	withLang(t, i18n.EN)
	dir := t.TempDir()
	t.Setenv("COHIVE_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "credentials.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCredentials(); err == nil || !strings.HasPrefix(err.Error(), "The credentials file has the wrong format: ") {
		t.Fatalf("LoadCredentials：%v", err)
	}
	t.Setenv("COHIVE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	if _, err := ConfigDir(); err == nil || !strings.Contains(err.Error(), "COHIVE_CONFIG_DIR") || hasHan(err.Error()) {
		t.Fatalf("ConfigDir：%v", err)
	}
	t.Setenv("COHIVE_CACHE_DIR", "")
	if _, err := HookStatePath("codex", ""); err == nil || err.Error() != "No session ID" {
		t.Fatalf("HookStatePath：%v", err)
	}
}

func TestProxyMessagesEnglish(t *testing.T) {
	withLang(t, i18n.EN)
	var out strings.Builder
	in := strings.NewReader("not json\n" + `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n")
	unauthorized := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer unauthorized.Close()
	p := &Proxy{Server: unauthorized.URL, Token: "cohive_x", Log: &strings.Builder{}}
	if err := p.Run(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Parse error: the input is not valid JSON", "The Cohive token is invalid or has been revoked. If you were removed from this workspace"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("输出应包含 %q：%s", want, out.String())
		}
	}
	if hasHan(out.String()) {
		t.Errorf("英文输出里不应有汉字：%s", out.String())
	}

	// 令牌被拒、重签也失败、以及不再是成员
	out.Reset()
	p = &Proxy{Server: unauthorized.URL, Token: "cohive_x", Log: &strings.Builder{},
		Refresh: func(context.Context) (string, error) { return "", ErrNotMember }}
	_ = p.Run(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), &out)
	if !strings.Contains(out.String(), "you are no longer a member of this workspace") {
		t.Errorf("不再是成员：%s", out.String())
	}
	out.Reset()
	p = &Proxy{Server: unauthorized.URL, Token: "cohive_x", Log: &strings.Builder{},
		Refresh: func(context.Context) (string, error) { return "", errors.New("boom") }}
	_ = p.Run(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), &out)
	if !strings.Contains(out.String(), "reissuing it failed too: boom") {
		t.Errorf("重签失败：%s", out.String())
	}

	// 连接失败、服务返回非 2xx
	out.Reset()
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	p = &Proxy{Server: dead.URL, Token: "cohive_x"}
	_ = p.Run(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), &out)
	if !strings.Contains(out.String(), "Failed to connect to the Cohive server "+dead.URL) {
		t.Errorf("连接失败：%s", out.String())
	}
	out.Reset()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("oops"))
	}))
	defer bad.Close()
	p = &Proxy{Server: bad.URL, Token: "cohive_x"}
	_ = p.Run(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), &out)
	if !strings.Contains(out.String(), "The Cohive server returned HTTP 502: oops") {
		t.Errorf("HTTP 错误：%s", out.String())
	}
}
