package server_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// 移出与转交、服务器管理（建工作空间、加人、停用）的接口走一遍。
func TestManageOverHTTP(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	q := f.svc.Pool
	// lee-codex 是 default 的管理员兼服务器管理员；lee-claude 名下有一张单、一个服务
	if err := store.SetMemberRole(ctx, q, f.b.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := store.SetServerAdmin(ctx, q, f.b.AccountID, true); err != nil {
		t.Fatal(err)
	}
	setPassword(t, f, f.b.AccountID, "admin-password-1")
	admin := loginAs(t, f, "lee-codex", "admin-password-1")
	d, err := f.svc.Draft(ctx, f.b, core.DraftInput{Title: "给 claude 的单", Owner: "lee-claude"})
	if err != nil {
		t.Fatal(err)
	}
	is, err := f.svc.Act(ctx, f.b, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertService(ctx, q, f.a.WorkspaceID, "web", f.a.MemberID); err != nil {
		t.Fatal(err)
	}

	// 服务器管理：建工作空间并指定管理员、列账号
	if code, out := admin.do("POST", "/api/v1/server/workspaces", map[string]any{"name": "ops", "admin": "lee-claude"}, ""); code != 201 {
		t.Fatalf("建工作空间：%d %v", code, out)
	}
	if code, out := admin.do("POST", "/api/v1/server/workspaces", map[string]any{"name": "ops"}, ""); code != 409 {
		t.Fatalf("重名应 409：%d %v", code, out)
	}
	_, out := admin.do("GET", "/api/v1/server/workspaces", nil, "")
	if items := out["items"].([]any); len(items) != 2 {
		t.Fatalf("应有两个工作空间：%v", out)
	}
	_, out = admin.do("GET", "/api/v1/server/accounts?q=lee-cl", nil, "")
	if items := out["items"].([]any); len(items) != 1 || len(items[0].(map[string]any)["workspaces"].([]any)) != 2 {
		t.Fatalf("按前缀列账号，lee-claude 在两个工作空间：%v", out)
	}

	// 工作空间管理：查账号、直接加入
	if code, out := admin.do("GET", "/api/v1/w/default/admin/accounts/nobody", nil, ""); code != 404 {
		t.Fatalf("查不到的账号 404：%d %v", code, out)
	}
	if code, out := admin.do("POST", "/api/v1/w/default/admin/members", map[string]any{"account": "lee-claude"}, ""); code != 409 {
		t.Fatalf("已是成员应 409：%d %v", code, out)
	}

	// 移出：先取清单，不完整时 409 并附最新清单，完整后 204
	path := "/api/v1/w/default/admin/members/" + jsonNumber(f.a.MemberID)
	code, h := admin.do("GET", path+"/handover", nil, "")
	if code != 200 || len(h["issues"].([]any)) != 1 || len(h["services"].([]any)) != 1 {
		t.Fatalf("转交清单：%d %v", code, h)
	}
	code, out = admin.do("POST", path+"/remove", map[string]any{"issues": []any{}, "services": []any{}}, "")
	if code != 409 || out["handover"] == nil {
		t.Fatalf("不完整应 409 并附清单：%d %v", code, out)
	}
	plan := map[string]any{
		"issues":   []any{map[string]any{"id": is.ID, "as": "owner", "to_member_id": f.b.MemberID, "expected_version": is.Version}},
		"services": []any{map[string]any{"name": "web", "to_member_id": f.b.MemberID}},
	}
	if code, out := admin.do("POST", path+"/remove", plan, ""); code != 204 {
		t.Fatalf("移出：%d %v", code, out)
	}
	if code, _ := newBrowser(t, f.url).do("GET", "/api/v1/w/default/board", nil, f.tokenA); code != 401 {
		t.Fatalf("移出后令牌失效：%d", code)
	}
	_, out = admin.do("GET", "/api/v1/w/default/admin/members?status=removed", nil, "")
	if items := out["items"].([]any); len(items) != 1 || items[0].(map[string]any)["account"] != "lee-claude" {
		t.Fatalf("已移出列表：%v", out)
	}
	// 加回：复用原成员资格
	code, out = admin.do("POST", "/api/v1/w/default/admin/members", map[string]any{"account": "lee-claude"}, "")
	if code != 201 || int64(out["member_id"].(float64)) != f.a.MemberID {
		t.Fatalf("加回应复用原成员资格：%d %v", code, out)
	}

	// 停用与启用
	if code, out := admin.do("POST", "/api/v1/server/accounts/lee-codex/disable", map[string]any{}, ""); code != 400 {
		t.Fatalf("不能停用自己：%d %v", code, out)
	}
	if code, out := admin.do("POST", "/api/v1/server/accounts/lee-claude/disable", map[string]any{}, ""); code != 204 {
		t.Fatalf("停用：%d %v", code, out)
	}
	if code, out := admin.do("POST", "/api/v1/server/accounts/lee-claude/disable", map[string]any{}, ""); code != 409 {
		t.Fatalf("重复停用 409：%d %v", code, out)
	}
	_, out = admin.do("GET", "/api/v1/w/default/admin/members", nil, "")
	for _, it := range out["items"].([]any) {
		m := it.(map[string]any)
		if m["account"] == "lee-claude" && m["disabled"] != true {
			t.Fatalf("成员列表应标出停用：%v", m)
		}
	}
	if code, _ := admin.do("POST", "/api/v1/server/accounts/lee-claude/enable", map[string]any{}, ""); code != 204 {
		t.Fatal("启用")
	}
	// 服务器管理员开关：至少保留一名
	if code, out := admin.do("POST", "/api/v1/server/accounts/lee-codex", map[string]any{"server_admin": false}, ""); code != 400 {
		t.Fatalf("不能撤销最后一名服务器管理员：%d %v", code, out)
	}
	// 删除工作空间：有问题单的不行，空的可以
	if code, _ := admin.do("DELETE", "/api/v1/server/workspaces/default", nil, ""); code != 409 {
		t.Fatalf("有问题单的不能删：%d", code)
	}
	if code, _ := admin.do("DELETE", "/api/v1/server/workspaces/ops", nil, ""); code != 204 {
		t.Fatalf("删除空工作空间：%d", code)
	}
}
