package server_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/store"
)

// 全部管理接口（方法 + 路径）：工作空间管理与服务器管理。
var adminRoutes = [][2]string{
	{"GET", "/api/v1/w/default/admin/pending"},
	{"POST", "/api/v1/w/default/admin/signups/1"},
	{"POST", "/api/v1/w/default/admin/device-auths/1"},
	{"GET", "/api/v1/w/default/admin/members"},
	{"GET", "/api/v1/w/default/admin/accounts/lee-claude"},
	{"POST", "/api/v1/w/default/admin/members"},
	{"POST", "/api/v1/w/default/admin/members/1"},
	{"GET", "/api/v1/w/default/admin/members/1/handover"},
	{"POST", "/api/v1/w/default/admin/members/1/handover"},
	{"POST", "/api/v1/w/default/admin/members/1/remove"},
	{"GET", "/api/v1/w/default/admin/devices"},
	{"POST", "/api/v1/w/default/admin/devices/1/revoke"},
	{"GET", "/api/v1/w/default/admin/invites"},
	{"POST", "/api/v1/w/default/admin/invites"},
	{"POST", "/api/v1/w/default/admin/invites/1/revoke"},
	{"GET", "/api/v1/w/default/admin/workspace"},
	{"POST", "/api/v1/w/default/admin/workspace"},
	{"GET", "/api/v1/server/workspaces"},
	{"POST", "/api/v1/server/workspaces"},
	{"DELETE", "/api/v1/server/workspaces/x"},
	{"GET", "/api/v1/server/accounts"},
	{"POST", "/api/v1/server/accounts/lee-claude"},
	{"POST", "/api/v1/server/accounts/lee-claude/disable"},
	{"POST", "/api/v1/server/accounts/lee-claude/enable"},
	{"DELETE", "/api/v1/server/accounts/lee-claude/telegram"},
	{"GET", "/api/v1/server/pending-devices"},
	{"POST", "/api/v1/server/device-auths/1"},
}

// trustRoutes 是会改变本人归属或信任关系的接口：也只认密码登录的网页会话（设计稿第 5 节第 2 条）。
var trustRoutes = [][2]string{
	{"POST", "/api/v1/invites/nope/accept"},
	{"GET", "/api/v1/device/auth/da_x"},
	{"POST", "/api/v1/device/auth/da_x"},
}

// C1：管理接口只接受账号密码登录的网页会话；管理员的令牌、cohive web 免密会话一律 403。
func TestAdminRequiresPasswordSession(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	if err := store.SetMemberRole(ctx, f.svc.Pool, f.b.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := store.SetServerAdmin(ctx, f.svc.Pool, f.b.AccountID, true); err != nil {
		t.Fatal(err)
	}
	m, _ := store.AccountByID(ctx, f.svc.Pool, f.b.AccountID)
	if err := f.svc.ChangePassword(ctx, m, "", "", "admin-password-1"); err != nil {
		t.Fatal(err)
	}

	// 管理员的编程工具令牌
	tok := newBrowser(t, f.url)
	tok.origin = ""
	for _, rt := range append(adminRoutes, trustRoutes...) {
		code, out := tok.do(rt[0], rt[1], map[string]any{}, f.tokenB)
		if code != 403 || out["need_password_login"] != true {
			t.Errorf("令牌调 %s %s 应被拒：%d %v", rt[0], rt[1], code, out)
		}
	}

	// cohive web 免密会话
	once, err := f.svc.CreateWebLoginCode(ctx, f.b.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	dev := newBrowser(t, f.url)
	resp, err := dev.client.Get(f.url + "/auth/once/" + once)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if code, out := dev.do("GET", "/api/v1/me", nil, ""); code != 200 || out["login_method"] != "device" {
		t.Fatalf("免密会话应已登录：%d %v", code, out)
	}
	for _, rt := range append(adminRoutes, trustRoutes...) {
		code, out := dev.do(rt[0], rt[1], map[string]any{}, "")
		if code != 403 || out["need_password_login"] != true {
			t.Errorf("免密会话调 %s %s 应被拒：%d %v", rt[0], rt[1], code, out)
		}
	}

	// 账号密码登录的会话可以
	pw := newBrowser(t, f.url)
	if code, out := pw.do("POST", "/api/v1/auth/login", map[string]string{"name": "lee-codex", "password": "admin-password-1"}, ""); code != 200 {
		t.Fatalf("密码登录：%d %v", code, out)
	}
	if code, out := pw.do("GET", "/api/v1/me", nil, ""); code != 200 || out["login_method"] != "password" {
		t.Fatalf("应为密码会话：%d %v", code, out)
	}
	if code, out := pw.do("GET", "/api/v1/w/default/admin/members", nil, ""); code != 200 {
		t.Fatalf("密码会话可以访问管理接口：%d %v", code, out)
	}
	if code, out := pw.do("GET", "/api/v1/server/accounts", nil, ""); code != 200 {
		t.Fatalf("密码会话可以访问服务器管理接口：%d %v", code, out)
	}
	if code, out := tok.do("GET", "/api/v1/me", nil, f.tokenB); code != 200 || out["login_method"] != "token" {
		t.Fatalf("令牌请求的登录方式：%d %v", code, out)
	}
}
