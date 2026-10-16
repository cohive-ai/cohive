package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cohive-ai/cohive/internal/client"
	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/server"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
)

type testServer struct {
	url string
	svc *core.Service
	uid int64
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
	if err := store.SetMemberTelegramUserID(ctx, pool, m.ID, &uid); err != nil {
		t.Fatal(err)
	}
	svc := &core.Service{Pool: pool}
	srv := httptest.NewServer(server.New(svc, fstest.MapFS{}, server.Options{BotName: func() string { return "CohiveBot" }}))
	t.Cleanup(srv.Close)
	return testServer{url: srv.URL, svc: svc, uid: uid}
}

func (s testServer) code(t *testing.T) string {
	t.Helper()
	res, err := s.svc.IssueLoginCode(context.Background(), s.uid, "")
	if err != nil {
		t.Fatal(err)
	}
	return res.Code
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

func TestLogin(t *testing.T) {
	configDir(t)
	s := newTestServer(t)
	out, err := run(t, "login", "--server", s.url+"/", "--code", s.code(t), "--tool", "codex")
	if err != nil {
		t.Fatalf("登录失败：%v\n%s", err, out)
	}
	if !strings.Contains(out, "lee-codex") || !strings.Contains(out, "cohive init") {
		t.Fatalf("应提示登录成功和下一步：%s", out)
	}
	if strings.Contains(out, "cohive_") {
		t.Fatalf("输出里不应出现令牌：%s", out)
	}

	creds, err := client.LoadCredentials()
	if err != nil {
		t.Fatal(err)
	}
	l, ok := creds.Find(s.url, "default", "codex")
	if !ok {
		t.Fatalf("应保存登录：%+v", creds)
	}
	req, _ := http.NewRequest(http.MethodGet, s.url+"/api/v1/me", nil)
	req.Header.Set("Authorization", "Bearer "+l.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("保存的令牌应可用，得到 %d", resp.StatusCode)
	}

	status, err := run(t, "status")
	if err != nil || !strings.Contains(status, "lee-codex") || strings.Contains(status, l.Token) {
		t.Fatalf("status 应列出登录且不显示令牌：%v\n%s", err, status)
	}
}

func TestLoginErrors(t *testing.T) {
	configDir(t)
	s := newTestServer(t)
	if _, err := run(t, "login", "--server", s.url, "--code", "AAAA-AAAA", "--tool", "codex"); err == nil || !strings.Contains(err.Error(), "/login") {
		t.Fatalf("错误的登录码应提示重新领取，得到 %v", err)
	}
	if _, err := run(t, "login", "--server", "cohive.example.com", "--code", "X", "--tool", "codex"); err == nil || !strings.Contains(err.Error(), "http") {
		t.Fatalf("服务地址缺协议时应报错，得到 %v", err)
	}
	if _, err := run(t, "login", "--server", s.url, "--tool", "codex"); err == nil || !strings.Contains(err.Error(), "--code") {
		t.Fatalf("缺登录码时应报错，得到 %v", err)
	}
	// 照抄占位文字、长度不对时在本地就提示格式，不发到服务端
	for _, bad := range []string{"登录码", "ABC-DEF", "ABCD-EFGH-IJ"} {
		if _, err := run(t, "login", "--server", "http://127.0.0.1:1", "--code", bad, "--tool", "codex"); err == nil || !strings.Contains(err.Error(), "ABCD-EFGH") {
			t.Fatalf("%q 应在本地提示登录码格式，得到 %v", bad, err)
		}
	}
}
