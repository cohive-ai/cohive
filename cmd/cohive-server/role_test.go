package main

import (
	"strings"
	"testing"
)

func TestMemberRole(t *testing.T) {
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
}
