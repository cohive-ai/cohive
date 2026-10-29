package server_test

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/httpapi"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/server"
	"github.com/cohive-ai/cohive/internal/store"
)

// 工作空间归档与彻底删除的网页接口（WS-1）：带确认数归档、归档后只读、群里告知、
// 彻底删除只认服务器管理员并要求名字一致、有未结束的单子拒绝、管理记录保留。
func TestArchiveAndPurgeOverHTTP(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	ann := &fakeAnnouncer{}
	srv := httptest.NewServer(server.New(f.svc, builtWeb, server.Options{API: httpapi.Options{Announcer: ann}}))
	defer srv.Close()
	f.url = srv.URL
	q := f.svc.Pool
	if err := store.SetMemberRole(ctx, q, f.b.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := store.SetServerAdmin(ctx, q, f.b.AccountID, true); err != nil {
		t.Fatal(err)
	}
	setPassword(t, f, f.b.AccountID, "admin-password-1")
	setPassword(t, f, f.a.AccountID, "member-password-1")
	admin := loginAs(t, f, "lee-codex", "admin-password-1")
	member := loginAs(t, f, "lee-claude", "member-password-1")

	d, err := f.svc.Draft(ctx, f.a, core.DraftInput{Title: "下单接口 500", Owner: "lee-codex", Actual: "x"})
	if err != nil {
		t.Fatal(err)
	}
	is, err := f.svc.Act(ctx, f.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version})
	if err != nil {
		t.Fatal(err)
	}
	draft, err := f.svc.Draft(ctx, f.a, core.DraftInput{Title: "草稿", Actual: "x"})
	if err != nil {
		t.Fatal(err)
	}
	wsID := f.a.WorkspaceID
	const adm = "/api/v1/w/default/admin"

	// 管理页状态与归档
	_, out := admin.do("GET", adm+"/workspace", nil, "")
	if out["archived"] != false || out["open_issues"] != float64(1) {
		t.Fatalf("管理页应给出未归档与未结束数 1：%v", out)
	}
	if code, out := member.do("POST", adm+"/archive", map[string]any{"confirm_open": 1}, ""); code != 403 {
		t.Fatalf("普通成员不能归档：%d %v", code, out)
	}
	code, out := admin.do("POST", adm+"/archive", map[string]any{"confirm_open": 0}, "")
	if code != 409 || out["code"] != "open_issues_unconfirmed" || out["open_issues"] != float64(1) {
		t.Fatalf("确认数不符应 409 并附实际数量：%d %v", code, out)
	}
	if code, out := admin.do("POST", adm+"/archive", map[string]any{"confirm_open": 1}, ""); code != 200 || out["archived"] != true || out["archived_at"] == nil {
		t.Fatalf("归档：%d %v", code, out)
	}
	if code, out := admin.do("POST", adm+"/archive", map[string]any{"confirm_open": 1}, ""); code != 409 || out["code"] != "conflict" {
		t.Fatalf("重复归档应 409 conflict：%d %v", code, out)
	}
	waitAnnounce(t, ann, fmt.Sprintf("archive:%d:lee-codex", wsID))

	// 状态：管理页、/me、服务器列表
	_, out = admin.do("GET", adm+"/workspace", nil, "")
	if out["archived"] != true || out["archived_at"] == nil {
		t.Fatalf("管理页应显示已归档：%v", out)
	}
	_, out = member.do("GET", "/api/v1/me", nil, "")
	if w := out["workspaces"].([]any)[0].(map[string]any); w["archived"] != true {
		t.Fatalf("/me 的工作空间应带 archived：%v", out["workspaces"])
	}
	_, out = admin.do("GET", "/api/v1/server/workspaces", nil, "")
	row := out["items"].([]any)[0].(map[string]any)
	if row["archived"] != true || row["archived_at"] == nil || row["issues"] != float64(2) || row["unfinished_issues"] != float64(1) {
		t.Fatalf("服务器列表应带归档与单数：%v", row)
	}

	// 写被拒（409 workspace_archived），读照常（令牌、会话都能读）
	path := fmt.Sprintf("/api/v1/w/default/issues/%d", is.ID)
	code, out = admin.do("POST", path+"/cancel", map[string]any{"expected_version": is.Version, "reason": "x"}, "")
	if code != 409 || out["code"] != "workspace_archived" {
		t.Fatalf("归档后作废应 409 workspace_archived：%d %v", code, out)
	}
	code, out = member.do("DELETE", fmt.Sprintf("/api/v1/w/default/issues/%d", draft.ID), nil, "")
	if code != 409 || out["code"] != "workspace_archived" {
		t.Fatalf("归档后删草稿应 409：%d %v", code, out)
	}
	code, out = admin.do("POST", adm+"/invites", map[string]any{"kind": "personal"}, "")
	if code != 409 || out["code"] != "workspace_archived" {
		t.Fatalf("归档后建邀请应 409：%d %v", code, out)
	}
	code, out = admin.do("POST", adm+"/workspace", map[string]any{"show_title": true}, "")
	if code != 409 || out["code"] != "workspace_archived" {
		t.Fatalf("归档后改设置应 409：%d %v", code, out)
	}
	if _, err := f.svc.Draft(ctx, f.a, core.DraftInput{Title: "新单"}); err == nil {
		t.Fatal("归档后起草应被拒")
	} else {
		var ae *core.ArchivedError
		if !errors.As(err, &ae) {
			t.Fatalf("应为 ArchivedError：%v", err)
		}
	}
	if code, _, _ := get(t, f.url+"/api/v1/w/default/issues/"+fmt.Sprint(is.ID), f.tokenA); code != 200 {
		t.Fatalf("凭令牌仍能读单子：%d", code)
	}
	if code, _, _ := get(t, f.url+"/api/v1/w/default/board", f.tokenB); code != 200 {
		t.Fatalf("凭令牌仍能读看板：%d", code)
	}
	// 彻底删除：成员 403；名字不一致 400；有未结束 409
	const purge = "/api/v1/server/workspaces/default/purge"
	if code, out := member.do("POST", purge, map[string]any{"confirm": "default"}, ""); code != 403 {
		t.Fatalf("非服务器管理员不能彻底删除：%d %v", code, out)
	}
	code, out = admin.do("POST", purge, map[string]any{"confirm": "other"}, "")
	if code != 400 || out["code"] != "purge_confirm_mismatch" {
		t.Fatalf("确认名字不一致应 400：%d %v", code, out)
	}
	if code, out := admin.do("POST", purge, map[string]any{"confirm": "default"}, ""); code != 409 {
		t.Fatalf("有未结束单子应 409：%d %v", code, out)
	}

	// 取消归档后作废那张单，再归档（不再有未结束），然后彻底删除
	if code, out := admin.do("POST", adm+"/unarchive", nil, ""); code != 200 || out["archived"] != false {
		t.Fatalf("取消归档：%d %v", code, out)
	}
	waitAnnounce(t, ann, fmt.Sprintf("unarchive:%d:lee-codex", wsID))
	if code, out := admin.do("POST", path+"/cancel", map[string]any{"expected_version": is.Version, "reason": "需求取消"}, ""); code != 200 {
		t.Fatalf("取消归档后可作废：%d %v", code, out)
	}
	if code, out := admin.do("POST", adm+"/archive", map[string]any{"confirm_open": 0}, ""); code != 200 {
		t.Fatalf("没有未结束单子时确认数 0 即可归档：%d %v", code, out)
	}
	code, out = admin.do("POST", purge, map[string]any{"confirm": "default"}, "")
	if code != 200 || out["issues"] != float64(1) || out["drafts"] != float64(1) {
		t.Fatalf("彻底删除应返回问题单数与草稿数：%d %v", code, out)
	}
	if _, err := store.WorkspaceByName(ctx, q, "default"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("工作空间应已删除：%v", err)
	}
	var archives, purges int
	if err := q.QueryRow(ctx, `SELECT count(*) FILTER (WHERE action = 'workspace.archive'), count(*) FILTER (WHERE action = 'workspace.purge')
		FROM admin_events`).Scan(&archives, &purges); err != nil || archives != 2 || purges != 1 {
		t.Fatalf("管理记录应保留（归档 2 次）并新增彻底删除 1 条：%d %d %v", archives, purges, err)
	}
}

// waitAnnounce 等后台告知到达（接口在后台发群消息，不拖慢请求）。
func waitAnnounce(t *testing.T, ann *fakeAnnouncer, want string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		ann.mu.Lock()
		for _, a := range ann.archive {
			if a == want {
				ann.mu.Unlock()
				return
			}
		}
		ann.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("群里没有收到告知 %s", want)
}
