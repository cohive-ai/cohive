// cohive 是成员本机使用的命令行：登录、初始化项目、为编程工具转发 MCP 请求。
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/cohive-ai/cohive/internal/buildinfo"
	"github.com/cohive-ai/cohive/internal/client"
	"github.com/cohive-ai/cohive/internal/i18n"
)

func main() {
	i18n.SetDefault(client.ResolveLanguage())
	if err := runCLI(context.Background(), os.Stdin, os.Stdout, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, i18n.T("cli.error", i18n.D{"Err": err.Error()}))
		os.Exit(1)
	}
}

func runCLI(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(out, i18n.T("cli.usage"))
		return nil
	}
	switch args[0] {
	case "login":
		return login(ctx, in, out, args[1:])
	case "status":
		return status(ctx, out)
	case "version", "--version":
		fmt.Fprintf(out, "cohive %s\n", buildinfo.String())
		return nil
	case "web":
		return openWeb(ctx, out, args[1:])
	case "init":
		return initProject(ctx, in, out, args[1:])
	case "unbind":
		return unbind(out, args[1:])
	case "hook":
		// 编程工具的会话钩子：任何情况下都正常退出，出错只是没有提醒
		runHook(ctx, in, out, args[1:])
		return nil
	case "mcp-proxy":
		return runProxy(ctx, in, out, os.Stderr, args[1:])
	case "help", "-h", "--help":
		fmt.Fprint(out, i18n.T("cli.usage"))
		return nil
	}
	fmt.Fprint(out, i18n.T("cli.usage"))
	return i18n.Errorf("cli.unknown_command", i18n.D{"Command": strconv.Quote(args[0])})
}

// loginWithToken 保存管理员用 cohive-server token issue 签发的令牌。
// 令牌从标准输入读取，不出现在命令行参数和 shell 历史里；先向服务端确认归属再保存。
func loginWithToken(ctx context.Context, in io.Reader, out io.Writer, server string) error {
	if server == "" {
		return i18n.Errorf("cli.login.missing_server")
	}
	srv, err := client.CheckServer(server)
	if err != nil {
		return err
	}
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	token := strings.TrimSpace(line)
	if !strings.HasPrefix(token, "cohive_") {
		return i18n.Errorf("cli.login.token_stdin_empty", i18n.D{"Server": srv})
	}
	l, err := client.Whoami(ctx, srv, token)
	if err != nil {
		return err
	}
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	creds.Upsert(l)
	if err := creds.Save(); err != nil {
		return err
	}
	fmt.Fprint(out, i18n.T("cli.login.token_saved", i18n.D{
		"Workspace": l.Workspace, "Member": l.Member, "Tool": l.Tool, "Path": client.CredentialsPath()}))
	return nil
}

func status(ctx context.Context, out io.Writer) error {
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	if len(creds.Logins) == 0 && len(creds.Devices) == 0 {
		fmt.Fprintln(out, i18n.T("cli.status.none"))
		printBinding(out)
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if len(creds.Devices) > 0 {
		fmt.Fprintln(tw, i18n.T("cli.status.devices_header"))
		for _, d := range creds.Devices {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", d.Server, d.Account, d.DeviceID, d.CreatedAt.Local().Format("2006-01-02 15:04"))
		}
		fmt.Fprintln(tw, "\t\t\t")
	}
	fmt.Fprintln(tw, i18n.T("cli.status.logins_header"))
	servers := map[string]bool{}
	var order []string
	for _, l := range creds.Logins {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", l.Server, l.Workspace, l.Member, l.Tool, l.CreatedAt.Local().Format("2006-01-02 15:04"))
		if !servers[l.Server] {
			servers[l.Server] = true
			order = append(order, l.Server)
		}
	}
	for _, d := range creds.Devices {
		if !servers[d.Server] {
			servers[d.Server] = true
			order = append(order, d.Server)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintln(out)
	printBinding(out)
	for _, srv := range order {
		if w := client.VersionWarning(ctx, srv); w != "" {
			fmt.Fprintf(out, "\n%s\n", w)
		}
	}
	return nil
}
