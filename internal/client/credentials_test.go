package client_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cohive-ai/cohive/internal/client"
)

func TestCredentialsSavedPrivately(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cohive")
	t.Setenv("COHIVE_CONFIG_DIR", dir)

	c, err := client.LoadCredentials()
	if err != nil || len(c.Logins) != 0 {
		t.Fatalf("没有文件时应返回空列表：%+v %v", c, err)
	}
	c.Upsert(client.Login{Server: "https://a.example.com", Workspace: "default", Member: "lee", Tool: "codex", Token: "cohive_x"})
	c.Upsert(client.Login{Server: "https://a.example.com", Workspace: "default", Member: "lee", Tool: "claude-code", Token: "cohive_y"})
	c.Upsert(client.Login{Server: "https://a.example.com", Workspace: "default", Member: "lee", Tool: "codex", Token: "cohive_z"})
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(client.CredentialsPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("凭据文件权限应为 600，实际 %v", info.Mode().Perm())
	}
	if d, _ := os.Stat(dir); d.Mode().Perm() != 0o700 {
		t.Fatalf("配置目录权限应为 700，实际 %v", d.Mode().Perm())
	}

	again, err := client.LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Logins) != 2 {
		t.Fatalf("同一服务、工作空间、工具应只保留最新的一条，得到 %d 条", len(again.Logins))
	}
	l, ok := again.Find("https://a.example.com/", "default", "codex")
	if !ok || l.Token != "cohive_z" {
		t.Fatalf("应找到最新的 codex 令牌（地址末尾的斜杠不影响）：%+v %v", l, ok)
	}
}
