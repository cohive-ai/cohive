package main

import (
	"strings"
	"testing"
)

// --workspace 漏写值时必须报错，不能悄悄落回 default，否则令牌可能签到错误的工作空间。
func TestWorkspaceFlagRequiresValue(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "member", "add", "--name", "lee")
	for _, args := range [][]string{
		{"token", "issue", "--member", "lee", "--tool", "codex", "--workspace"},
		{"token", "issue", "--workspace", "--member", "lee", "--tool", "codex"},
		{"member", "list", "--workspace="},
	} {
		if _, err := runCmd(t, args...); err == nil || !strings.Contains(err.Error(), "--workspace") {
			t.Fatalf("%v 应报 --workspace 缺值，得到 %v", args, err)
		}
	}
}

func TestTokenRevokeScopedToWorkspace(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "workspace", "add", "--name", "payments")
	mustRun(t, "member", "add", "--name", "lee")
	mustRun(t, "token", "issue", "--member", "lee", "--tool", "codex") // 编号 1，属于 default
	if _, err := runCmd(t, "token", "revoke", "--workspace", "payments", "--id", "1"); err == nil {
		t.Fatal("不应能在 payments 里吊销 default 的令牌")
	}
	mustRun(t, "token", "revoke", "--id", "1")
}

func TestNotifyRetryAndCounts(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	out := mustRun(t, "workspace", "list")
	if !strings.Contains(out, "待发") || !strings.Contains(out, "失败") {
		t.Fatalf("工作空间列表应显示待发与失败条数：%s", out)
	}
	out = mustRun(t, "notify", "retry", "--workspace", "default")
	if !strings.Contains(out, "0 条") {
		t.Fatalf("没有失败通知时应提示 0 条：%s", out)
	}
}

func TestAllowedHostsValidation(t *testing.T) {
	t.Setenv("COHIVE_ALLOWED_HOSTS", " cohive.example.com , other.example.com:443 ")
	hosts, err := allowedHosts()
	if err != nil || len(hosts) != 2 || hosts[0] != "cohive.example.com" {
		t.Fatalf("应解析出两个域名：%v %v", hosts, err)
	}
	for _, bad := range []string{"https://cohive.example.com", "cohive.example.com/", "cohive.example.com/mcp"} {
		t.Setenv("COHIVE_ALLOWED_HOSTS", bad)
		if _, err := allowedHosts(); err == nil || !strings.Contains(err.Error(), "only the hostname") {
			t.Fatalf("%q 应报错并提示只写域名，得到 %v", bad, err)
		}
	}
}

func TestUsageHasNoRemovedCommand(t *testing.T) {
	out := mustRun(t, "help")
	if strings.Contains(out, "telegram chats") {
		t.Fatal("帮助文本不应再出现已删除的 telegram chats 命令")
	}
}
