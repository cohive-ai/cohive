package server_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

func eventItems(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, ok := out["items"].([]any)
	if !ok {
		t.Fatalf("返回里没有 items：%v", out)
	}
	items := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		items = append(items, it.(map[string]any))
	}
	return items
}

func actions(items []map[string]any) []string {
	var out []string
	for _, it := range items {
		out = append(out, it["action"].(string))
	}
	return out
}

// 查看记录：工作空间管理员只看本空间的；服务器管理员看全部；筛选、翻页、参数校验；
// 被删工作空间的记录仍可查；操作人停用后记录照常显示，他本人不能再查看。
func TestViewAdminEvents(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	q := f.svc.Pool
	// lee-codex 是服务器管理员（不是 default 的管理员）；lee-claude 是 default 的管理员；ann 是 default 的普通成员
	if err := store.SetServerAdmin(ctx, q, f.b.AccountID, true); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMemberRole(ctx, q, f.a.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	setPassword(t, f, f.b.AccountID, "root-password-1")
	setPassword(t, f, f.a.AccountID, "wsadm-password-1")
	ann, err := store.CreateMember(ctx, q, f.a.WorkspaceID, "ann", "")
	if err != nil {
		t.Fatal(err)
	}
	setPassword(t, f, ann.AccountID, "ann-password-01")
	root := loginAs(t, f, "lee-codex", "root-password-1")
	wsAdmin := loginAs(t, f, "lee-claude", "wsadm-password-1")
	member := loginAs(t, f, "ann", "ann-password-01")
	rootAcc, _ := store.AccountByID(ctx, q, f.b.AccountID)
	wsAcc, _ := store.AccountByID(ctx, q, f.a.AccountID)

	// 造记录：default 里两条（lee-claude 改角色、生成邀请），ops 里一条，服务器级两条，tmp 建了又删，再在 default 里改一次角色
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(f.svc.SetRole(ctx, core.WebActor(wsAcc), f.a.WorkspaceID, ann.ID, store.RoleAdmin))
	_, err = f.svc.CreateInvite(ctx, core.WebActor(wsAcc), f.a.WorkspaceID, core.InviteSpec{Kind: store.InviteTeam})
	must(err)
	_, err = f.svc.CreateWorkspace(ctx, core.WebActor(rootAcc), "ops", "")
	must(err)
	must(f.svc.SetServerAdmin(ctx, core.ServerCLI(), ann.AccountID, true))
	_, err = f.svc.CreateWorkspace(ctx, core.WebActor(rootAcc), "tmp", "")
	must(err)
	must(f.svc.DeleteWorkspace(ctx, core.WebActor(rootAcc), "tmp"))

	// 工作空间管理员：只看本空间（不含 ops、不含服务器级）
	code, out := wsAdmin.do("GET", "/api/v1/w/default/admin/events", nil, "")
	if got := actions(eventItems(t, out)); code != 200 || len(got) != 2 || got[0] != core.ActInviteCreate || got[1] != core.ActMemberRole {
		t.Fatalf("工作空间管理员应只看到本空间的两条：%d %v", code, out)
	}
	it := eventItems(t, out)[1]
	if it["text"] != "lee-claude 在网页上把 ann 的角色从成员改为管理员" || it["via_label"] != "网页" || it["action_label"] != "改角色" {
		t.Fatalf("人话与中文名：%v", it)
	}
	if out["types"] == nil || out["vias"] == nil {
		t.Fatalf("应附筛选用的类别与入口：%v", out)
	}
	if code, _ := wsAdmin.do("GET", "/api/v1/w/ops/admin/events", nil, ""); code != 404 {
		t.Fatalf("别的工作空间应 404：%d", code)
	}
	if code, _ := wsAdmin.do("GET", "/api/v1/server/events", nil, ""); code != 403 {
		t.Fatalf("工作空间管理员不能看服务器记录：%d", code)
	}
	// ann 已是服务器管理员：先撤销再看普通成员被拒
	must(f.svc.SetServerAdmin(ctx, core.ServerCLI(), ann.AccountID, false))
	must(f.svc.SetRole(ctx, core.ServerCLI(), f.a.WorkspaceID, ann.ID, store.RoleMember))
	if code, _ := member.do("GET", "/api/v1/w/default/admin/events", nil, ""); code != 403 {
		t.Fatalf("普通成员应 403：%d", code)
	}

	// 服务器管理员：全部，可按工作空间筛选（含已删除的）
	_, out = root.do("GET", "/api/v1/server/events", nil, "")
	all := eventItems(t, out)
	// 三人设密码 3 条 + 上面造的 8 条 + 两次被拒（lee-claude 看服务器记录、ann 看工作空间记录）；看别的工作空间 404 不记
	if len(all) != 13 {
		t.Fatalf("服务器管理员应看到全部 13 条：%v", actions(all))
	}
	_, out = root.do("GET", "/api/v1/server/events?workspace=ops", nil, "")
	if got := actions(eventItems(t, out)); len(got) != 1 || got[0] != core.ActWorkspaceCreate {
		t.Fatalf("按工作空间筛选：%v", got)
	}
	_, out = root.do("GET", "/api/v1/server/events?workspace=tmp", nil, "")
	if got := eventItems(t, out); len(got) != 2 || got[0]["action"] != core.ActWorkspaceDelete || got[0]["workspace"] != "tmp" {
		t.Fatalf("已删除的工作空间仍可按名字查到：%v", got)
	}

	// 筛选：类别、操作人、入口
	for _, c := range []struct {
		query string
		want  int
	}{
		{"type=role", 2}, {"type=account", 5}, {"type=workspace", 3}, {"type=denied", 2}, {"actor=lee-claude", 4}, {"actor=nobody", 0},
		{"via=server_cli", 3}, {"via=web&actor=lee-codex", 4},
	} {
		_, out := root.do("GET", "/api/v1/server/events?"+c.query, nil, "")
		if got := eventItems(t, out); len(got) != c.want {
			t.Errorf("%s：应 %d 条，得到 %v", c.query, c.want, actions(got))
		}
	}
	for _, bad := range []string{"type=nope", "via=x", "limit=0", "limit=500", "before=abc", "before=-1"} {
		if code, _ := root.do("GET", "/api/v1/server/events?"+bad, nil, ""); code != 400 {
			t.Errorf("%s 应 400：%d", bad, code)
		}
	}

	// 翻页：每页 4 条，翻页期间写入新记录也不重不漏
	_, out = root.do("GET", "/api/v1/server/events?limit=4", nil, "")
	p1 := eventItems(t, out)
	next := out["next_before"]
	_, err = f.svc.CreateWorkspace(ctx, core.WebActor(rootAcc), "new", "")
	must(err)
	var ids []float64
	for _, it := range p1 {
		ids = append(ids, it["id"].(float64))
	}
	for next != nil {
		_, out = root.do("GET", fmt.Sprintf("/api/v1/server/events?limit=4&before=%v", next), nil, "")
		for _, it := range eventItems(t, out) {
			ids = append(ids, it["id"].(float64))
		}
		next = out["next_before"]
	}
	if len(ids) != len(all) {
		t.Fatalf("翻页应拿到原有的 %d 条，得到 %v", len(all), ids)
	}
	for i := range ids {
		if ids[i] != all[i]["id"].(float64) {
			t.Fatalf("翻页顺序不对：%v", ids)
		}
	}

	// 操作人停用后：他的记录照常显示，他本人不能再查看
	must(f.svc.DisableAccount(ctx, core.WebActor(rootAcc), wsAcc.ID))
	_, out = root.do("GET", "/api/v1/server/events?actor=lee-claude", nil, "")
	if got := eventItems(t, out); len(got) != 4 {
		t.Fatalf("停用后记录应保留：%v", got)
	}
	if code, _ := wsAdmin.do("GET", "/api/v1/w/default/admin/events", nil, ""); code < 400 {
		t.Fatalf("停用的人不能再查看：%d", code)
	}
}
