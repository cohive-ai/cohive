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

// cohive web：网页登录属于账号，不分工作空间；用设备密钥换链接。配对了多个服务时要求指定服务。
// M5 配对的旧电脑没有设备密钥时，用属于设备的令牌兼容。
func TestWebPicksServer(t *testing.T) {
	configDir(t)
	opened := fastDevice(t)
	var gotCred string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCred = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		_, _ = w.Write([]byte(`{"url":"/auth/once/abc"}`))
	}))
	defer fake.Close()
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"url":"/auth/once/zzz"}`))
	}))
	defer other.Close()
	creds := &client.Credentials{}
	creds.SetDevice(client.Device{Server: fake.URL, Account: "lee", DeviceID: 1, Secret: "cohive_dev_a"})
	creds.Upsert(client.Login{Server: fake.URL, Workspace: "alpha", Member: "lee", Tool: "codex", Token: "tok-alpha", DeviceID: 1, CreatedAt: time.Now()})
	creds.Upsert(client.Login{Server: other.URL, Workspace: "beta", Member: "lee", Tool: "codex", Token: "tok-beta", DeviceID: 2, CreatedAt: time.Now()})
	if err := creds.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "web", "--dir", t.TempDir()); err == nil || !strings.Contains(err.Error(), "--server") {
		t.Fatalf("两个服务时应要求指定：%v", err)
	}
	if _, err := run(t, "web", "--dir", t.TempDir(), "--server", fake.URL); err != nil {
		t.Fatal(err)
	}
	if gotCred != "cohive_dev_a" || *opened != fake.URL+"/auth/once/abc" {
		t.Fatalf("应使用设备密钥：%q %q", gotCred, *opened)
	}
	// 在仓库里：用仓库配置的服务
	repo := t.TempDir()
	writeRepoConfig(t, repo, fake.URL)
	gotCred = ""
	if _, err := run(t, "web", "--dir", repo); err != nil || gotCred != "cohive_dev_a" {
		t.Fatalf("应按仓库的服务找到设备密钥：%v %q", err, gotCred)
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
