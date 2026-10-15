// Package testutil 提供测试用的独立数据库环境。
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultURL 是开发用数据库（deploy/docker-compose.dev.yml）的地址。
const DefaultURL = "postgres://cohive:cohive@localhost:55432/cohive?sslmode=disable"

// DatabaseURL 返回测试数据库地址，可用 COHIVE_DATABASE_URL 覆盖。
func DatabaseURL() string {
	if u := os.Getenv("COHIVE_DATABASE_URL"); u != "" {
		return u
	}
	return DefaultURL
}

// NewSchemaPool 为当前测试创建一个随机命名的 schema，并返回 search_path 指向它的连接池。
// 测试结束时删除该 schema。不执行迁移。
func NewSchemaPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	admin, err := pgxpool.New(ctx, DatabaseURL())
	if err != nil {
		t.Fatalf("连接测试数据库失败（先执行 make db-up）：%v", err)
	}
	buf := make([]byte, 6)
	if _, err := rand.Read(buf); err != nil {
		t.Fatal(err)
	}
	schema := "t_" + hex.EncodeToString(buf)
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		admin.Close()
		t.Fatalf("创建测试 schema 失败：%v", err)
	}

	cfg, err := pgxpool.ParseConfig(DatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		admin.Close()
	})
	return pool
}
