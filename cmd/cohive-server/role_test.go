package main

import (
	"strings"
	"testing"
)

func TestMemberRoleAndJoinApproval(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "member", "add", "--name", "lee")
	mustRun(t, "member", "set", "--name", "lee", "--role", "admin")
	if out := mustRun(t, "member", "list"); !strings.Contains(out, "管理员") {
		t.Fatalf("成员列表应显示管理员角色：%s", out)
	}
	if _, err := runCmd(t, "member", "set", "--name", "lee", "--role", "boss"); err == nil {
		t.Fatal("不认识的角色应报错")
	}
	if out := mustRun(t, "workspace", "list"); !strings.Contains(out, "加入需审批") {
		t.Fatalf("工作空间列表应显示默认需要审批：%s", out)
	}
	mustRun(t, "workspace", "set", "--workspace", "default", "--join-approval=false")
	if out := mustRun(t, "workspace", "list"); !strings.Contains(out, "自助加入") {
		t.Fatalf("关闭审批后应显示自助加入：%s", out)
	}
}
