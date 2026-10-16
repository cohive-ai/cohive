package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/client"
)

// 找不到用户主目录时不能退回写到当前目录（可能是项目目录，令牌会被提交进仓库）。
func TestNoHomeDirRefuses(t *testing.T) {
	t.Setenv("COHIVE_CONFIG_DIR", "")
	t.Setenv("HOME", "")
	if _, err := client.LoadCredentials(); err == nil || !strings.Contains(err.Error(), "COHIVE_CONFIG_DIR") {
		t.Fatalf("找不到主目录时应报错并提示设置 COHIVE_CONFIG_DIR，得到 %v", err)
	}
}

// 已有目录的权限不应被强行改掉。
func TestSaveKeepsExistingDirMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shared")
	os.Mkdir(dir, 0o755)
	t.Setenv("COHIVE_CONFIG_DIR", dir)
	c := &client.Credentials{}
	c.Upsert(client.Login{Server: "https://a", Workspace: "w", Tool: "codex", Token: "t"})
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o755 {
		t.Fatalf("已有目录的权限不应被修改，实际 %v", info.Mode().Perm())
	}
	if info, _ := os.Stat(client.CredentialsPath()); info.Mode().Perm() != 0o600 {
		t.Fatal("凭据文件本身仍应是 600")
	}
}

// 服务端返回的内容无法识别时，转发器要回一个错误，不能让编程工具一直等。
func TestProxyReportsBadResponse(t *testing.T) {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"trunc`))
	}))
	defer bad.Close()
	p := &client.Proxy{Server: bad.URL, Token: "cohive_secret_token"}
	var out strings.Builder
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n" + "not json\n")
	if err := p.Run(context.Background(), in, &out); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, `"id":1`) || !strings.Contains(got, "error") {
		t.Fatalf("应对请求 1 返回错误：%s", got)
	}
	if !strings.Contains(got, `"id":null`) || !strings.Contains(got, "-32700") {
		t.Fatalf("输入不是 JSON 时应返回解析错误：%s", got)
	}
}

// 服务端错误正文转给模型前要截短并抹掉令牌。
func TestProxyRedactsErrorBody(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		io.WriteString(w, "upstream saw header Authorization: "+r.Header.Get("Authorization")+strings.Repeat("x", 5000))
	}))
	defer echo.Close()
	p := &client.Proxy{Server: echo.URL, Token: "cohive_secret_token"}
	var out strings.Builder
	p.Run(context.Background(), strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`+"\n"), &out)
	got := out.String()
	if strings.Contains(got, "cohive_secret_token") {
		t.Fatalf("错误信息里不能出现令牌：%s", got)
	}
	if len(got) > 1500 {
		t.Fatalf("错误正文应截短，实际 %d 字节", len(got))
	}
}

// 没登录时转发器照常启动，对请求返回带提示的错误，用户能在编程工具里直接看到原因。
func TestProxyNotLoggedInAnswersWithHint(t *testing.T) {
	configDir(t)
	var out strings.Builder
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	err := runProxy(context.Background(), in, &out, io.Discard,
		[]string{"--server", "https://cohive.example.com", "--workspace", "default", "--tool", "codex"})
	if err != nil {
		t.Fatalf("没登录时转发器不应直接退出：%v", err)
	}
	if !strings.Contains(out.String(), `"id":1`) || !strings.Contains(out.String(), "cohive login") {
		t.Fatalf("应对初始化返回带登录提示的错误：%s", out.String())
	}
}

// 管理员签发的令牌从标准输入读入，校验后保存，之后 init 照常可用。
func TestLoginWithAdminToken(t *testing.T) {
	configDir(t)
	s := newTestServer(t)
	token := s.token(t, "claude-code")
	var out strings.Builder
	err := runCLI(context.Background(), strings.NewReader(token+"\n"), &out, []string{"login", "--server", s.url, "--token-stdin"})
	if err != nil {
		t.Fatalf("用令牌登录失败：%v", err)
	}
	if strings.Contains(out.String(), token) || !strings.Contains(out.String(), "lee-codex") {
		t.Fatalf("应显示成员且不回显令牌：%s", out.String())
	}
	creds, _ := client.LoadCredentials()
	if _, ok := creds.Find(s.url, "default", "claude-code"); !ok {
		t.Fatalf("应按令牌所属的工作空间与工具保存：%+v", creds)
	}
	if err := runCLI(context.Background(), strings.NewReader("cohive_wrong\n"), io.Discard, []string{"login", "--server", s.url, "--token-stdin"}); err == nil {
		t.Fatal("无效的令牌应报错")
	}
}
