package db_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/testutil"
)

// 第九号迁移：把编号最小的、能认出来的工作空间公网地址规范化后提升为服务器级对外地址，删掉工作空间级字段，
// 加显示名（空表示与路径相同）。
func TestMigrate0009ExternalURL(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		urls []string // 按工作空间编号顺序（default 之后依次建）
		want string
	}{
		{"跳过空值，规范化大小写与末尾斜杠", []string{"", "HTTPS://Cohive.Example.com/", "https://other.example.com"}, "https://cohive.example.com"},
		{"带路径时去掉路径", []string{"https://x.example.com:8443/cohive?a=1"}, "https://x.example.com:8443"},
		{"认不出的值丢弃，取下一个", []string{"cohive.example.com", "ftp://x", "http://10.0.0.5:8787"}, "http://10.0.0.5:8787"},
		{"全都认不出", []string{"nope", " "}, ""},
		{"都没填", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pool := testutil.NewSchemaPool(t)
			if err := db.MigrateUpTo(ctx, pool, "0008"); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO workspaces (name) VALUES ('default') ON CONFLICT DO NOTHING`); err != nil {
				t.Fatal(err)
			}
			for i, u := range c.urls {
				if _, err := pool.Exec(ctx, `INSERT INTO workspaces (name, public_url) VALUES ($1, $2)`, "ws"+string(rune('a'+i)), u); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Migrate(ctx, pool); err != nil {
				t.Fatalf("第九号迁移失败：%v", err)
			}
			var got string
			var n int
			if err := pool.QueryRow(ctx, `SELECT external_url, (SELECT count(*) FROM server_settings) FROM server_settings`).Scan(&got, &n); err != nil {
				t.Fatal(err)
			}
			if got != c.want || n != 1 {
				t.Fatalf("对外地址 %q（%d 行），应为 %q（1 行）", got, n, c.want)
			}
			var col bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
				WHERE table_schema = current_schema() AND table_name = 'workspaces' AND column_name = 'public_url')`).Scan(&col); err != nil || col {
				t.Fatalf("工作空间级 public_url 应删掉：%v %v", col, err)
			}
			var blank int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM workspaces WHERE display_name <> ''`).Scan(&blank); err != nil || blank != 0 {
				t.Fatalf("显示名缺省为空：%d %v", blank, err)
			}
			// 只能有一行
			if _, err := pool.Exec(ctx, `INSERT INTO server_settings (id) VALUES (false)`); err == nil {
				t.Fatal("server_settings 不应能有第二行")
			}
		})
	}
}
