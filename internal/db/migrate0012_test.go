package db_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/testutil"
)

// 第十二号迁移：现有问题单从升级时刻开始计时、档位为 0；工作空间默认 4、24 小时；时长与档位有范围约束。
func TestMigrate0012OverdueReminder(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.MigrateUpTo(ctx, pool, "0011"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO workspaces (id, name) VALUES (21, 'w');
		INSERT INTO accounts (id, name) VALUES (21, 'a');
		INSERT INTO members (id, workspace_id, account_id) VALUES (21, 21, 21);
		INSERT INTO issues (workspace_id, source_member_id, source_tool, owner_id, title, status, updated_at)
		VALUES (21, 21, 'codex', 21, '旧单', 'OPEN', now() - interval '30 days');`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("第十二号迁移失败：%v", err)
	}
	var fresh bool
	var level, open, verify int
	if err := pool.QueryRow(ctx, `SELECT status_since > now() - interval '1 minute', remind_level FROM issues`).Scan(&fresh, &level); err != nil || !fresh || level != 0 {
		t.Fatalf("现有问题单从升级时刻开始计时（不按 30 天前补催）：%v %d %v", fresh, level, err)
	}
	if err := pool.QueryRow(ctx, `SELECT remind_open_hours, remind_verify_hours FROM workspaces WHERE id = 21`).Scan(&open, &verify); err != nil || open != 4 || verify != 24 {
		t.Fatalf("默认时长：%d %d %v", open, verify, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workspaces SET remind_open_hours = 721 WHERE id = 21`); err == nil {
		t.Fatal("时长超过 720 应被拒绝")
	}
	if _, err := pool.Exec(ctx, `UPDATE issues SET remind_level = 3`); err == nil {
		t.Fatal("档位只能是 0 到 2")
	}
}
