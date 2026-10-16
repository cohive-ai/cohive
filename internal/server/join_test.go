package server_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/store"
)

// loginAs 用密码登录一个浏览器。
func loginAs(t *testing.T, f fixture, name, pw string) *browser {
	t.Helper()
	b := newBrowser(t, f.url)
	if code, out := b.do("POST", "/api/v1/auth/login", map[string]string{"name": name, "password": pw}, ""); code != 200 {
		t.Fatalf("%s 登录：%d %v", name, code, out)
	}
	return b
}

// setPassword 给账号设密码。
func setPassword(t *testing.T, f fixture, accountID int64, pw string) {
	t.Helper()
	a, err := store.AccountByID(context.Background(), f.svc.Pool, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svc.ChangePassword(context.Background(), a, "", "", pw); err != nil {
		t.Fatal(err)
	}
}

// 已有账号在网页上接受邀请：个人邀请直接加入，团队邀请生成加入申请；登录与 /me 返回全部工作空间。
func TestAcceptInviteOverHTTP(t *testing.T) {
	f := newFixture(t, builtWeb)
	opsAdmin, _, _ := opsWorkspace(t, f)
	setPassword(t, f, f.a.AccountID, "lee-password-1")

	// 登录不再带工作空间，返回账号与全部工作空间
	b := newBrowser(t, f.url)
	code, out := b.do("POST", "/api/v1/auth/login", map[string]any{"name": "lee-claude", "password": "lee-password-1", "workspace": "x"}, "")
	if code != 200 || accName(out) != "lee-claude" || len(out["workspaces"].([]any)) != 1 {
		t.Fatalf("登录：%d %v", code, out)
	}

	_, out = opsAdmin.do("POST", "/api/v1/w/ops/admin/invites", map[string]any{"kind": "personal"}, "")
	personal := strings.TrimPrefix(out["url"].(string), f.url+"/i/")
	if code, out := b.do("GET", "/api/v1/invites/"+personal, nil, ""); code != 200 || out["signed_in_as"] != "lee-claude" ||
		out["already_member"] != false || out["workspace"] != "ops" {
		t.Fatalf("带会话查看邀请：%d %v", code, out)
	}
	if code, out := newBrowser(t, f.url).do("GET", "/api/v1/invites/"+personal, nil, ""); code != 200 || out["signed_in_as"] != nil {
		t.Fatalf("不带会话时不返回本人信息：%d %v", code, out)
	}
	if code, out := b.do("POST", "/api/v1/invites/"+personal+"/accept", map[string]any{}, ""); code != 200 || out["status"] != "active" {
		t.Fatalf("接受个人邀请：%d %v", code, out)
	}
	if code, out := b.do("GET", "/api/v1/me", nil, ""); code != 200 || len(out["workspaces"].([]any)) != 2 {
		t.Fatalf("应在两个工作空间：%d %v", code, out)
	}
	if code, _ := b.do("GET", "/api/v1/w/ops/board", nil, ""); code != 200 {
		t.Fatalf("加入后能看 ops 的看板：%d", code)
	}

	// 团队邀请：已有账号生成加入申请，待审列表标出已有账号
	_, out = opsAdmin.do("POST", "/api/v1/w/ops/admin/invites", map[string]any{"kind": "team"}, "")
	team := strings.TrimPrefix(out["url"].(string), f.url+"/i/")
	setPassword(t, f, f.b.AccountID, "codex-password-1")
	c := loginAs(t, f, "lee-codex", "codex-password-1")
	if code, out := c.do("POST", "/api/v1/invites/"+team+"/accept", map[string]any{}, ""); code != 200 || out["status"] != "pending" {
		t.Fatalf("团队邀请：%d %v", code, out)
	}
	if code, out := c.do("POST", "/api/v1/invites/"+team+"/accept", map[string]any{}, ""); code != 409 {
		t.Fatalf("重复申请应 409：%d %v", code, out)
	}
	_, pending := opsAdmin.do("GET", "/api/v1/w/ops/admin/pending", nil, "")
	sus := pending["signups"].([]any)
	if len(sus) != 1 || sus[0].(map[string]any)["kind"] != "existing" || sus[0].(map[string]any)["name"] != "lee-codex" {
		t.Fatalf("待审列表：%v", pending)
	}
	id := int64(sus[0].(map[string]any)["id"].(float64))
	if code, _ := opsAdmin.do("POST", "/api/v1/w/ops/admin/signups/"+jsonNumber(id), map[string]bool{"approve": true}, ""); code != 204 {
		t.Fatalf("批准加入：%d", code)
	}
	if code, _ := c.do("GET", "/api/v1/w/ops/board", nil, ""); code != 200 {
		t.Fatalf("批准后能看 ops 的看板：%d", code)
	}
	if code, out := b.do("POST", "/api/v1/invites/"+personal+"/accept", map[string]any{}, ""); code != 404 {
		t.Fatalf("用过的个人邀请应失效：%d %v", code, out)
	}
}
