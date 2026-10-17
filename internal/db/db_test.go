package db_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/testutil"
)

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)

	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("第一次迁移失败：%v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("第二次迁移失败：%v", err)
	}

	var n int
	err := pool.QueryRow(ctx,
		`SELECT count(*) FROM information_schema.tables
		 WHERE table_schema = current_schema() AND table_name = 'issues'`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("issues 表应存在，实际数量 %d", n)
	}

	var applied int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 9 {
		t.Fatalf("应登记 9 个迁移，实际 %d", applied)
	}
}
