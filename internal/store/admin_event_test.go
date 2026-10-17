package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func insertEvent(t *testing.T, pool *pgxpool.Pool, e store.AdminEvent) {
	t.Helper()
	ctx := context.Background()
	err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error { return store.InsertAdminEvent(ctx, tx, e) })
	if err != nil {
		t.Fatal(err)
	}
}

func ids(es []store.AdminEvent) []int64 {
	var out []int64
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func TestInsertAndListAdminEvents(t *testing.T) {
	ctx := context.Background()
	pool, ws := setup(t)
	acc, err := store.CreateAccount(ctx, pool, "lee", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateWorkspace(ctx, pool, "pay")
	if err != nil {
		t.Fatal(err)
	}
	insertEvent(t, pool, store.AdminEvent{ActorAccountID: &acc.ID, ActorName: "lee", Via: "web", WorkspaceID: &ws,
		WorkspaceName: "default", Action: "member.role", TargetKind: "member", TargetName: "ann",
		Detail: map[string]any{"from": "member", "to": "admin"}})
	insertEvent(t, pool, store.AdminEvent{Via: "server_cli", Action: "account.reset_password", TargetKind: "account", TargetName: "ann"})
	insertEvent(t, pool, store.AdminEvent{ActorAccountID: &acc.ID, ActorName: "lee", Via: "telegram", WorkspaceID: &other.ID,
		WorkspaceName: "pay", Action: "signup.approve", TargetKind: "signup", TargetName: "dora"})

	all, err := store.ListAdminEvents(ctx, pool, store.AdminEventFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[0].ID <= all[1].ID || all[1].ID <= all[2].ID {
		t.Fatalf("应按编号倒序列出 3 条：%v", ids(all))
	}
	if all[2].Detail["to"] != "admin" || all[1].ActorAccountID != nil || all[1].Detail == nil {
		t.Fatalf("字段读回不对：%+v", all)
	}

	// 游标翻页：两页拼起来不重不漏
	p1, _ := store.ListAdminEvents(ctx, pool, store.AdminEventFilter{Limit: 2})
	p2, _ := store.ListAdminEvents(ctx, pool, store.AdminEventFilter{Limit: 2, Before: p1[len(p1)-1].ID})
	if got := append(ids(p1), ids(p2)...); len(got) != 3 || got[0] != all[0].ID || got[2] != all[2].ID {
		t.Fatalf("翻页结果 %v 应等于 %v", got, ids(all))
	}

	cases := []struct {
		name string
		f    store.AdminEventFilter
		want []int64
	}{
		{"工作空间", store.AdminEventFilter{WorkspaceID: &ws}, []int64{all[2].ID}},
		{"操作人", store.AdminEventFilter{ActorAccountID: &acc.ID}, []int64{all[0].ID, all[2].ID}},
		{"动作", store.AdminEventFilter{Actions: []string{"signup.approve", "account.reset_password"}}, []int64{all[0].ID, all[1].ID}},
		{"入口", store.AdminEventFilter{Via: "server_cli"}, []int64{all[1].ID}},
		{"组合", store.AdminEventFilter{ActorAccountID: &acc.ID, Via: "web"}, []int64{all[2].ID}},
	}
	for _, c := range cases {
		got, err := store.ListAdminEvents(ctx, pool, c.f)
		if err != nil {
			t.Fatal(err)
		}
		if g := ids(got); len(g) != len(c.want) || (len(g) > 0 && (g[0] != c.want[0] || g[len(g)-1] != c.want[len(c.want)-1])) {
			t.Errorf("按%s筛选：得到 %v，应为 %v", c.name, g, c.want)
		}
	}
}

func TestAdminEventsAppendOnly(t *testing.T) {
	ctx := context.Background()
	pool, _ := setup(t)
	insertEvent(t, pool, store.AdminEvent{Via: "server_cli", Action: "account.reset_password", TargetKind: "account", TargetName: "ann"})
	for _, sql := range []string{
		`UPDATE admin_events SET actor_name = 'x'`,
		`DELETE FROM admin_events`,
		`TRUNCATE admin_events`,
	} {
		_, err := pool.Exec(ctx, sql)
		if err == nil || !strings.Contains(err.Error(), "只能追加") {
			t.Errorf("%s 应被拦截，得到 %v", sql, err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM admin_events WHERE actor_name = ''`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("记录应原样保留，得到 %d 条（%v）", n, err)
	}
}

func TestAdminEventsSurviveWorkspaceDelete(t *testing.T) {
	ctx := context.Background()
	pool, _ := setup(t)
	w, err := store.CreateWorkspace(ctx, pool, "tmp")
	if err != nil {
		t.Fatal(err)
	}
	insertEvent(t, pool, store.AdminEvent{Via: "web", WorkspaceID: &w.ID, WorkspaceName: "tmp", Action: "workspace.create",
		TargetKind: "workspace", TargetID: &w.ID, TargetName: "tmp"})
	if err := store.DeleteEmptyWorkspace(ctx, pool, w.ID); err != nil {
		t.Fatalf("有记录的空工作空间也应能删除：%v", err)
	}
	got, err := store.ListAdminEvents(ctx, pool, store.AdminEventFilter{WorkspaceID: &w.ID})
	if err != nil || len(got) != 1 || got[0].WorkspaceName != "tmp" {
		t.Fatalf("删除工作空间后记录应保留并带原名：%+v（%v）", got, err)
	}
}
