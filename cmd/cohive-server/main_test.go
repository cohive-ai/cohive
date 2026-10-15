package main

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/testutil"
)

// adminEnv 让命令连接到当前测试独占的 schema。
func adminEnv(t *testing.T) {
	t.Helper()
	pool := testutil.NewSchemaPool(t)
	var schema string
	if err := pool.QueryRow(context.Background(), "SELECT current_schema()").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(testutil.DatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	t.Setenv("COHIVE_DATABASE_URL", u.String())
}

func runCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := run(context.Background(), &out, args)
	return out.String(), err
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := runCmd(t, args...)
	if err != nil {
		t.Fatalf("%v 失败：%v\n输出：%s", args, err, out)
	}
	return out
}

func TestAdminCommands(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "member", "add", "--name", "lee-claude", "--display", "Lee（Claude Code）")
	mustRun(t, "member", "add", "--name", "lee-codex")

	out := mustRun(t, "member", "list")
	if !strings.Contains(out, "lee-claude") || !strings.Contains(out, "lee-codex") {
		t.Fatalf("成员列表应包含两位成员：%s", out)
	}

	out = mustRun(t, "token", "issue", "--member", "lee-claude", "--tool", "claude-code")
	if !strings.Contains(out, "cohive_") {
		t.Fatalf("应打印明文令牌：%s", out)
	}

	mustRun(t, "service", "add", "--name", "order-api", "--owner", "lee-codex")
	out = mustRun(t, "service", "list")
	if !strings.Contains(out, "order-api") || !strings.Contains(out, "lee-codex") {
		t.Fatalf("服务列表不符：%s", out)
	}

	if _, err := runCmd(t, "service", "add", "--name", "x", "--owner", "nobody"); err == nil {
		t.Fatal("负责人不存在时应报错")
	}
	if _, err := runCmd(t, "member", "add", "--name", "lee-claude"); err == nil {
		t.Fatal("重复成员名应报错")
	}
	if _, err := runCmd(t, "token", "issue", "--member", "lee-claude"); err == nil {
		t.Fatal("缺少 --tool 应报错")
	}
}

func TestTokenRevoke(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "member", "add", "--name", "a")
	out := mustRun(t, "token", "issue", "--member", "a", "--tool", "codex")
	if !strings.Contains(out, "编号") {
		t.Fatalf("应打印令牌编号：%s", out)
	}
	out = mustRun(t, "token", "list")
	if !strings.Contains(out, "codex") {
		t.Fatalf("令牌列表应包含 codex：%s", out)
	}
	mustRun(t, "token", "revoke", "--id", "1")
	if _, err := runCmd(t, "token", "revoke", "--id", "1"); err == nil {
		t.Fatal("重复吊销应报错")
	}
}

func TestUnknownCommand(t *testing.T) {
	if _, err := runCmd(t, "bogus"); err == nil {
		t.Fatal("未知命令应报错")
	}
}
