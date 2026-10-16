package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/server"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
)

type testServer struct {
	url       string
	svc       *core.Service
	uid       int64
	memberID  int64
	accountID int64
}

// newTestServer 起一个真实的协作服务，并把成员 lee-codex 绑定到 Telegram 用户编号 42。
func newTestServer(t *testing.T) testServer {
	t.Helper()
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	ws, err := store.DefaultWorkspace(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.CreateMember(ctx, pool, ws, "lee-codex", "")
	if err != nil {
		t.Fatal(err)
	}
	uid := int64(42)
	if err := store.SetAccountTelegramUserID(ctx, pool, m.AccountID, &uid); err != nil {
		t.Fatal(err)
	}
	svc := &core.Service{Pool: pool}
	srv := httptest.NewServer(server.New(svc, fstest.MapFS{}, server.Options{}))
	t.Cleanup(srv.Close)
	return testServer{url: srv.URL, svc: svc, uid: uid, memberID: m.ID, accountID: m.AccountID}
}

// token 直接为 lee-codex 签发一个不属于设备的令牌（相当于管理员手工签发）。
func (s testServer) token(t *testing.T, tool string) string {
	t.Helper()
	plain, _, err := store.IssueToken(context.Background(), s.svc.Pool, s.memberID, tool)
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

// approve 模拟成员在浏览器里授权最近一次待授权的设备。
func (s testServer) approve(t *testing.T, ok bool) {
	ctx := context.Background()
	m, err := store.AccountByID(ctx, s.svc.Pool, s.accountID)
	if err != nil {
		t.Error(err)
		return
	}
	for i := 0; i < 300; i++ {
		var nonce string
		err := s.svc.Pool.QueryRow(ctx, `SELECT nonce FROM device_auths WHERE status = 'pending' ORDER BY id DESC LIMIT 1`).Scan(&nonce)
		if err == nil {
			if _, err := s.svc.AuthorizeDevice(ctx, m, nonce, ok); err != nil {
				t.Error(err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("没有等到设备授权请求")
}

// adminApprove 以服务器管理员身份批准正在等待的设备申请。
func (s testServer) adminApprove(t *testing.T) {
	t.Helper()
	var id int64
	if err := s.svc.Pool.QueryRow(context.Background(), `SELECT id FROM device_auths WHERE status = 'awaiting_admin'`).Scan(&id); err != nil {
		t.Fatalf("没有等待批准的设备：%v", err)
	}
	if _, err := s.svc.DecideDeviceAuth(context.Background(), nil, 0, id, true); err != nil {
		t.Fatal(err)
	}
}

// login 走完整的设备授权登录这台电脑。
func (s testServer) login(t *testing.T, tools ...string) string {
	t.Helper()
	fastDevice(t)
	go s.approve(t, true)
	out, err := run(t, "login", "--server", s.url, "--tools", strings.Join(tools, ","), "--open=false")
	if err != nil {
		t.Fatalf("登录失败：%v\n%s", err, out)
	}
	return out
}

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runCLI(context.Background(), strings.NewReader(""), &out, args)
	return out.String(), err
}

func configDir(t *testing.T) {
	t.Helper()
	t.Setenv("COHIVE_CONFIG_DIR", filepath.Join(t.TempDir(), "cohive"))
}

func TestVersionCommand(t *testing.T) {
	out, err := run(t, "version")
	if err != nil || !strings.HasPrefix(out, "cohive ") {
		t.Fatalf("cohive version：%v %q", err, out)
	}
}
