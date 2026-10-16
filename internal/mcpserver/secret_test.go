package mcpserver_test

import (
	"bytes"
	"context"
	"log"
	"log/slog"
	"math/rand"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/server"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
)

func fakeSlackToken() string {
	const alnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	r := rand.New(rand.NewSource(7))
	pick := func(set string, n int) string {
		b := make([]byte, n)
		for i := range b {
			b[i] = set[r.Intn(len(set))]
		}
		return string(b)
	}
	return "xoxb-" + pick("0123456789", 12) + "-" + pick("0123456789", 12) + "-" + pick(alnum, 24)
}

// 计划 6.2 第 6 条与主脑补充要求：经完整的服务端路由，按 readOnlyHint=false 枚举全部写工具，
// 每个都喂一段带密钥的正文，必须全部被拒；拒绝时服务端日志里没有请求正文与密钥。
// 以后新增写工具而没有在这里补用例，测试直接失败。
func TestEveryWriteToolRejectsSecretsAndLogsNothing(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	ws, err := store.DefaultWorkspace(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.CreateMember(ctx, pool, ws, "lee-claude", "")
	if err != nil {
		t.Fatal(err)
	}
	tok, _, err := store.IssueToken(ctx, pool, m.ID, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(&core.Service{Pool: pool}, fstest.MapFS{}, server.Options{}))
	t.Cleanup(srv.Close)

	// 截住标准库 log 与 slog 的输出
	var logs bytes.Buffer
	log.SetOutput(&logs)
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { log.SetOutput(os.Stderr); slog.SetDefault(prev) })

	secret := fakeSlackToken()
	body := "调用 Slack 失败\nSLACK_BOT_TOKEN=" + secret
	args := map[string]map[string]any{
		"cohive_draft_issue":          {"title": "通知发不出去", "evidence": body},
		"cohive_submit_issue":         {"issue_id": 1, "expected_version": 1, "note": body},
		"cohive_accept_issue":         {"issue_id": 1, "expected_version": 1, "decision": "decline", "note": body},
		"cohive_update_issue":         {"issue_id": 1, "note": body},
		"cohive_request_verification": {"issue_id": 1, "expected_version": 1, "note": body},
		"cohive_close_issue":          {"issue_id": 1, "expected_version": 1, "verdict": "reopen", "note": body},
	}
	s := mustConnect(t, srv.URL+"/mcp", tok)
	res, err := s.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, tool := range res.Tools {
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint {
			continue
		}
		a, ok := args[tool.Name]
		if !ok {
			t.Fatalf("写工具 %s 没有密钥扫描用例：新增写入正文的入口必须走 core 的密钥检查，并在这里补一条", tool.Name)
		}
		n++
		_, errText := call(t, s, tool.Name, a)
		if !strings.Contains(errText, "疑似有密钥") || !strings.Contains(errText, "第 2 行") || !strings.Contains(errText, "Slack 令牌") {
			t.Errorf("%s 应以密钥命中拒绝，并写明行号与类别，得到 %q", tool.Name, errText)
		}
		assertNoLeak(t, tool.Name+" 的错误", errText, secret)
		if !strings.Contains(tool.Description, "扫描密钥") {
			t.Errorf("%s 的说明应提示写入前扫描密钥", tool.Name)
		}
	}
	if n != len(args) {
		t.Fatalf("应有 %d 个写工具，得到 %d", len(args), n)
	}
	assertNoLeak(t, "服务端日志", logs.String(), secret)
	if strings.Contains(logs.String(), "调用 Slack 失败") {
		t.Fatalf("服务端日志不应出现请求正文：%s", logs.String())
	}
	var issues int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM issues").Scan(&issues); err != nil || issues != 0 {
		t.Fatalf("被拒绝的写入不应建出问题单：%d %v", issues, err)
	}
}

func assertNoLeak(t *testing.T, what, text, secret string) {
	t.Helper()
	for i := 0; i+8 <= len(secret); i++ {
		if strings.Contains(text, secret[i:i+8]) {
			t.Fatalf("%s 里出现了密钥片段 %q", what, secret[i:i+8])
		}
	}
}
