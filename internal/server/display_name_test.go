package server_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// 本人改昵称的接口：密码登录与 cohive web 免密登录的网页会话都可以，编程工具令牌一律拒绝且不改动；
// 撞名返回 409 与稳定错误码 display_name_taken，并说明撞的是登录名还是昵称。
func TestMyDisplayNameEndpoint(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	setPassword(t, f, f.a.AccountID, "member-password-1")
	pw := loginAs(t, f, "lee-claude", "member-password-1")
	path := "/api/v1/me/display-name"
	display := func() string {
		a, _ := store.AccountByID(ctx, f.svc.Pool, f.a.AccountID)
		return a.DisplayName
	}

	if code, out := pw.do("POST", path, map[string]any{"display_name": "  Claude  "}, ""); code != 200 || out["display_name"] != "Claude" {
		t.Fatalf("密码会话改昵称：%d %v", code, out)
	}
	if code, out := pw.do("GET", "/api/v1/me", nil, ""); code != 200 || out["account"].(map[string]any)["display_name"] != "Claude" {
		t.Fatalf("/me 应返回新昵称：%d %v", code, out)
	}

	// 编程工具令牌：拒绝，不改动，不记录
	_, before := lastEvent(t, f.svc)
	tok := newBrowser(t, f.url)
	tok.origin = ""
	if code, out := tok.do("POST", path, map[string]any{"display_name": "Agent Set"}, f.tokenA); code != 403 || out["code"] != "web_only" {
		t.Fatalf("令牌应被拒：%d %v", code, out)
	}
	if _, after := lastEvent(t, f.svc); display() != "Claude" || after != before {
		t.Fatalf("令牌被拒后不应改动或记录：%q %d→%d", display(), before, after)
	}

	// 撞别人的登录名（大小写不同）：409，只说类别
	code, out := pw.do("POST", path, map[string]any{"display_name": "LEE-CODEX"}, "")
	if code != 409 || out["code"] != core.CodeDisplayNameTaken || out["clash"] != store.ClashName || out["error"] == "" {
		t.Fatalf("撞登录名：%d %v", code, out)
	}
	if err := store.SetAccountDisplayName(ctx, f.svc.Pool, f.b.AccountID, "Codex"); err != nil {
		t.Fatal(err)
	}
	if code, out := pw.do("POST", path, map[string]any{"display_name": "codex"}, ""); code != 409 || out["clash"] != store.ClashDisplayName {
		t.Fatalf("撞昵称：%d %v", code, out)
	}
	if display() != "Claude" {
		t.Fatalf("被拒后不应改动：%q", display())
	}

	// cohive web 免密会话也可以；清空
	once, err := f.svc.CreateWebLoginCode(ctx, f.a.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	dev := newBrowser(t, f.url)
	resp, err := dev.client.Get(f.url + "/auth/once/" + once)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if code, out := dev.do("POST", path, map[string]any{"display_name": ""}, ""); code != 200 || out["display_name"] != "" {
		t.Fatalf("免密会话清空昵称：%d %v", code, out)
	}
	e, _ := lastEvent(t, f.svc)
	if display() != "" || e.Action != core.ActAccountUpdate || e.Via != core.ViaWeb || e.ActorName != "lee-claude" {
		t.Fatalf("清空应保存并记录：%q %+v", display(), e)
	}
}
