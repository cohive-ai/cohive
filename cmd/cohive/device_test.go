package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/client"
)

// fastDevice 调快轮询并替换打开链接的动作，返回被打开的链接。
func fastDevice(t *testing.T) *string {
	t.Helper()
	opened := new(string)
	oldInterval, oldOpen := devicePollInterval, openURL
	devicePollInterval = 10 * time.Millisecond
	openURL = func(link string) error { *opened = link; return nil }
	t.Cleanup(func() { devicePollInterval, openURL = oldInterval, oldOpen })
	return opened
}

// approveDevice 模拟成员在机器人私聊里打开链接并点数字；pick 根据终端数字决定点哪个。
func (s testServer) approveDevice(t *testing.T, pick func(match int) int) {
	ctx := context.Background()
	for i := 0; i < 300; i++ {
		var id int64
		var nonce string
		var match int
		err := s.svc.Pool.QueryRow(ctx, `SELECT id, start_nonce, match_number FROM device_logins
			WHERE status = 'pending' ORDER BY id DESC LIMIT 1`).Scan(&id, &nonce, &match)
		if err == nil {
			if _, err := s.svc.OpenDeviceLogin(ctx, "dl_"+nonce, s.uid); err != nil {
				t.Error(err)
				return
			}
			_, _ = s.svc.ConfirmDeviceLogin(ctx, id, s.uid, pick(match))
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("没有等到登录请求")
}

func writeRepoConfig(t *testing.T, dir, server string) {
	t.Helper()
	mcp := `{"mcpServers":{"cohive":{"type":"stdio","command":"cohive","args":["mcp-proxy","--server","` + server +
		`","--workspace","default","--tool","claude-code"]}}}`
	codex := "[mcp_servers.cohive]\ncommand = \"cohive\"\nargs = [\"mcp-proxy\", \"--server\", \"" + server +
		"\", \"--workspace\", \"default\", \"--tool\", \"codex\"]\n"
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(mcp), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".codex", "config.toml"), []byte(codex), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDeviceLoginFromRepo(t *testing.T) {
	configDir(t)
	opened := fastDevice(t)
	s := newTestServer(t)
	repo := t.TempDir()
	writeRepoConfig(t, repo, s.url)

	go s.approveDevice(t, func(m int) int { return m })
	out, err := run(t, "login", "--dir", repo)
	if err != nil {
		t.Fatalf("登录失败：%v\n%s", err, out)
	}
	if !strings.HasPrefix(*opened, "https://t.me/CohiveBot?start=dl_") || !strings.Contains(out, *opened) {
		t.Fatalf("应打开并显示确认链接：%q\n%s", *opened, out)
	}
	if !strings.Contains(out, "█") || !strings.Contains(out, "点选这个数字") || !strings.Contains(out, "已经配置好") {
		t.Fatalf("应显示二维码、数字和后续提示：%s", out)
	}
	if strings.Contains(out, "cohive_") {
		t.Fatalf("输出里不应出现令牌：%s", out)
	}
	creds, err := client.LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"claude-code", "codex"} {
		if l, ok := creds.Find(s.url, "default", tool); !ok || l.Member != "lee-codex" {
			t.Fatalf("应按仓库配置保存 %s 的登录：%+v", tool, creds)
		}
	}
}

func TestDeviceLoginWrongNumber(t *testing.T) {
	configDir(t)
	fastDevice(t)
	s := newTestServer(t)
	go s.approveDevice(t, func(m int) int { return m + 1 })
	out, err := run(t, "login", "--server", s.url, "--tools", "codex", "--open=false")
	if err == nil || !strings.Contains(err.Error(), "没有通过") {
		t.Fatalf("选错数字应失败：%v\n%s", err, out)
	}
}

func TestDeviceLoginNeedsServer(t *testing.T) {
	configDir(t)
	if _, err := run(t, "login", "--dir", t.TempDir()); err == nil || !strings.Contains(err.Error(), "--server") {
		t.Fatalf("没有仓库配置时应提示 --server：%v", err)
	}
}

func TestReadRepoTarget(t *testing.T) {
	dir := t.TempDir()
	if tg, err := readRepoTarget(dir); err != nil || tg.Server != "" {
		t.Fatalf("没有配置时应为空：%+v %v", tg, err)
	}
	writeRepoConfig(t, dir, "https://a.example.com/")
	tg, err := readRepoTarget(dir)
	if err != nil || tg.Server != "https://a.example.com" || tg.Workspace != "default" || strings.Join(tg.Tools, ",") != "claude-code,codex" {
		t.Fatalf("读取结果不符：%+v %v", tg, err)
	}
	// 两份配置指向不同服务
	if err := os.WriteFile(filepath.Join(dir, ".codex", "config.toml"),
		[]byte("[mcp_servers.cohive]\nargs = [\"--server=https://b.example.com\", \"--workspace\", \"default\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRepoTarget(dir); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("两份配置不一致应报错：%v", err)
	}
}
