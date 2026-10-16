package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/client"
)

// fastDevice 调快轮询并替换打开浏览器的动作，返回最后打开的链接。
func fastDevice(t *testing.T) *string {
	t.Helper()
	opened := new(string)
	oldInterval, oldWait, oldOpen := devicePollInterval, deviceAdminWait, openURL
	devicePollInterval, deviceAdminWait = 10*time.Millisecond, 200*time.Millisecond
	openURL = func(link string) error { *opened = link; return nil }
	t.Cleanup(func() { devicePollInterval, deviceAdminWait, openURL = oldInterval, oldWait, oldOpen })
	return opened
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

func TestLoginOpensBrowserAndSaves(t *testing.T) {
	configDir(t)
	opened := fastDevice(t)
	s := newTestServer(t)
	go s.approve(t, true)
	out, err := run(t, "login", "--server", s.url+"/", "--tools", "codex,claude-code")
	if err != nil {
		t.Fatalf("登录失败：%v\n%s", err, out)
	}
	if !strings.HasPrefix(*opened, s.url+"/device/da_") || !strings.Contains(out, *opened) || !strings.Contains(out, "█") {
		t.Fatalf("应打开并显示授权页与二维码：%q\n%s", *opened, out)
	}
	if strings.Contains(out, "cohive_") || !strings.Contains(out, "lee-codex") || !strings.Contains(out, "cohive init") {
		t.Fatalf("应显示结果、不显示令牌：%s", out)
	}
	creds, _ := client.LoadCredentials()
	l, ok := creds.Find(s.url, "default", "codex")
	if !ok || l.DeviceID == 0 {
		t.Fatalf("应保存带设备编号的登录：%+v", creds)
	}
	req, _ := http.NewRequest(http.MethodGet, s.url+"/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+l.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("保存的令牌应可用：%v %v", err, resp)
	}
	resp.Body.Close()

	// 已配对：再次登录不再授权
	out, err = run(t, "login", "--server", s.url, "--tools", "codex")
	if err != nil || !strings.Contains(out, "已登录") || !strings.Contains(out, "--force") {
		t.Fatalf("已配对时应直接提示：%v\n%s", err, out)
	}

	// cohive web：免密打开网页
	*opened = ""
	out, err = run(t, "web")
	if err != nil || !strings.HasPrefix(*opened, s.url+"/auth/once/") {
		t.Fatalf("免密打开网页：%v %q\n%s", err, *opened, out)
	}
	status, err := run(t, "status")
	if err != nil || !strings.Contains(status, "lee-codex") || strings.Contains(status, l.Token) {
		t.Fatalf("status 应列出登录且不显示令牌：%v\n%s", err, status)
	}
}

func TestLoginAddsMissingToolWithoutBrowser(t *testing.T) {
	configDir(t)
	opened := fastDevice(t)
	s := newTestServer(t)
	s.login(t, "codex")
	*opened = ""
	out, err := run(t, "login", "--server", s.url, "--tools", "codex,claude-code")
	if err != nil || *opened != "" {
		t.Fatalf("补登录其他工具不应再打开浏览器：%v %q\n%s", err, *opened, out)
	}
	creds, _ := client.LoadCredentials()
	a, _ := creds.Find(s.url, "default", "claude-code")
	b, _ := creds.Find(s.url, "default", "codex")
	if a.DeviceID == 0 || a.DeviceID != b.DeviceID {
		t.Fatalf("应签在同一设备上：%+v %+v", a, b)
	}
}

func TestLoginDenied(t *testing.T) {
	configDir(t)
	fastDevice(t)
	s := newTestServer(t)
	go s.approve(t, false)
	if _, err := run(t, "login", "--server", s.url, "--open=false"); err == nil || !strings.Contains(err.Error(), "拒绝") {
		t.Fatalf("拒绝后应失败：%v", err)
	}
}

func TestLoginAwaitAdminThenResume(t *testing.T) {
	configDir(t)
	fastDevice(t)
	s := newTestServer(t)
	s.login(t, "codex")

	// 第二台设备：换一个凭据目录模拟另一台电脑
	configDir(t)
	go s.approve(t, true)
	if _, err := run(t, "login", "--server", s.url, "--tools", "codex", "--open=false"); err == nil || !strings.Contains(err.Error(), "--resume") {
		t.Fatalf("等管理员超时后应提示稍后领取：%v", err)
	}
	s.adminApprove(t)
	out, err := run(t, "login", "--resume")
	if err != nil || !strings.Contains(out, "已登录") {
		t.Fatalf("批准后领取：%v\n%s", err, out)
	}
	if _, err := run(t, "login", "--resume"); err == nil {
		t.Fatal("领取后不应再有待领取的授权")
	}
}

func TestLoginFromRepoConfig(t *testing.T) {
	configDir(t)
	fastDevice(t)
	s := newTestServer(t)
	repo := t.TempDir()
	writeRepoConfig(t, repo, s.url)
	if out, err := run(t, "login", "--dir", repo); err == nil || !strings.Contains(err.Error(), "没有确认") || !strings.Contains(out, s.url) {
		t.Fatalf("仓库里的陌生地址应先确认：%v\n%s", err, out)
	}
	sub := filepath.Join(repo, "src")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	go s.approve(t, true)
	var buf bytes.Buffer
	err := runCLI(context.Background(), strings.NewReader("\n"), &buf, []string{"login", "--dir", sub, "--open=false"})
	if err != nil || !strings.Contains(buf.String(), "已经配置好") {
		t.Fatalf("从仓库配置登录：%v\n%s", err, buf.String())
	}
	creds, _ := client.LoadCredentials()
	if _, ok := creds.Find(s.url, "default", "claude-code"); !ok {
		t.Fatalf("应按仓库配置登录两个工具：%+v", creds)
	}
}

func TestLoginRejectsForeignAuthURL(t *testing.T) {
	configDir(t)
	opened := fastDevice(t)
	for _, u := range []string{`\\evil\share\x.exe`, "file:///etc/passwd", "https://evil.example.com/other", "-a Calculator"} {
		fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"request_id":1,"poll_secret":"s","expires_in":900,"url":"` + strings.ReplaceAll(u, `\`, `\\`) + `"}`))
		}))
		_, err := run(t, "login", "--server", fake.URL)
		fake.Close()
		if err == nil || *opened != "" {
			t.Fatalf("非法授权地址 %q 应拒绝且不打开：%v %q", u, err, *opened)
		}
	}
	// 合法路径也只拼在已知服务地址后面
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"request_id":1,"poll_secret":"s","expires_in":900,"url":"https://evil.example.com/device/da_abc"}`))
	}))
	defer fake.Close()
	st, err := client.StartDevice(context.Background(), fake.URL, []string{"codex"}, "x")
	if err != nil || st.URL != fake.URL+"/device/da_abc" {
		t.Fatalf("授权页应在已知服务上：%+v %v", st, err)
	}
}

func TestLoginFlagErrors(t *testing.T) {
	configDir(t)
	if _, err := run(t, "login", "--dir", t.TempDir()); err == nil || !strings.Contains(err.Error(), "--server") {
		t.Fatalf("没有地址时应提示：%v", err)
	}
	if _, err := run(t, "login", "--server", "cohive.example.com"); err == nil || !strings.Contains(err.Error(), "http") {
		t.Fatalf("地址缺协议：%v", err)
	}
	dir := t.TempDir()
	writeRepoConfig(t, dir, "https://a.example.com")
	if _, err := run(t, "login", "--dir", dir, "--token-stdin"); err == nil || !strings.Contains(err.Error(), "--server") {
		t.Fatalf("令牌方式应要求明确指定服务地址：%v", err)
	}
	if _, err := run(t, "web"); err == nil || !strings.Contains(err.Error(), "cohive login") {
		t.Fatalf("未配对时 web 应提示先登录：%v", err)
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
	if err := os.WriteFile(filepath.Join(dir, ".codex", "config.toml"),
		[]byte("[mcp_servers.cohive]\nargs = [\"--server=https://b.example.com\", \"--workspace\", \"default\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readRepoTarget(dir); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Fatalf("两份配置不一致应报错：%v", err)
	}
}
