package db_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/testutil"
)

// 第十三号迁移：已有账号、工作空间、注册申请的语言留空（跟随服务器缺省语言），不批量写入；值只接受语言代码形状。
func TestMigrate0013Language(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.MigrateUpTo(ctx, pool, "0012"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO workspaces (id, name) VALUES (31, 'w');
		INSERT INTO accounts (id, name) VALUES (31, 'a');
		INSERT INTO invites (id, workspace_id, code_hash, kind, expires_at) VALUES (31, 31, 'c', 'team', now() + interval '1 day');
		INSERT INTO signups (id, invite_id, workspace_id, name, password_hash) VALUES (31, 31, 31, 'n', 'h');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("第十三号迁移失败：%v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM accounts WHERE language IS NOT NULL)
		+ (SELECT count(*) FROM workspaces WHERE language IS NOT NULL)
		+ (SELECT count(*) FROM signups WHERE language IS NOT NULL)`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("已有行的语言应留空：%d %v", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE accounts SET language = 'zh-CN' WHERE id = 31`); err != nil {
		t.Fatalf("语言代码应能写入：%v", err)
	}
	for _, bad := range []string{"", "x", "zh CN", "en;drop"} {
		if _, err := pool.Exec(ctx, `UPDATE workspaces SET language = $1 WHERE id = 31`, bad); err == nil {
			t.Errorf("%q 不是语言代码，应被拒绝", bad)
		}
	}
}
