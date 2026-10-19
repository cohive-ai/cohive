package db_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/testutil"
)

// 第十四号迁移：已有的 Telegram、钉钉数据原样保留；之后 Slack 等已规划的通讯工具能写入，不认识的仍被拒绝。
func TestMigrate0014ChatProviders(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.MigrateUpTo(ctx, pool, "0013"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, name) VALUES (41, 'a'), (42, 'b');
		INSERT INTO workspaces (id, name) VALUES (41, 'w1'), (42, 'w2');
		INSERT INTO chat_identities (account_id, provider, tenant, user_id) VALUES (41, 'telegram', '', '9001'), (41, 'dingtalk', 'corp', 'u1');
		INSERT INTO workspace_chats (workspace_id, provider, chat_id) VALUES (41, 'dingtalk', 'cid1');
		INSERT INTO admin_events (via, action, target_kind) VALUES ('dingtalk', 'x', 'account');`); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO chat_identities (account_id, provider, user_id) VALUES (42, 'slack', 'U1')`); err == nil {
		t.Fatal("迁移前不应接受 slack")
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("第十四号迁移失败：%v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM chat_identities) + (SELECT count(*) FROM workspace_chats)
		+ (SELECT count(*) FROM admin_events)`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("已有行应原样保留：%d %v", n, err)
	}
	for _, p := range []string{"slack", "feishu", "lark", "discord"} {
		if _, err := pool.Exec(ctx, `INSERT INTO chat_identities (account_id, provider, tenant, user_id) VALUES (42, $1, 'T1', 'U1')`, p); err != nil {
			t.Fatalf("%s 通讯账号应能写入：%v", p, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO workspace_chats (workspace_id, provider, chat_id) VALUES (42, $1, 'C-'||$1)`, p); err != nil {
			t.Fatalf("%s 群应能写入：%v", p, err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO admin_events (via, action, target_kind) VALUES ($1, 'x', 'account')`, p); err != nil {
			t.Fatalf("%s 途径应能写入：%v", p, err)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workspace_chats (workspace_id, provider, chat_id) VALUES (41, 'wechat', 'x')`); err == nil {
		t.Fatal("不认识的通讯工具应被拒绝")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO admin_events (via, action, target_kind) VALUES ('wechat', 'x', 'account')`); err == nil {
		t.Fatal("不认识的途径应被拒绝")
	}
}
