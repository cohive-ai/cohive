package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/server"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
)

var builtWeb = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>Cohive</title><div id=root></div>")},
	"assets/app.js": {Data: []byte("console.log(1)")},
}

type fixture struct {
	url            string
	svc            *core.Service
	a, b           store.Identity
	tokenA, tokenB string
}

func newFixture(t *testing.T, web fstest.MapFS) fixture {
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
	f := fixture{svc: &core.Service{Pool: pool}}
	mk := func(name, tool string) (store.Identity, string) {
		m, err := store.CreateMember(ctx, pool, ws, name, "")
		if err != nil {
			t.Fatal(err)
		}
		plain, _, err := store.IssueToken(ctx, pool, m.ID, tool)
		if err != nil {
			t.Fatal(err)
		}
		id, err := store.LookupToken(ctx, pool, plain)
		if err != nil {
			t.Fatal(err)
		}
		return id, plain
	}
	f.a, f.tokenA = mk("lee-claude", "claude-code")
	f.b, f.tokenB = mk("lee-codex", "codex")
	if err := store.UpsertService(ctx, pool, ws, "order-api", f.b.MemberID); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(f.svc, web, server.Options{}))
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func get(t *testing.T, url, token string) (int, string, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header.Get("Content-Type"), string(b)
}

func getJSON(t *testing.T, url, token string) map[string]any {
	t.Helper()
	code, _, body := get(t, url, token)
	if code != http.StatusOK {
		t.Fatalf("GET %s 应返回 200，得到 %d：%s", url, code, body)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("解析 %s：%v", url, err)
	}
	return out
}

func TestAPIRequiresToken(t *testing.T) {
	f := newFixture(t, builtWeb)
	if code, _, _ := get(t, f.url+"/api/v1/board", ""); code != http.StatusUnauthorized {
		t.Fatalf("未带令牌应返回 401，得到 %d", code)
	}
	if code, _, _ := get(t, f.url+"/api/v1/board", "cohive_forged"); code != http.StatusUnauthorized {
		t.Fatalf("伪造令牌应返回 401，得到 %d", code)
	}
}

func TestMe(t *testing.T) {
	f := newFixture(t, builtWeb)
	me := getJSON(t, f.url+"/api/v1/me", f.tokenA)
	if me["member"] != "lee-claude" || me["tool"] != "claude-code" {
		t.Fatalf("身份不符：%v", me)
	}
}

func TestBoardAPI(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	d, err := f.svc.Draft(ctx, f.a, core.DraftInput{Title: "下单接口返回 500", TargetService: "order-api"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Act(ctx, f.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version}); err != nil {
		t.Fatal(err)
	}
	board := getJSON(t, f.url+"/api/v1/board", f.tokenB)
	if len(board["members"].([]any)) != 2 {
		t.Fatalf("应有 2 位成员：%v", board["members"])
	}
	issues := board["issues"].([]any)
	if len(issues) != 1 {
		t.Fatalf("应有 1 张单：%v", issues)
	}
	first := issues[0].(map[string]any)
	if first["holder"] != "lee-codex" || first["needs"] == "" || first["title"] != "下单接口返回 500" {
		t.Fatalf("看板条目不符：%v", first)
	}
}

func TestIssueDetailAPI(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	d, err := f.svc.Draft(ctx, f.a, core.DraftInput{Title: "x", TargetService: "order-api"})
	if err != nil {
		t.Fatal(err)
	}
	url := f.url + "/api/v1/issues/" + jsonNumber(d.ID)
	if code, ct, _ := get(t, url, f.tokenB); code != http.StatusNotFound || !strings.Contains(ct, "json") {
		t.Fatalf("别人的草稿应返回 JSON 404，得到 %d %s", code, ct)
	}
	got := getJSON(t, url, f.tokenA)
	if got["issue"].(map[string]any)["status"] != "DRAFT" || len(got["events"].([]any)) != 1 {
		t.Fatalf("详情不符：%v", got)
	}
	if code, _, _ := get(t, f.url+"/api/v1/issues/abc", f.tokenA); code != http.StatusBadRequest {
		t.Fatalf("非法编号应返回 400，得到 %d", code)
	}
}

func TestIssueListAPI(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	for _, title := range []string{"下单接口返回 500", "登录页空白"} {
		d, err := f.svc.Draft(ctx, f.a, core.DraftInput{Title: title, TargetService: "order-api"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.svc.Act(ctx, f.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version}); err != nil {
			t.Fatal(err)
		}
	}
	all := getJSON(t, f.url+"/api/v1/issues", f.tokenB)["items"].([]any)
	if len(all) != 2 {
		t.Fatalf("应有 2 张单，得到 %d", len(all))
	}
	hit := getJSON(t, f.url+"/api/v1/issues?q=500", f.tokenB)["items"].([]any)
	if len(hit) != 1 {
		t.Fatalf("关键词过滤后应有 1 张，得到 %d", len(hit))
	}
	none := getJSON(t, f.url+"/api/v1/issues?status=CLOSED", f.tokenB)["items"].([]any)
	if len(none) != 0 {
		t.Fatalf("按状态过滤后应为空，得到 %d", len(none))
	}
}

func TestUnknownAPIReturnsJSON404(t *testing.T) {
	f := newFixture(t, builtWeb)
	code, ct, body := get(t, f.url+"/api/v1/nope", f.tokenA)
	if code != http.StatusNotFound || !strings.Contains(ct, "json") || strings.Contains(body, "<!doctype") {
		t.Fatalf("未知接口应返回 JSON 404 而不是页面，得到 %d %s %s", code, ct, body)
	}
}

func TestSPAFallback(t *testing.T) {
	f := newFixture(t, builtWeb)
	code, _, body := get(t, f.url+"/issues/3", "")
	if code != http.StatusOK || !strings.Contains(body, `id=root`) {
		t.Fatalf("前端路由应回退到 index.html，得到 %d %s", code, body)
	}
	code, ct, body := get(t, f.url+"/assets/app.js", "")
	if code != http.StatusOK || body != "console.log(1)" || !strings.Contains(ct, "javascript") {
		t.Fatalf("静态资源应原样返回，得到 %d %s %s", code, ct, body)
	}
	if code, _, _ := get(t, f.url+"/assets/missing.js", ""); code != http.StatusNotFound {
		t.Fatalf("缺失的静态资源应返回 404，得到 %d", code)
	}
	if code, _, body := get(t, f.url+"/healthz", ""); code != http.StatusOK || !strings.Contains(body, "ok") {
		t.Fatalf("健康检查应正常，得到 %d", code)
	}
}

func TestBoardNotBuilt(t *testing.T) {
	f := newFixture(t, fstest.MapFS{})
	code, _, body := get(t, f.url+"/", "")
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "make web") {
		t.Fatalf("看板未构建时应提示构建方法，得到 %d %s", code, body)
	}
}

// 经隧道转发来的请求 Host 是公网域名；只有白名单里的域名能访问 MCP，其余仍按 DNS 重绑定防护拒绝。
func TestMCPHostAllowlist(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(&core.Service{Pool: pool}, builtWeb, server.Options{AllowedHosts: []string{"cohive-test.example.com"}}))
	defer srv.Close()

	post := func(host string) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/mcp", strings.NewReader(`{}`))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("evil.example.org"); code != http.StatusForbidden {
		t.Fatalf("不在白名单的 Host 应返回 403，得到 %d", code)
	}
	if code := post("cohive-test.example.com"); code != http.StatusUnauthorized {
		t.Fatalf("白名单里的 Host 应进入令牌校验（未带令牌返回 401），得到 %d", code)
	}
	if code := post("COHIVE-TEST.example.com:443"); code != http.StatusUnauthorized {
		t.Fatalf("Host 比较应忽略大小写和端口，得到 %d", code)
	}
	if code := post("localhost:8787"); code != http.StatusUnauthorized {
		t.Fatalf("localhost 始终允许，得到 %d", code)
	}
}

func postLogin(t *testing.T, url, code, tool string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code, "tool": tool})
	resp, err := http.Post(url+"/api/v1/login", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func TestLoginExchangeAPI(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	uid := int64(42)
	if err := store.SetMemberTelegramUserID(ctx, f.svc.Pool, f.b.MemberID, &uid); err != nil {
		t.Fatal(err)
	}
	res, err := f.svc.IssueLoginCode(ctx, uid, "")
	if err != nil {
		t.Fatal(err)
	}

	if code, out := postLogin(t, f.url, res.Code, "bad tool!"); code != http.StatusBadRequest {
		t.Fatalf("工具名不合法应返回 400：%d %v", code, out)
	}
	code, out := postLogin(t, f.url, res.Code, "codex")
	if code != http.StatusOK || out["member"] != "lee-codex" || out["workspace"] != "default" {
		t.Fatalf("换取应成功：%d %v", code, out)
	}
	token, _ := out["token"].(string)
	if c, _, body := get(t, f.url+"/api/v1/me", token); c != http.StatusOK || !strings.Contains(body, "lee-codex") {
		t.Fatalf("换到的令牌应能直接使用：%d %s", c, body)
	}
	if code, out := postLogin(t, f.url, res.Code, "codex"); code != http.StatusUnauthorized || out["error"] == nil {
		t.Fatalf("登录码重复使用应返回 401 和错误说明：%d %v", code, out)
	}
}

func TestLoginExchangeRateLimited(t *testing.T) {
	f := newFixture(t, builtWeb)
	limited := false
	for i := 0; i < 40; i++ {
		code, _ := postLogin(t, f.url, "AAAA-AAAA", "codex")
		if code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Fatal("连续尝试错误的登录码应被限流")
	}
}

func jsonNumber(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
