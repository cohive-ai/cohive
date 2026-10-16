// cohive 是成员本机使用的命令行：登录、初始化项目、为编程工具转发 MCP 请求。
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/cohive-ai/cohive/internal/buildinfo"
	"github.com/cohive-ai/cohive/internal/client"
)

const usage = `用法：cohive <命令>

  login [--server <地址>] [--tools claude-code,codex]
                     配对这台电脑：自动打开浏览器授权页，点“授权”即可（网页“个人中心”里有带地址的完整命令）。
                     在已执行过 cohive init 的仓库里执行时，服务地址与工具从仓库配置读取；
                     已配对的电脑再登录其他工具时直接签发，不必再授权
  login --resume     领取之前等待管理员批准的设备授权
  login --token-stdin --server <地址>
                     备用：从标准输入读入管理员签发的令牌
  web [--server <地址>] [--workspace <名称>]
                     在浏览器里免密打开 Cohive 网页（用这台电脑的配对；在仓库里执行时按仓库配置选择）
  init [--tools claude-code,codex] [--server <地址>] [--workspace <名称>]
                     在当前项目里写入 Cohive 的 MCP 配置、确认规则与 /cohive 技能
  status             列出本机保存的登录（不显示令牌）
  version            显示本机 cohive 的版本
  mcp-proxy --server <地址> --workspace <名称> --tool <工具>
                     供编程工具调用的本机转发器，不需要手动运行

凭据保存在 ~/.config/cohive/credentials.json（仅本人可读），可用 COHIVE_CONFIG_DIR 改变目录。
`

func main() {
	if err := runCLI(context.Background(), os.Stdin, os.Stdout, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func runCLI(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
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
		return initProject(ctx, out, args[1:])
	case "mcp-proxy":
		return runProxy(ctx, in, out, os.Stderr, args[1:])
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return nil
	}
	fmt.Fprint(out, usage)
	return fmt.Errorf("未知命令 %q", args[0])
}

// loginWithToken 保存管理员用 cohive-server token issue 签发的令牌。
// 令牌从标准输入读取，不出现在命令行参数和 shell 历史里；先向服务端确认归属再保存。
func loginWithToken(ctx context.Context, in io.Reader, out io.Writer, server string) error {
	if server == "" {
		return fmt.Errorf("缺少 --server（协作服务地址）")
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
		return fmt.Errorf("没有读到令牌：请把管理员给你的令牌（以 cohive_ 开头）通过标准输入传进来，例如 pbpaste | cohive login --server %s --token-stdin", srv)
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
	fmt.Fprintf(out, "已登录：工作空间 %s，成员 %s，工具 %s\n令牌已保存到 %s（仅本人可读）\n\n下一步：在项目目录执行 cohive init\n",
		l.Workspace, l.Member, l.Tool, client.CredentialsPath())
	return nil
}

func status(ctx context.Context, out io.Writer) error {
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	if len(creds.Logins) == 0 && len(creds.Devices) == 0 {
		fmt.Fprintln(out, "还没有登录。执行 cohive login --server <协作服务地址>，在浏览器里授权即可。")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	if len(creds.Devices) > 0 {
		fmt.Fprintln(tw, "服务\t账号\t设备编号\t配对时间")
		for _, d := range creds.Devices {
			fmt.Fprintf(tw, "%s\t%s\t%d\t%s\n", d.Server, d.Account, d.DeviceID, d.CreatedAt.Local().Format("2006-01-02 15:04"))
		}
		fmt.Fprintln(tw, "\t\t\t")
	}
	fmt.Fprintln(tw, "服务\t工作空间\t账号\t工具\t登录时间")
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
	for _, srv := range order {
		if w := client.VersionWarning(ctx, srv); w != "" {
			fmt.Fprintf(out, "\n%s\n", w)
		}
	}
	return nil
}
