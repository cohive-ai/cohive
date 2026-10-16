package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/client"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// joinWorkspace 把测试服务上的 lee-codex 加入（或加回）一个工作空间，返回成员资格编号。
func (s testServer) joinWorkspace(t *testing.T, name string) int64 {
	t.Helper()
	ctx := context.Background()
	ws, err := store.WorkspaceByName(ctx, s.svc.Pool, name)
	if err != nil {
		if ws, err = store.CreateWorkspace(ctx, s.svc.Pool, name); err != nil {
			t.Fatal(err)
		}
	}
	m, err := store.AddMembership(ctx, s.svc.Pool, s.accountID, ws.ID, store.RoleMember, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m.ID
}

// 配对时交付全部在册工作空间的令牌与设备密钥；之后新加入的工作空间，再执行 login 直接补签。
func TestLoginSavesAllWorkspacesAndTopsUp(t *testing.T) {
	configDir(t)
	s := newTestServer(t)
	s.joinWorkspace(t, "ops")
	out := s.login(t, "codex")
	if !strings.Contains(out, "default") || !strings.Contains(out, "ops") || strings.Contains(out, "cohive_") {
		t.Fatalf("应列出两个工作空间、不显示令牌：\n%s", out)
	}
	creds, _ := client.LoadCredentials()
	dev, ok := creds.Device(s.url)
	if !ok || !strings.HasPrefix(dev.Secret, "cohive_dev_") || dev.Account != "lee-codex" {
		t.Fatalf("应保存设备密钥：%+v", creds.Devices)
	}
	for _, ws := range []string{"default", "ops"} {
		if l, ok := creds.Find(s.url, ws, "codex"); !ok || l.DeviceID != dev.DeviceID {
			t.Fatalf("应保存 %s 的令牌：%+v", ws, creds.Logins)
		}
	}
	info, err := os.Stat(client.CredentialsPath())
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("凭据文件应仅本人可读：%v %v", info, err)
	}
	s.joinWorkspace(t, "pay")
	out, err = run(t, "login", "--server", s.url, "--tools", "codex")
	if err != nil || !strings.Contains(out, "pay/codex") || !strings.Contains(out, "已登录") {
		t.Fatalf("新加入的工作空间应直接补签：%v\n%s", err, out)
	}
	creds, _ = client.LoadCredentials()
	if _, ok := creds.Find(s.url, "pay", "codex"); !ok {
		t.Fatalf("应保存 pay 的令牌：%+v", creds.Logins)
	}
}

// cohive init：在多个工作空间时必须指定 --workspace，并顺手补签这个工作空间的令牌。
func TestInitPicksWorkspace(t *testing.T) {
	configDir(t)
	s := newTestServer(t)
	s.login(t, "codex")
	s.joinWorkspace(t, "ops")
	dir := t.TempDir()
	if _, err := run(t, "init", "--dir", dir); err == nil || !strings.Contains(err.Error(), "--workspace") {
		t.Fatalf("多个工作空间时应要求指定：%v", err)
	}
	if _, err := run(t, "init", "--dir", dir, "--workspace", "nope"); err == nil || !strings.Contains(err.Error(), "成员") {
		t.Fatalf("不是成员的工作空间应拒绝：%v", err)
	}
	out, err := run(t, "init", "--dir", dir, "--workspace", "ops")
	if err != nil || !strings.Contains(out, "已为工作空间 ops 签发") {
		t.Fatalf("init 应补签 ops 的令牌：%v\n%s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".codex", "config.toml"))
	creds, _ := client.LoadCredentials()
	l, ok := creds.Find(s.url, "ops", "codex")
	if !ok || !strings.Contains(string(b), `"ops"`) || strings.Contains(string(b), l.Token) {
		t.Fatalf("配置应指向 ops 且不含令牌：%v\n%s", ok, b)
	}
}

// 6.2 第 6 条：转发器缺令牌时用设备密钥自动签发；被移出后令牌失效、重签失败、给出提示；
// 加回后转发器收到 401 时重签成功并继续。
func TestProxyAutoIssueAndRefresh(t *testing.T) {
	configDir(t)
	s := newTestServer(t)
	s.login(t, "codex")
	opsMember := s.joinWorkspace(t, "ops")

	sess, stop := startProxy(t, "--server", s.url, "--workspace", "ops", "--tool", "codex")
	if _, err := sess.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("自动签发后应能用：%v", err)
	}
	creds, _ := client.LoadCredentials()
	if _, ok := creds.Find(s.url, "ops", "codex"); !ok {
		t.Fatal("自动签发的令牌应写进凭据文件")
	}
	// 被移出 ops
	if err := store.RemoveMembership(context.Background(), s.svc.Pool, opsMember, nil); err != nil {
		t.Fatal(err)
	}
	_, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "cohive_inbox", Arguments: map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "移出") {
		t.Fatalf("移出后应报错并提示：%v", err)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	// 加回 ops：凭据文件里还是旧令牌，转发器收到 401 后重签
	s.joinWorkspace(t, "ops")
	sess, stop = startProxy(t, "--server", s.url, "--workspace", "ops", "--tool", "codex")
	defer stop()
	if _, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "cohive_inbox", Arguments: map[string]any{}}); err != nil {
		t.Fatalf("加回后应重签并继续：%v", err)
	}
	creds2, _ := client.LoadCredentials()
	old, _ := creds.Find(s.url, "ops", "codex")
	now, _ := creds2.Find(s.url, "ops", "codex")
	if now.Token == old.Token {
		t.Fatal("凭据文件里应换成新令牌")
	}
}

// 转发器：不是成员的工作空间给出明确提示（不报内部错误）。
func TestProxyNotMemberExplains(t *testing.T) {
	configDir(t)
	s := newTestServer(t)
	s.login(t, "codex")
	got := proxyFirstError(t, "--server", s.url, "--workspace", "nope", "--tool", "codex")
	if !strings.Contains(got, "还不是工作空间 nope 的成员") {
		t.Fatalf("应提示不是成员：%s", got)
	}
}

// 6.2 第 16 条：M5 格式的凭据文件（没有 devices）照常能读、令牌照常用；
// 自动签发与 cohive web 给出“旧版配对”的提示或用设备令牌兼容。
func TestLegacyCredentials(t *testing.T) {
	configDir(t)
	s := newTestServer(t)
	// 走一遍配对，再把凭据文件改成 M5 的格式：只有 logins
	s.login(t, "codex")
	creds, _ := client.LoadCredentials()
	l, _ := creds.Find(s.url, "default", "codex")
	m5 := `{"logins":[{"server":"` + s.url + `","workspace":"default","member":"lee-codex","tool":"codex","token":"` + l.Token +
		`","device_id":` + strconv.FormatInt(l.DeviceID, 10) + `,"created_at":"2026-10-16T10:00:00Z"}]}`
	if err := os.WriteFile(client.CredentialsPath(), []byte(m5), 0o600); err != nil {
		t.Fatal(err)
	}
	creds, err := client.LoadCredentials()
	if err != nil || len(creds.Logins) != 1 || len(creds.Devices) != 0 {
		t.Fatalf("应能读 M5 格式：%+v %v", creds, err)
	}
	sess, stop := startProxy(t, "--server", s.url, "--workspace", "default", "--tool", "codex")
	if _, err := sess.ListTools(context.Background(), nil); err != nil {
		t.Fatalf("旧令牌照常用：%v", err)
	}
	_ = stop()
	s.joinWorkspace(t, "ops")
	if got := proxyFirstError(t, "--server", s.url, "--workspace", "ops", "--tool", "codex"); !strings.Contains(got, "旧版配对") {
		t.Fatalf("旧版配对的电脑不能自动签发，应提示重新配对：%s", got)
	}
	if _, err := run(t, "login", "--server", s.url, "--tools", "claude-code"); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("旧版配对补登应提示 --force：%v", err)
	}
	// ann 已是 ops 的在册成员：init 不能误报“不是成员”，应提示旧版配对、重新配对
	_, err = run(t, "init", "--dir", t.TempDir(), "--server", s.url, "--workspace", "ops", "--tools", "codex")
	if err == nil || !strings.Contains(err.Error(), "旧版配对") || !strings.Contains(err.Error(), "--force") || strings.Contains(err.Error(), "不是") {
		t.Fatalf("旧版配对的电脑 init 新工作空间应提示重新配对：%v", err)
	}
	opened := fastDevice(t)
	if _, err := run(t, "web"); err != nil || !strings.HasPrefix(*opened, s.url+"/auth/once/") {
		t.Fatalf("旧版配对的电脑用设备令牌兼容 cohive web：%v %q", err, *opened)
	}
}

// proxyFirstError 启动转发器，发一条初始化请求，返回第一行输出（JSON-RPC 错误）。
func proxyFirstError(t *testing.T, args ...string) string {
	t.Helper()
	inR, inW := io.Pipe()
	var out strings.Builder
	done := make(chan error, 1)
	go func() { done <- runProxy(context.Background(), inR, &out, io.Discard, args) }()
	_, _ = io.WriteString(inW, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`+"\n")
	inW.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("转发器没有退出")
	}
	return out.String()
}

// 只有管理员手工签发的令牌（没有配对过这台电脑）时，init 别的工作空间提示配对，而不是“不是成员”。
func TestInitWithOnlyManualToken(t *testing.T) {
	configDir(t)
	s := newTestServer(t)
	s.joinWorkspace(t, "ops")
	tok := s.token(t, "codex")
	var out strings.Builder
	if err := runCLI(context.Background(), strings.NewReader(tok+"\n"), &out, []string{"login", "--server", s.url, "--token-stdin"}); err != nil {
		t.Fatalf("手工令牌登录：%v\n%s", err, out.String())
	}
	_, err := run(t, "init", "--dir", t.TempDir(), "--workspace", "ops", "--tools", "codex")
	if err == nil || !strings.Contains(err.Error(), "手工签发") || strings.Contains(err.Error(), "不是") {
		t.Fatalf("应提示配对这台电脑：%v", err)
	}
	if got := proxyFirstError(t, "--server", s.url, "--workspace", "ops", "--tool", "codex"); !strings.Contains(got, "cohive login") || strings.Contains(got, "不是") {
		t.Fatalf("转发器也应提示登录而不是误报不是成员：%s", got)
	}
}
