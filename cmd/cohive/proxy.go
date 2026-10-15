package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/cohive-ai/cohive/internal/client"
)

// runProxy 运行本机转发器：从凭据文件取令牌，把标准输入的 MCP 消息转发到协作服务。
func runProxy(ctx context.Context, in io.Reader, out, errOut io.Writer, args []string) error {
	fs := flag.NewFlagSet("mcp-proxy", flag.ContinueOnError)
	fs.SetOutput(errOut)
	server := fs.String("server", "", "协作服务地址")
	workspace := fs.String("workspace", "", "工作空间名")
	tool := fs.String("tool", "", "编程工具名：claude-code 或 codex")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *server == "" || *workspace == "" || *tool == "" {
		return fmt.Errorf("mcp-proxy 需要 --server、--workspace、--tool，通常由 cohive init 写进项目配置")
	}
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	l, ok := creds.Find(*server, *workspace, *tool)
	if !ok {
		return fmt.Errorf("本机还没有登录 %s 的工作空间 %s（工具 %s）：私聊机器人发 /login 领取登录码，再执行 cohive login --server %s --code <登录码> --tool %s",
			client.NormalizeServer(*server), *workspace, *tool, client.NormalizeServer(*server), *tool)
	}
	p := &client.Proxy{Server: l.Server, Token: l.Token, Log: errOut}
	return p.Run(ctx, in, out)
}
