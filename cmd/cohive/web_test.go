package main

import (
	"net/http"
	"net/http/httptest"
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
