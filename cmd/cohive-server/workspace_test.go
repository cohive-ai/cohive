package main

import (
	"strings"
	"testing"
)

func TestWorkspaceCommands(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "workspace", "add", "--name", "payments")
	if _, err := runCmd(t, "workspace", "add", "--name", "payments"); err == nil {
		t.Fatal("重复的工作空间名应报错")
	}
	out := mustRun(t, "workspace", "list")
	if !strings.Contains(out, "payments") {
		t.Fatalf("工作空间列表应包含 payments：%s", out)
	}

	// 同名成员可以分别存在于不同工作空间
	mustRun(t, "member", "add", "--name", "lee")
	mustRun(t, "member", "add", "--workspace", "payments", "--name", "lee")
	mustRun(t, "member", "add", "--workspace", "payments", "--name", "ann")
	out = mustRun(t, "member", "list", "--workspace", "payments")
	if !strings.Contains(out, "ann") {
		t.Fatalf("payments 的成员列表应包含 ann：%s", out)
	}
	if out := mustRun(t, "member", "list"); strings.Contains(out, "ann") {
		t.Fatalf("默认工作空间不应看到 payments 的成员：%s", out)
	}

	mustRun(t, "service", "add", "--workspace", "payments", "--name", "pay-api", "--owner", "ann")
	if _, err := runCmd(t, "service", "add", "--name", "pay-api", "--owner", "ann"); err == nil {
		t.Fatal("默认工作空间里没有 ann，应报错")
	}
	out = mustRun(t, "token", "issue", "--workspace", "payments", "--member", "ann", "--tool", "codex")
	if !strings.Contains(out, "cohive_") {
		t.Fatalf("应签发令牌：%s", out)
	}
	if _, err := runCmd(t, "member", "list", "--workspace", "nope"); err == nil {
		t.Fatal("不存在的工作空间应报错")
	}
}

func TestWorkspaceNotifyAndMemberTelegram(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "workspace", "add", "--name", "payments")
	mustRun(t, "member", "add", "--workspace", "payments", "--name", "ann")

	mustRun(t, "member", "set", "--workspace", "payments", "--name", "ann", "--telegram", "@ann_dev")
	out := mustRun(t, "member", "list", "--workspace", "payments")
	if !strings.Contains(out, "ann_dev") || strings.Contains(out, "@ann_dev") {
		t.Fatalf("应保存去掉 @ 的 Telegram 用户名：%s", out)
	}
	if _, err := runCmd(t, "member", "set", "--workspace", "payments", "--name", "ann", "--telegram", "bad name!"); err == nil {
		t.Fatal("不合法的 Telegram 用户名应报错")
	}

	mustRun(t, "workspace", "notify", "--workspace", "payments", "--telegram-chat", "-1001234567890",
		"--show-title=false", "--public-url", "https://cohive.example.com/")
	out = mustRun(t, "workspace", "list")
	for _, want := range []string{"telegram", "-1001234567890", "标题 关", "简述 开", "https://cohive.example.com"} {
		if !strings.Contains(out, want) {
			t.Fatalf("工作空间列表应显示 %q：%s", want, out)
		}
	}
	mustRun(t, "workspace", "notify", "--workspace", "payments", "--off")
	if out := mustRun(t, "workspace", "list"); !strings.Contains(out, "未配置") {
		t.Fatalf("关闭后应显示未配置：%s", out)
	}
}

func TestMemberTelegramID(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "member", "add", "--name", "lee")
	mustRun(t, "member", "set", "--name", "lee", "--telegram-id", "7012345678")
	out := mustRun(t, "member", "list")
	if !strings.Contains(out, "7012345678") {
		t.Fatalf("成员列表应显示 Telegram 用户编号：%s", out)
	}
	if _, err := runCmd(t, "member", "set", "--name", "lee", "--telegram-id", "abc"); err == nil {
		t.Fatal("非数字的用户编号应报错")
	}
	// 只改用户名时不应清掉已绑定的编号
	mustRun(t, "member", "set", "--name", "lee", "--telegram", "lee_ut")
	if out := mustRun(t, "member", "list"); !strings.Contains(out, "7012345678") || !strings.Contains(out, "lee_ut") {
		t.Fatalf("只改用户名不应影响编号：%s", out)
	}
	if _, err := runCmd(t, "member", "set", "--name", "lee"); err == nil {
		t.Fatal("没有任何要修改的项时应报错")
	}
}
