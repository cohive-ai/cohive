package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// startProxy 在进程内运行 cohive mcp-proxy，并用官方 MCP 客户端经管道连上它。
func startProxy(t *testing.T, args ...string) (*mcp.ClientSession, func() error) {
	t.Helper()
	toProxyR, toProxyW := io.Pipe()
	fromProxyR, fromProxyW := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := runProxy(context.Background(), toProxyR, fromProxyW, io.Discard, args)
		fromProxyW.Close()
		done <- err
	}()
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sess, err := c.Connect(ctx, &mcp.IOTransport{Reader: fromProxyR, Writer: toProxyW}, nil)
	if err != nil {
		toProxyW.Close()
		t.Fatalf("经转发器连接失败：%v", err)
	}
	return sess, func() error {
		sess.Close()
		toProxyW.Close()
		return <-done
	}
}

func TestProxyEndToEnd(t *testing.T) {
	configDir(t)
	s := newTestServer(t)
	if _, err := run(t, "login", "--server", s.url, "--code", s.code(t), "--tool", "codex"); err != nil {
		t.Fatal(err)
	}
	sess, stop := startProxy(t, "--server", s.url, "--workspace", "default", "--tool", "codex")
	defer stop()

	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) < 10 {
		t.Fatalf("应列出 Cohive 的全部工具，得到 %d 个", len(tools.Tools))
	}
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "cohive_draft_issue",
		Arguments: map[string]any{"title": "经转发器起草"}})
	if err != nil || res.IsError {
		t.Fatalf("经转发器调用工具失败：%v %+v", err, res)
	}
	inbox, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "cohive_inbox", Arguments: map[string]any{}})
	if err != nil || inbox.IsError {
		t.Fatalf("收件箱调用失败：%v", err)
	}
	text := inbox.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "经转发器起草") || !strings.Contains(text, "lee-codex") {
		t.Fatalf("收件箱应看到刚起草的草稿：%s", text)
	}
}

func TestProxyRevokedTokenExplains(t *testing.T) {
	configDir(t)
	s := newTestServer(t)
	creds := &client.Credentials{}
	creds.Upsert(client.Login{Server: s.url, Workspace: "default", Member: "lee-codex", Tool: "codex", Token: "cohive_revoked"})
	if err := creds.Save(); err != nil {
		t.Fatal(err)
	}
	toProxyR, toProxyW := io.Pipe()
	var out strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- runProxy(context.Background(), toProxyR, &out, io.Discard,
			[]string{"--server", s.url, "--workspace", "default", "--tool", "codex"})
	}()
	io.WriteString(toProxyW, `{"jsonrpc":"2.0","id":7,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`+"\n")
	io.WriteString(toProxyW, `{"jsonrpc":"2.0","method":"notifications/initialized"}`+"\n")
	toProxyW.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"id":7`) || !strings.Contains(lines[0], "cohive login") {
		t.Fatalf("令牌失效时应对请求返回带提示的 JSON-RPC 错误，通知不输出：%q", out.String())
	}
}
