package main

import (
	"strings"
	"testing"
)

// 服务器命令行兜底：账号停用与启用、服务器管理员开关、移出并转交、删除空工作空间。
func TestAccountAndRemoveCommands(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "workspace", "add", "--name", "pay")
	if out := mustRun(t, "member", "add", "--workspace", "pay", "--name", "boss", "--role", "admin"); !strings.Contains(out, "新建账号") {
		t.Fatalf("账号不存在时新建：%s", out)
	}
	mustRun(t, "member", "add", "--workspace", "pay", "--name", "ann")
	mustRun(t, "member", "add", "--workspace", "pay", "--name", "bob")
	if out := mustRun(t, "member", "add", "--name", "ann"); !strings.Contains(out, "已把账号 ann 加入") {
		t.Fatalf("已有账号直接加入第二个工作空间：%s", out)
	}
	mustRun(t, "service", "add", "--workspace", "pay", "--name", "api", "--owner", "ann")

	// 服务器管理员：至少保留一名
	mustRun(t, "account", "server-admin", "--name", "boss", "--on")
	if _, err := runCmd(t, "account", "server-admin", "--name", "boss", "--off"); err == nil || !strings.Contains(err.Error(), "至少") {
		t.Fatalf("不能撤销最后一名服务器管理员：%v", err)
	}
	out := mustRun(t, "account", "list")
	if !strings.Contains(out, "boss") || !strings.Contains(out, "pay（管理员）") {
		t.Fatalf("账号列表：%s", out)
	}

	// 移出并转交：缺 --as 报错；--as 必须是本空间管理员
	if _, err := runCmd(t, "member", "remove", "--workspace", "pay", "--name", "ann", "--handover-to", "bob"); err == nil {
		t.Fatal("缺 --as 应报错")
	}
	if _, err := runCmd(t, "member", "remove", "--workspace", "pay", "--name", "ann", "--handover-to", "bob", "--as", "bob"); err == nil {
		t.Fatal("--as 不是管理员应报错")
	}
	out = mustRun(t, "member", "remove", "--workspace", "pay", "--name", "ann", "--handover-to", "bob", "--as", "boss")
	if !strings.Contains(out, "1 个服务") {
		t.Fatalf("移出：%s", out)
	}
	if out := mustRun(t, "service", "list", "--workspace", "pay"); !strings.Contains(out, "bob") {
		t.Fatalf("服务负责人应转给 bob：%s", out)
	}
	if out := mustRun(t, "member", "list", "--workspace", "pay"); strings.Contains(out, "ann") {
		t.Fatalf("默认只列在册：%s", out)
	}
	if out := mustRun(t, "member", "list", "--workspace", "pay", "--all"); !strings.Contains(out, "已移出") {
		t.Fatalf("--all 列出已移出：%s", out)
	}

	// 停用与启用
	mustRun(t, "account", "disable", "--name", "bob")
	if out := mustRun(t, "member", "list", "--workspace", "pay"); !strings.Contains(out, "已停用") {
		t.Fatalf("应标出已停用：%s", out)
	}
	if _, err := runCmd(t, "service", "add", "--workspace", "pay", "--name", "web", "--owner", "bob"); err == nil {
		t.Fatal("不能把已停用的人登记为负责人")
	}
	if _, err := runCmd(t, "account", "disable", "--name", "boss"); err == nil {
		t.Fatal("不能停用最后一名服务器管理员")
	}
	mustRun(t, "account", "enable", "--name", "bob")

	// 删除工作空间：只能删空的
	mustRun(t, "workspace", "add", "--name", "tmp", "--admin", "boss")
	mustRun(t, "workspace", "remove", "--name", "tmp")
	if out := mustRun(t, "workspace", "list"); strings.Contains(out, "tmp") {
		t.Fatalf("应已删除：%s", out)
	}
	if _, err := runCmd(t, "workspace", "remove", "--name", "nope"); err == nil {
		t.Fatal("不存在的工作空间应报错")
	}
	// 一个群只能绑一个工作空间
	mustRun(t, "workspace", "notify", "--workspace", "pay", "--telegram-chat", "-1001")
	if _, err := runCmd(t, "workspace", "notify", "--telegram-chat", "-1001"); err == nil || !strings.Contains(err.Error(), "pay") {
		t.Fatalf("同一个群不能绑第二个工作空间：%v", err)
	}
	// 重置密码不再要工作空间
	if out := mustRun(t, "admin", "reset-password", "--name", "ann"); !strings.Contains(out, "已重置 ann") {
		t.Fatalf("重置密码：%s", out)
	}
}
