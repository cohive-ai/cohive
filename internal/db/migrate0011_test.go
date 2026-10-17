package db_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/testutil"
)

// 第十一号迁移：Telegram 连接、群绑定、审批消息记录无损迁到通讯账号表、工作空间群表。
func TestMigrate0011ChatChannels(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.MigrateUpTo(ctx, pool, "0010"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO accounts (id, name, telegram_user_id, telegram_username) VALUES
		  (1, 'both', 9001, 'bothtg'),   -- 编号和用户名都有
		  (2, 'uname', NULL, 'onlyname'), -- 命令行只登记了用户名
		  (3, 'uid', 9003, ''),           -- 只有编号（没设用户名）
		  (4, 'none', NULL, '');          -- 没连接
		INSERT INTO workspaces (id, name, notify_channel, notify_target) VALUES
		  (11, 'bound', 'telegram', '-1001'),
		  (12, 'off', '', '-1002'),       -- 关闭了群通知，群编号还留着
		  (13, 'never', '', '');
		INSERT INTO bot_posts (kind, ref_id, workspace_id, chat_id, message_id) VALUES
		  ('su', 5, 11, -1001, 77), ('da', 6, 11, -1001, 78);`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("第十一号迁移失败：%v", err)
	}

	type ident struct{ user, handle string }
	got := map[int64]ident{}
	rows, err := pool.Query(ctx, `SELECT account_id, user_id, handle FROM chat_identities WHERE provider = 'telegram' AND tenant = ''`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id int64
		var i ident
		if err := rows.Scan(&id, &i.user, &i.handle); err != nil {
			t.Fatal(err)
		}
		got[id] = i
	}
	rows.Close()
	want := map[int64]ident{1: {"9001", "bothtg"}, 2: {"", "onlyname"}, 3: {"9003", ""}}
	if len(got) != len(want) {
		t.Fatalf("通讯账号 %v，应为 %v", got, want)
	}
	for id, w := range want {
		if got[id] != w {
			t.Fatalf("账号 %d 的通讯账号 %+v，应为 %+v", id, got[id], w)
		}
	}

	type chat struct{ provider, id string }
	chats := map[int64]chat{}
	rows, err = pool.Query(ctx, `SELECT workspace_id, provider, chat_id FROM workspace_chats`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var ws int64
		var c chat
		if err := rows.Scan(&ws, &c.provider, &c.id); err != nil {
			t.Fatal(err)
		}
		chats[ws] = c
	}
	rows.Close()
	if len(chats) != 1 || chats[11] != (chat{"telegram", "-1001"}) {
		t.Fatalf("工作空间群 %v，应只有 bound → telegram -1001（关闭了群通知的不迁）", chats)
	}

	var posts int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bot_posts
		WHERE provider = 'telegram' AND chat_id = '-1001' AND message_id IN ('77', '78')`).Scan(&posts); err != nil || posts != 2 {
		t.Fatalf("审批消息记录应原样保留：%d %v", posts, err)
	}

	var cols int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
		WHERE table_schema = current_schema() AND ((table_name = 'accounts' AND column_name LIKE 'telegram%')
		  OR (table_name = 'workspaces' AND column_name IN ('notify_channel', 'notify_target')))`).Scan(&cols); err != nil || cols != 0 {
		t.Fatalf("旧列应删掉：%d %v", cols, err)
	}

	// 一个 Telegram 账号只能连一个 Cohive 账号；一个群只能绑一个工作空间
	if _, err := pool.Exec(ctx, `INSERT INTO chat_identities (account_id, provider, user_id) VALUES (4, 'telegram', '9001')`); err == nil {
		t.Fatal("同一 Telegram 编号不应能连第二个账号")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO workspace_chats (workspace_id, provider, chat_id) VALUES (13, 'telegram', '-1001')`); err == nil {
		t.Fatal("同一个群不应能绑第二个工作空间")
	}
	// 只登记用户名的可以有多条（编号为空不参与唯一约束），但编号和用户名不能都为空
	if _, err := pool.Exec(ctx, `INSERT INTO chat_identities (account_id, provider, handle) VALUES (4, 'telegram', 'x')`); err != nil {
		t.Fatalf("只有用户名的连接应允许：%v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO chat_identities (account_id, provider) VALUES (4, 'dingtalk')`); err == nil {
		t.Fatal("编号和用户名都为空的连接不应允许")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO admin_events (actor_name, via, action, target_kind)
		VALUES ('x', 'dingtalk', 'account.dingtalk_link', 'account')`); err != nil {
		t.Fatalf("管理操作记录应接受途径 dingtalk：%v", err)
	}
}
