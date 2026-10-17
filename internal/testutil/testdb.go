// Package testutil 提供测试用的独立数据库环境。
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

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
	// 用 schema 名作连接的应用名，测试可据此在 pg_stat_activity 里只看自己的连接（见 WaitLockWaiters）
	cfg.ConnConfig.RuntimeParams["application_name"] = schema
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

// WaitLockWaiters 等到本测试连接池里至少有 n 个连接在等锁（按应用名过滤，不受其他测试影响）。
// 用于在并发用例里确认某个事务已经停在预期的锁上，再放行下一步。
func WaitLockWaiters(t *testing.T, pool *pgxpool.Pool, n int) {
	t.Helper()
	app := pool.Config().ConnConfig.RuntimeParams["application_name"]
	deadline := time.Now().Add(10 * time.Second)
	for {
		var got int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity
			WHERE application_name = $1 AND wait_event_type = 'Lock'`, app).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("等了 10 秒，等锁的连接只有 %d 个，预期至少 %d 个", got, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
