package server_test

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/server"
	"github.com/cohive-ai/cohive/internal/testutil"
)

// 同一成员名连续输错密码后被锁定：即使随后输入正确密码也要等锁定结束。
func TestLoginLockedAfterFailures(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	svc := &core.Service{Pool: pool, DataDir: filepath.Join(t.TempDir(), "data")}
	path, err := svc.EnsureInitialAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(svc, builtWeb, server.Options{}))
	defer srv.Close()
	b := newBrowser(t, srv.URL)
	for i := 0; i < 5; i++ {
		if code, _ := b.do("POST", "/api/v1/auth/login", map[string]string{"name": "admin", "password": "wrong-password"}, ""); code != 401 {
			t.Fatalf("第 %d 次错误密码应为 401：%d", i+1, code)
		}
	}
	code, out := b.do("POST", "/api/v1/auth/login", map[string]string{"name": "admin", "password": initialPassword(t, path)}, "")
	if code != 429 || out["error"] == nil {
		t.Fatalf("连续失败后应锁定：%d %v", code, out)
	}
}

// 无效邀请码：先报邀请无效，不先校验密码（也就不会先算密码哈希）。
func TestRegisterChecksInviteFirst(t *testing.T) {
	f := newFixture(t, builtWeb)
	b := newBrowser(t, f.url)
	code, out := b.do("POST", "/api/v1/register", map[string]string{"code": "nope", "name": "ann", "password": "short"}, "")
	if code != 404 || out["error"] == nil || out["error"] == "密码至少 10 位" {
		t.Fatalf("应先报邀请无效：%d %v", code, out)
	}
}
