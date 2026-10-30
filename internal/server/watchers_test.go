package server_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

func watcherNames(out map[string]any) []string {
	var names []string
	for _, w := range out["watchers"].([]any) {
		names = append(names, w.(map[string]any)["name"].(string))
	}
	return names
}

// 关注者的网页接口（CC-1）：详情给出关注者与按钮权限；关注、取消、抄送别人；
// 非发起人的普通成员不能移除别人，免密会话的管理员也不行；归档后拒绝。
func TestWebWatchers(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	if _, err := store.CreateMember(ctx, f.svc.Pool, f.a.WorkspaceID, "carol", ""); err != nil {
		t.Fatal(err)
	}
	setPassword(t, f, f.b.AccountID, "admin-password-1")
	setPassword(t, f, f.a.AccountID, "member-password-1")
	owner := loginAs(t, f, "lee-codex", "admin-password-1") // 负责人，此时还不是管理员
	source := loginAs(t, f, "lee-claude", "member-password-1")

	d, err := f.svc.Draft(ctx, f.a, core.DraftInput{Title: "下单接口 500", Owner: "lee-codex", Actual: "x"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/v1/w/default/issues/%d", d.ID)
	wpath := path + "/watchers"

	// 草稿只有发起人看得见，也能抄送
	if code, out := source.do("GET", path, nil, ""); code != 200 || out["can_watch"] != true || out["can_add_watchers"] != true ||
		out["can_remove_watchers"] != true || out["watching"] != false || len(out["watchers"].([]any)) != 0 {
		t.Fatalf("草稿详情：%d %v", code, out)
	}
	if code, out := source.do("POST", wpath, map[string]any{"watch": true, "members": []string{"carol"}}, ""); code != 200 || fmt.Sprint(watcherNames(out)) != "[carol]" {
		t.Fatalf("草稿抄送：%d %v", code, out)
	}
	is, err := f.svc.Act(ctx, f.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version})
	if err != nil {
		t.Fatal(err)
	}

	// 负责人（非发起人、非管理员）：能抄送、能关注自己，不能移除别人
	if _, out := owner.do("GET", path, nil, ""); out["can_add_watchers"] != true || out["can_remove_watchers"] != false || out["can_watch"] != true {
		t.Fatalf("负责人的权限：%v", out)
	}
	if code, out := owner.do("POST", wpath, map[string]any{"watch": true}, ""); code != 200 || fmt.Sprint(watcherNames(out)) != "[carol lee-codex]" {
		t.Fatalf("自行关注：%d %v", code, out)
	}
	if _, out := owner.do("GET", path, nil, ""); out["watching"] != true {
		t.Fatalf("watching：%v", out)
	}
	if code, out := owner.do("POST", wpath, map[string]any{"watch": false, "members": []string{"carol"}}, ""); code != 400 || out["code"] != "invalid_input" {
		t.Fatalf("非发起人移除别人应 400：%d %v", code, out)
	}
	if code, out := owner.do("POST", wpath, map[string]any{"watch": false}, ""); code != 200 || fmt.Sprint(watcherNames(out)) != "[carol]" {
		t.Fatalf("取消关注：%d %v", code, out)
	}
	if code, out := owner.do("POST", wpath, map[string]any{"watch": true, "members": []string{"nobody"}}, ""); code != 400 {
		t.Fatalf("不存在的成员：%d %v", code, out)
	}

	// 管理员：账号密码会话能移除别人，免密会话不能
	if err := store.SetMemberRole(ctx, f.svc.Pool, f.b.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, out := owner.do("GET", path, nil, ""); out["can_remove_watchers"] != true {
		t.Fatalf("管理员（密码会话）能移除：%v", out)
	}
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
	if _, out := dev.do("GET", path, nil, ""); out["can_remove_watchers"] != false {
		t.Fatalf("免密会话不显示移除：%v", out)
	}
	if code, out := dev.do("POST", wpath, map[string]any{"watch": false, "members": []string{"carol"}}, ""); code != 400 {
		t.Fatalf("免密会话不能按管理员移除：%d %v", code, out)
	}
	if code, out := owner.do("POST", wpath, map[string]any{"watch": false, "members": []string{"carol"}}, ""); code != 200 || len(watcherNames(out)) != 0 {
		t.Fatalf("管理员移除：%d %v", code, out)
	}

	// 令牌不能调
	tok := newBrowser(t, f.url)
	tok.origin = ""
	if code, _ := tok.do("POST", wpath, map[string]any{"watch": true}, f.tokenB); code != 403 {
		t.Fatalf("令牌不能调网页接口：%d", code)
	}

	// 归档后：详情不给操作，写入被拒
	if _, err := f.svc.ArchiveWorkspace(ctx, core.WebActor(store.Account{ID: f.b.AccountID, Name: "lee-codex"}), f.b.WorkspaceID, 1); err != nil {
		t.Fatalf("归档：%v (is=%d)", err, is.ID)
	}
	if _, out := source.do("GET", path, nil, ""); out["can_watch"] != false || out["can_add_watchers"] != false || out["can_remove_watchers"] != false {
		t.Fatalf("归档后不给操作：%v", out)
	}
	if code, out := source.do("POST", wpath, map[string]any{"watch": true}, ""); code != 409 || out["code"] != "workspace_archived" {
		t.Fatalf("归档后应 409：%d %v", code, out)
	}
}
