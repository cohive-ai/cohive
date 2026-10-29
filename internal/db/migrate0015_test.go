package db_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/testutil"
)

// 第十五号迁移：已有工作空间原样保留且都未归档；归档时间与归档人可写入。
func TestMigrate0015WorkspaceArchive(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.MigrateUpTo(ctx, pool, "0014"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, name) VALUES (51, 'a'); INSERT INTO workspaces (id, name) VALUES (51, 'w')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("第十五号迁移失败：%v", err)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM workspaces WHERE archived_at IS NULL AND archived_by IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("已有工作空间应都未归档：%d %v", n, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workspaces SET archived_at = now(), archived_by = 51 WHERE id = 51`); err != nil {
		t.Fatalf("归档时间与归档人应能写入：%v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE workspaces SET archived_by = 999 WHERE id = 51`); err == nil {
		t.Fatal("归档人必须是已有账号")
	}
}
