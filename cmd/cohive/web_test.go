package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/client"
)

// B18：同一服务上配对了两个工作空间时，cohive web 要求指定工作空间；指定后用对应的令牌。
func TestWebPicksWorkspace(t *testing.T) {
	configDir(t)
	opened := fastDevice(t)
	var gotToken string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_, _ = w.Write([]byte(`{"url":"/auth/once/abc"}`))
	}))
	defer fake.Close()
	creds := &client.Credentials{}
	creds.Upsert(client.Login{Server: fake.URL, Workspace: "alpha", Member: "lee", Tool: "codex", Token: "tok-alpha", DeviceID: 1, CreatedAt: time.Now()})
	creds.Upsert(client.Login{Server: fake.URL, Workspace: "beta", Member: "lee", Tool: "codex", Token: "tok-beta", DeviceID: 2, CreatedAt: time.Now()})
	if err := creds.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "web", "--dir", t.TempDir()); err == nil || !strings.Contains(err.Error(), "--workspace") {
		t.Fatalf("两个工作空间时应要求指定：%v", err)
	}
	if _, err := run(t, "web", "--dir", t.TempDir(), "--workspace", "beta"); err != nil {
		t.Fatal(err)
	}
	if gotToken != "tok-beta" || *opened != fake.URL+"/auth/once/abc" {
		t.Fatalf("应使用 beta 的令牌：%q %q", gotToken, *opened)
	}
	// 在仓库里：用仓库配置的工作空间
	repo := t.TempDir()
	writeRepoConfig(t, repo, fake.URL)
	// writeRepoConfig 写的工作空间是 default；本机没有 default 的配对，应提示先登录
	if _, err := run(t, "web", "--dir", repo); err == nil || !strings.Contains(err.Error(), "cohive login") {
		t.Fatalf("应按仓库的工作空间查找：%v", err)
	}
}

// B19：子目录里有一份不含 cohive 的 .mcp.json 时，继续往上找到仓库里的 Cohive 配置。
func TestFindRepoDirSkipsForeignConfig(t *testing.T) {
	repo := t.TempDir()
	writeRepoConfig(t, repo, "https://a.example.com")
	sub := filepath.Join(repo, "tools", "x")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, ".mcp.json"), []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readRepoTarget(findRepoDir(sub))
	if err != nil || got.Server != "https://a.example.com" {
		t.Fatalf("应找到上层的 Cohive 配置：%+v %v", got, err)
	}
	// 遇到仓库根（.git）就停，不再往上
	inner := filepath.Join(repo, "vendor", "lib")
	if err := os.MkdirAll(filepath.Join(inner, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if findRepoDir(inner) != inner {
		t.Fatalf("遇到 .git 应停下：%s", findRepoDir(inner))
	}
}
