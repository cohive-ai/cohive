package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/cohive-ai/cohive/internal/client"
)

// runProxy 运行本机转发器：从凭据文件取令牌，把标准输入的 MCP 消息转发到协作服务。
// 本机没有这个工作空间的令牌、但有这个服务的设备密钥时，自动为本人签发（新加入工作空间后不必重新配对）；
// 令牌被拒（例如被移出后又被加回）时用设备密钥重签一次再重试。
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
	srv := client.NormalizeServer(*server)
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	p := &client.Proxy{Log: errOut, Server: srv}
	dev, hasDev := creds.Device(srv)
	issue := func(ctx context.Context) (string, error) {
		wt, err := client.IssueTokens(ctx, srv, dev.Secret, *workspace, []string{*tool})
		if err != nil {
			return "", err
		}
		tok := wt.Tokens[*tool]
		if tok == "" {
			return "", errors.New("协作服务没有返回令牌")
		}
		// 重新读一次再保存，免得覆盖别的转发器刚写进去的内容
		c, err := client.LoadCredentials()
		if err != nil {
			return "", err
		}
		c.Upsert(client.Login{Server: srv, Workspace: *workspace, Member: dev.Account, Tool: *tool, Token: tok,
			DeviceID: dev.DeviceID, CreatedAt: time.Now()})
		return tok, c.Save()
	}
	l, ok := creds.Find(srv, *workspace, *tool)
	switch {
	case ok:
		p.Token = l.Token
	case hasDev:
		tok, err := issue(ctx)
		switch {
		case errors.Is(err, client.ErrNotMember):
			p.Unavailable = fmt.Sprintf("你还不是工作空间 %s 的成员，请联系它的管理员把你加进去", *workspace)
		case err != nil:
			p.Unavailable = fmt.Sprintf("为工作空间 %s 签发 %s 的令牌失败：%v", *workspace, *tool, err)
		default:
			fmt.Fprintf(errOut, "cohive mcp-proxy: 已为工作空间 %s 签发 %s 的令牌\n", *workspace, *tool)
			p.Token = tok
		}
	default:
		// 照常启动，对请求返回原因，用户能在编程工具里直接看到，而不只是在日志里
		p.Unavailable = fmt.Sprintf("本机还没有登录 %s 的工作空间 %s（工具 %s）：在仓库目录执行 cohive login --server %s --tools %s，在浏览器里授权即可",
			srv, *workspace, *tool, srv, *tool)
		if _, legacy := creds.LegacyDevice(srv); legacy {
			p.Unavailable = fmt.Sprintf("这台电脑是旧版配对，不能自动拿到工作空间 %s 的令牌：请执行 cohive login --server %s --force 重新配对", *workspace, srv)
		}
	}
	if hasDev {
		p.Refresh = issue
	}
	return p.Run(ctx, in, out)
}
