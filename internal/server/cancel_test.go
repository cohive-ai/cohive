package server_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// 作废与删除草稿的网页接口（#12）：详情给出能否作废、删除；列表默认不含已作废；
// 免密会话只能按发起人身份作废，管理员作废要账号密码登录；令牌不能调这两个接口（编程工具走 MCP）。
func TestWebCancelAndDelete(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	if err := store.SetMemberRole(ctx, f.svc.Pool, f.b.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	setPassword(t, f, f.b.AccountID, "admin-password-1")
	setPassword(t, f, f.a.AccountID, "member-password-1")
	admin := loginAs(t, f, "lee-codex", "admin-password-1")
	member := loginAs(t, f, "lee-claude", "member-password-1")

	// a 发给 b、b 已接单的单；a 的草稿
	d, err := f.svc.Draft(ctx, f.a, core.DraftInput{Title: "下单接口 500", Owner: "lee-codex", Actual: "x"})
	if err != nil {
		t.Fatal(err)
	}
	is, err := f.svc.Act(ctx, f.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version})
	if err == nil {
		is, err = f.svc.Act(ctx, f.b, core.ActInput{IssueID: d.ID, Action: issue.ActAccept, ExpectedVersion: is.Version})
	}
	if err != nil {
		t.Fatal(err)
	}
	draft, err := f.svc.Draft(ctx, f.a, core.DraftInput{Title: "草稿", Actual: "x"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/w/default/issues/%d", is.ID)

	if _, out := member.do("GET", path, nil, ""); out["can_cancel"] != false || out["can_delete"] != false {
		t.Fatalf("已接单后发起人不能作废：%v", out)
	}
	if _, out := admin.do("GET", path, nil, ""); out["can_cancel"] != true {
		t.Fatalf("管理员（密码会话）能作废：%v", out)
	}
	if _, out := member.do("GET", fmt.Sprintf("/api/v1/w/default/issues/%d", draft.ID), nil, ""); out["can_delete"] != true || out["can_cancel"] != false {
		t.Fatalf("自己的草稿能删、不能作废：%v", out)
	}
	if code, _ := member.do("POST", path+"/cancel", map[string]any{"expected_version": is.Version, "reason": "不要了"}, ""); code != 409 {
		t.Fatalf("发起人作废已接单的单应 409：%d", code)
	}
	tok := newBrowser(t, f.url)
	tok.origin = ""
	if code, _ := tok.do("POST", path+"/cancel", map[string]any{"expected_version": is.Version, "reason": "x"}, f.tokenB); code != 403 {
		t.Fatalf("令牌不能调网页作废接口：%d", code)
	}
	// 管理员的免密会话：不走管理员作废
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
	if _, out := dev.do("GET", path, nil, ""); out["can_cancel"] != false {
		t.Fatalf("免密会话不显示管理员作废：%v", out)
	}
	if code, _ := dev.do("POST", path+"/cancel", map[string]any{"expected_version": is.Version, "reason": "x"}, ""); code != 409 {
		t.Fatalf("免密会话不能走管理员作废：%d", code)
	}
	code, out := admin.do("POST", path+"/cancel", map[string]any{"expected_version": is.Version, "reason": "需求取消"}, "")
	if code != 200 || out["status"] != "CANCELLED" {
		t.Fatalf("管理员作废：%d %v", code, out)
	}

	// 列表：默认不含已作废，筛选“已作废”时列出
	ids := func(q string) []any {
		_, out := member.do("GET", "/api/v1/w/default/issues"+q, nil, "")
		var got []any
		for _, it := range out["items"].([]any) {
			got = append(got, it.(map[string]any)["id"])
		}
		return got
	}
	for _, id := range ids("") {
		if id == float64(is.ID) {
			t.Fatalf("默认列表不含已作废：%v", ids(""))
		}
	}
	if got := ids("?status=CANCELLED"); len(got) != 1 || got[0] != float64(is.ID) {
		t.Fatalf("筛选已作废：%v", got)
	}

	// 删除草稿：别人删不了，本人删了就查不到
	dpath := fmt.Sprintf("/api/v1/w/default/issues/%d", draft.ID)
	if code, _ := admin.do("DELETE", dpath, nil, ""); code != 404 {
		t.Fatalf("别人的草稿看不见、删不了：%d", code)
	}
	if code, _ := member.do("DELETE", path, nil, ""); code != 409 {
		t.Fatalf("已发出的单不能删：%d", code)
	}
	if code, _ := member.do("DELETE", dpath, nil, ""); code != 204 {
		t.Fatalf("删除草稿：%d", code)
	}
	if code, _ := member.do("GET", dpath, nil, ""); code != 404 {
		t.Fatalf("删掉后查不到：%d", code)
	}
}
