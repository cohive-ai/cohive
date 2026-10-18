package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/cohive-ai/cohive/internal/client"
	"github.com/cohive-ai/cohive/internal/i18n"
)

// runProxy 运行本机转发器：从凭据文件取令牌，把标准输入的 MCP 消息转发到协作服务。
// 本机没有这个工作空间的令牌、但有这个服务的设备密钥时，自动为本人签发（新加入工作空间后不必重新配对）；
// 令牌被拒（例如被移出后又被加回）时用设备密钥重签一次再重试。
func runProxy(ctx context.Context, in io.Reader, out, errOut io.Writer, args []string) error {
	fs := flag.NewFlagSet("mcp-proxy", flag.ContinueOnError)
	fs.SetOutput(errOut)
	server := fs.String("server", "", i18n.T("cli.flag.proxy.server"))
	workspace := fs.String("workspace", "", i18n.T("cli.flag.proxy.workspace"))
	tool := fs.String("tool", "", i18n.T("cli.flag.proxy.tool"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *server == "" || *workspace == "" || *tool == "" {
		return i18n.Errorf("cli.proxy.missing_args")
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
			return "", i18n.Errorf("cli.proxy.no_token_returned")
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
			p.Unavailable = i18n.T("cli.proxy.not_member", i18n.D{"Workspace": *workspace})
		case err != nil:
			p.Unavailable = i18n.T("cli.proxy.issue_failed", i18n.D{"Workspace": *workspace, "Tool": *tool, "Err": i18n.Message(i18n.Default(), err)})
		default:
			fmt.Fprintf(errOut, "cohive mcp-proxy: %s\n", i18n.T("cli.proxy.issued", i18n.D{"Workspace": *workspace, "Tool": *tool}))
			p.Token = tok
		}
	default:
		// 照常启动，对请求返回原因，用户能在编程工具里直接看到，而不只是在日志里
		p.Unavailable = i18n.T("cli.proxy.not_logged_in", i18n.D{"Server": srv, "Workspace": *workspace, "Tool": *tool})
		if _, legacy := creds.LegacyDevice(srv); legacy {
			p.Unavailable = i18n.T("cli.proxy.legacy", i18n.D{"Workspace": *workspace, "Server": srv})
		}
	}
	if hasDev {
		p.Refresh = issue
	}
	return p.Run(ctx, in, out)
}
