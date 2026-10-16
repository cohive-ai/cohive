package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// opsWorkspace 在夹具上再建一个工作空间 ops：管理员 ops-admin（有密码），成员 ops-dev 起草了一张单。
func opsWorkspace(t *testing.T, f fixture) (admin *browser, opsToken string, opsIssue int64) {
	t.Helper()
	ctx := context.Background()
	q := f.svc.Pool
	ws, err := store.CreateWorkspace(ctx, q, "ops")
	if err != nil {
		t.Fatal(err)
	}
	a, err := store.CreateAccount(ctx, q, "ops-admin", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddMembership(ctx, q, a.ID, ws.ID, store.RoleAdmin, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ChangePassword(ctx, a, "", "", "ops-password-1"); err != nil {
		t.Fatal(err)
	}
	dev, err := store.CreateMember(ctx, q, ws.ID, "ops-dev", "")
	if err != nil {
		t.Fatal(err)
	}
	opsToken, _, err = store.IssueToken(ctx, q, dev.ID, "codex")
	if err != nil {
		t.Fatal(err)
	}
	who, err := store.LookupToken(ctx, q, opsToken)
	if err != nil {
		t.Fatal(err)
	}
	is, err := f.svc.Draft(ctx, who, core.DraftInput{Title: "ops 的单"})
	if err != nil {
		t.Fatal(err)
	}
	admin = newBrowser(t, f.url)
	if code, out := admin.do("POST", "/api/v1/auth/login", map[string]string{"name": "ops-admin", "password": "ops-password-1"}, ""); code != 200 {
		t.Fatalf("ops-admin 登录：%d %v", code, out)
	}
	return admin, opsToken, is.ID
}

// 6.2 第 4 条：工作空间隔离。别的工作空间的管理员对本空间的每个管理接口都 404；
// 别的工作空间的令牌访问本空间 404；按全局编号访问别的工作空间的问题单 404。
func TestWorkspaceIsolation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	opsAdmin, opsToken, opsIssue := opsWorkspace(t, f)
	for _, rt := range adminRoutes {
		if !strings.HasPrefix(rt[1], "/api/v1/w/default/") {
			continue
		}
		if code, out := opsAdmin.do(rt[0], rt[1], map[string]any{}, ""); code != 404 {
			t.Errorf("ops 的管理员调 %s %s 应 404：%d %v", rt[0], rt[1], code, out)
		}
	}
	// 反过来：default 的管理员看不到 ops
	if err := store.SetMemberRole(ctx, f.svc.Pool, f.b.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	acc, _ := store.AccountByID(ctx, f.svc.Pool, f.b.AccountID)
	if err := f.svc.ChangePassword(ctx, acc, "", "", "admin-password-1"); err != nil {
		t.Fatal(err)
	}
	defAdmin := newBrowser(t, f.url)
	if code, _ := defAdmin.do("POST", "/api/v1/auth/login", map[string]string{"name": "lee-codex", "password": "admin-password-1"}, ""); code != 200 {
		t.Fatal("default 管理员登录失败")
	}
	for _, p := range []string{"/api/v1/w/ops/admin/members", "/api/v1/w/ops/admin/pending", "/api/v1/w/ops/board", "/api/v1/w/nope/board"} {
		if code, out := defAdmin.do("GET", p, nil, ""); code != 404 || out["error"] != "工作空间不存在或你不是它的成员" {
			t.Errorf("GET %s 应统一 404：%d %v", p, code, out)
		}
	}
	// 令牌只能访问自己所属的工作空间
	tok := newBrowser(t, f.url)
	tok.origin = ""
	if code, _ := tok.do("GET", "/api/v1/w/default/board", nil, opsToken); code != 404 {
		t.Errorf("ops 的令牌访问 default 应 404：%d", code)
	}
	if code, _ := tok.do("GET", "/api/v1/w/ops/board", nil, opsToken); code != 200 {
		t.Errorf("ops 的令牌访问 ops 应 200：%d", code)
	}
	// 按全局编号访问别的工作空间的问题单
	if code, _ := tok.do("GET", "/api/v1/w/default/issues/"+jsonNumber(opsIssue), nil, f.tokenA); code != 404 {
		t.Errorf("default 的令牌按编号看 ops 的单应 404：%d", code)
	}
	if code, _ := defAdmin.do("GET", "/api/v1/w/default/issues/"+jsonNumber(opsIssue), nil, ""); code != 404 {
		t.Errorf("default 的会话按编号看 ops 的单应 404：%d", code)
	}
	// 服务器管理员不是成员：管理页能进，看板与问题单 404
	if err := store.SetServerAdmin(ctx, f.svc.Pool, f.b.AccountID, true); err != nil {
		t.Fatal(err)
	}
	if code, out := defAdmin.do("GET", "/api/v1/w/ops/admin/members", nil, ""); code != 200 {
		t.Errorf("服务器管理员可以进任一工作空间的管理页：%d %v", code, out)
	}
	if code, _ := defAdmin.do("GET", "/api/v1/w/ops/board", nil, ""); code != 404 {
		t.Errorf("服务器管理员不是成员时看不到看板：%d", code)
	}
}

// 6.2 第 2 条：设备密钥只能用于签发令牌、列工作空间、免密登录与吊销本人设备；
// 在 MCP、问题单、管理、个人接口上一律 401；成员令牌也不能当设备密钥用。
func TestDeviceSecretBoundary(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	acc, _ := store.AccountByID(ctx, f.svc.Pool, f.a.AccountID)
	st, err := f.svc.StartDeviceAuth(ctx, []string{"codex"}, "mac")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.AuthorizeDevice(ctx, acc, st.Nonce, true); err != nil {
		t.Fatal(err)
	}
	p, err := f.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret)
	if err != nil || p.DeviceSecret == "" {
		t.Fatalf("领取：%+v %v", p, err)
	}
	b := newBrowser(t, f.url)
	b.origin = ""
	for _, rt := range [][2]string{
		{"GET", "/api/v1/w/default/board"}, {"GET", "/api/v1/w/default/issues"}, {"GET", "/api/v1/me"},
		{"GET", "/api/v1/me/devices"}, {"POST", "/api/v1/auth/password"}, {"GET", "/api/v1/w/default/admin/members"},
		{"GET", "/api/v1/server/accounts"}, {"POST", "/api/v1/invites/x/accept"}, {"GET", "/api/v1/device/auth/da_x"},
	} {
		if code, out := b.do(rt[0], rt[1], map[string]any{}, p.DeviceSecret); code != 401 {
			t.Errorf("设备密钥调 %s %s 应 401：%d %v", rt[0], rt[1], code, out)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, f.url+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Authorization", "Bearer "+p.DeviceSecret)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Errorf("设备密钥调 MCP 应 401：%d", resp.StatusCode)
	}
	// 允许的接口
	if code, out := b.do("GET", "/api/v1/device/workspaces", nil, p.DeviceSecret); code != 200 || len(out["items"].([]any)) != 1 {
		t.Errorf("设备密钥列工作空间：%d %v", code, out)
	}
	code, out := b.do("POST", "/api/v1/device/tokens", map[string]any{"workspace": "default", "tools": []string{"claude-code"}}, p.DeviceSecret)
	if code != 200 || out["tokens"].(map[string]any)["claude-code"] == "" {
		t.Errorf("设备密钥签发令牌：%d %v", code, out)
	}
	if code, out := b.do("POST", "/api/v1/device/tokens", map[string]any{"workspace": "nope", "tools": []string{"codex"}}, p.DeviceSecret); code != 404 {
		t.Errorf("不是成员的工作空间应 404：%d %v", code, out)
	}
	if code, _ := b.do("POST", "/api/v1/auth/web-login", map[string]any{}, p.DeviceSecret); code != 200 {
		t.Errorf("设备密钥免密登录：%d", code)
	}
	// 成员令牌不能当设备密钥用
	if code, out := b.do("POST", "/api/v1/device/tokens", map[string]any{"workspace": "default", "tools": []string{"codex"}}, f.tokenA); code != 401 {
		t.Errorf("成员令牌调 device/tokens 应 401：%d %v", code, out)
	}
	if code, _ := b.do("GET", "/api/v1/device/workspaces", nil, f.tokenA); code != 401 {
		t.Errorf("成员令牌调 device/workspaces 应 401：%d", code)
	}
	// 伪造的设备密钥
	if code, _ := b.do("GET", "/api/v1/device/workspaces", nil, core.DeviceSecretPrefix+"forged"); code != 401 {
		t.Errorf("伪造的设备密钥应 401：%d", code)
	}
}
