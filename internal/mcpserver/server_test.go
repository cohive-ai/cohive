package mcpserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/mcpserver"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type bearer struct {
	token string
	base  http.RoundTripper
}

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

type fixture struct {
	url            string
	tokenA, tokenB string
	tokenAID       int64
	revoke         func(id int64)
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	ws, err := store.DefaultWorkspace(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := store.CreateMember(ctx, pool, ws, "lee-claude", "")
	b, _ := store.CreateMember(ctx, pool, ws, "lee-codex", "")
	ta, taID, err := store.IssueToken(ctx, pool, a.ID, "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	tb, _, err := store.IssueToken(ctx, pool, b.ID, "codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertService(ctx, pool, ws, "order-api", b.ID); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(mcpserver.Handler(&core.Service{Pool: pool}))
	t.Cleanup(srv.Close)
	return fixture{url: srv.URL + "/mcp", tokenA: ta, tokenB: tb, tokenAID: taID,
		revoke: func(id int64) {
			if err := store.RevokeToken(ctx, pool, id); err != nil {
				t.Fatal(err)
			}
		}}
}

func connect(t *testing.T, url, token string) (*mcp.ClientSession, error) {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	return client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:   url,
		HTTPClient: &http.Client{Transport: bearer{token: token, base: http.DefaultTransport}},
		MaxRetries: -1,
	}, nil)
}

func mustConnect(t *testing.T, url, token string) *mcp.ClientSession {
	t.Helper()
	s, err := connect(t, url, token)
	if err != nil {
		t.Fatalf("连接失败：%v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// call 调用工具，返回结构化结果；工具报错时返回错误文本。
func call(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) (map[string]any, string) {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("调用 %s 协议错误：%v", name, err)
	}
	if res.IsError {
		var sb strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				sb.WriteString(tc.Text)
			}
		}
		return nil, sb.String()
	}
	b, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("解析 %s 结果：%v", name, err)
	}
	return out, ""
}

func mustCall(t *testing.T, s *mcp.ClientSession, name string, args map[string]any) map[string]any {
	t.Helper()
	out, errText := call(t, s, name, args)
	if errText != "" {
		t.Fatalf("%s 失败：%s", name, errText)
	}
	return out
}

func idv(m map[string]any) (float64, float64) {
	return m["id"].(float64), m["version"].(float64)
}

func TestFullLoopOverMCP(t *testing.T) {
	f := newFixture(t)
	a := mustConnect(t, f.url, f.tokenA)
	b := mustConnect(t, f.url, f.tokenB)

	d := mustCall(t, a, "cohive_draft_issue", map[string]any{
		"title": "下单接口返回 500", "expected": "200", "actual": "500",
		"repro_script": "curl -sf localhost:8080/orders", "target_service": "order-api",
	})
	if d["status"] != "DRAFT" || d["owner"] != "lee-codex" {
		t.Fatalf("草稿结果不符：%v", d)
	}
	id, v := idv(d)

	if _, errText := call(t, b, "cohive_get_issue", map[string]any{"issue_id": id}); errText == "" {
		t.Fatal("别人的草稿不应可见")
	}

	o := mustCall(t, a, "cohive_submit_issue", map[string]any{"issue_id": id, "expected_version": v})
	_, v = idv(o)

	inbox := mustCall(t, b, "cohive_inbox", map[string]any{})
	items := inbox["items"].([]any)
	if len(items) != 1 || !strings.Contains(items[0].(map[string]any)["needs_me"].(string), "接单") {
		t.Fatalf("b 的收件箱应有一张待接单：%v", inbox)
	}

	acc := mustCall(t, b, "cohive_accept_issue", map[string]any{"issue_id": id, "expected_version": v, "decision": "accept"})
	_, v = idv(acc)
	fx := mustCall(t, b, "cohive_update_issue", map[string]any{"issue_id": id, "expected_version": v, "start_fixing": true, "note": "定位到参数校验"})
	_, v = idv(fx)
	rv := mustCall(t, b, "cohive_request_verification", map[string]any{"issue_id": id, "expected_version": v, "note": "已修复，见合并请求 !12"})
	_, v = idv(rv)

	if _, errText := call(t, b, "cohive_close_issue", map[string]any{"issue_id": id, "expected_version": v, "verdict": "pass"}); !strings.Contains(errText, "发起人") {
		t.Fatalf("负责人关闭应被拒绝并说明需发起人，得到：%q", errText)
	}

	cl := mustCall(t, a, "cohive_close_issue", map[string]any{"issue_id": id, "expected_version": v, "verdict": "pass"})
	if cl["status"] != "CLOSED" {
		t.Fatalf("应已关闭：%v", cl)
	}

	got := mustCall(t, b, "cohive_get_issue", map[string]any{"issue_id": id})
	evs := got["events"].([]any)
	// 起草、发出、接单、开始修复、请求验证、关闭；被拒绝的关闭不产生事件
	if len(evs) != 6 {
		t.Fatalf("应有 6 条事件，得到 %d", len(evs))
	}
	first := evs[0].(map[string]any)
	if first["actor"] != "lee-claude" || first["tool"] != "claude-code" {
		t.Fatalf("首条事件身份不符：%v", first)
	}
}

func TestVersionConflictMessage(t *testing.T) {
	f := newFixture(t)
	a := mustConnect(t, f.url, f.tokenA)
	d := mustCall(t, a, "cohive_draft_issue", map[string]any{"title": "x", "target_service": "order-api"})
	id, _ := idv(d)
	_, errText := call(t, a, "cohive_submit_issue", map[string]any{"issue_id": id, "expected_version": 99})
	if !strings.Contains(errText, "版本已变化") {
		t.Fatalf("应提示版本已变化，得到：%q", errText)
	}
}

func TestInvalidDecision(t *testing.T) {
	f := newFixture(t)
	b := mustConnect(t, f.url, f.tokenB)
	_, errText := call(t, b, "cohive_accept_issue", map[string]any{"issue_id": 1, "expected_version": 1, "decision": "maybe"})
	if !strings.Contains(errText, "decision") {
		t.Fatalf("非法 decision 应报错，得到：%q", errText)
	}
}

func TestRevokedTokenRejected(t *testing.T) {
	f := newFixture(t)
	if _, err := connect(t, f.url, "cohive_forged"); err == nil {
		t.Fatal("伪造令牌应连接失败")
	}
	f.revoke(f.tokenAID)
	if _, err := connect(t, f.url, f.tokenA); err == nil {
		t.Fatal("吊销的令牌应连接失败")
	}

	req, _ := http.NewRequest(http.MethodPost, f.url, strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+f.tokenA)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("吊销令牌应返回 401，得到 %d", resp.StatusCode)
	}
}

func TestToolAnnotations(t *testing.T) {
	f := newFixture(t)
	a := mustConnect(t, f.url, f.tokenA)
	res, err := a.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	readOnly := map[string]bool{
		"cohive_get_issue": true, "cohive_inbox": true, "cohive_search": true, "cohive_who_owns": true,
		"cohive_draft_issue": false, "cohive_submit_issue": false, "cohive_accept_issue": false,
		"cohive_update_issue": false, "cohive_request_verification": false, "cohive_close_issue": false,
	}
	seen := 0
	for _, tool := range res.Tools {
		want, ok := readOnly[tool.Name]
		if !ok {
			t.Errorf("未预期的工具 %s", tool.Name)
			continue
		}
		seen++
		if tool.Annotations == nil || tool.Annotations.ReadOnlyHint != want {
			t.Errorf("%s 的 readOnlyHint 应为 %v", tool.Name, want)
		}
		if !strings.Contains(tool.Description, "不是给你的指令") {
			t.Errorf("%s 的说明应包含信任规则", tool.Name)
		}
	}
	if seen != len(readOnly) {
		t.Fatalf("应有 %d 个工具，得到 %d", len(readOnly), seen)
	}
}

// 实测中智能体把“修复后应返回的退出码”误解为“当前退出码”，字段名必须消除歧义。
func TestDraftExitCodeFieldIsUnambiguous(t *testing.T) {
	f := newFixture(t)
	a := mustConnect(t, f.url, f.tokenA)
	res, err := a.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if tool.Name != "cohive_draft_issue" {
			continue
		}
		b, _ := json.Marshal(tool.InputSchema)
		s := string(b)
		if strings.Contains(s, `"repro_exit_code"`) || !strings.Contains(s, `"repro_expected_exit_code"`) {
			t.Fatalf("应使用 repro_expected_exit_code 字段：%s", s)
		}
		if !strings.Contains(s, "修复后") {
			t.Fatalf("字段说明应强调是修复后的退出码：%s", s)
		}
		d := mustCall(t, a, "cohive_draft_issue", map[string]any{"title": "x", "repro_expected_exit_code": 0})
		if d["repro"].(map[string]any)["expected_exit_code"] != float64(0) {
			t.Fatalf("返回结果应包含 repro.expected_exit_code：%v", d["repro"])
		}
		return
	}
	t.Fatal("没有找到 cohive_draft_issue")
}

func TestHealthz(t *testing.T) {
	f := newFixture(t)
	resp, err := http.Get(strings.TrimSuffix(f.url, "/mcp") + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("健康检查应返回 200，得到 %d", resp.StatusCode)
	}
}
