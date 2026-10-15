// cohive 是成员本机使用的命令行：登录、初始化项目、为编程工具转发 MCP 请求。
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"

	"github.com/cohive-ai/cohive/internal/client"
)

const usage = `用法：cohive <命令>

  login --server <地址> --code <登录码> --tool <claude-code|codex>
                     用机器人私聊发来的一次性登录码登录（私聊机器人发 /login 领取）
  login --server <地址> --token-stdin
                     从标准输入读入管理员签发的令牌（没有 Telegram 时的备用方式）
  init [--tools claude-code,codex] [--server <地址>] [--workspace <名称>]
                     在当前项目里写入 Cohive 的 MCP 配置、确认规则与 /cohive 技能
  status             列出本机保存的登录（不显示令牌）
  mcp-proxy --server <地址> --workspace <名称> --tool <工具>
                     供编程工具调用的本机转发器，不需要手动运行

凭据保存在 ~/.config/cohive/credentials.json（仅本人可读），可用 COHIVE_CONFIG_DIR 改变目录。
`

// loginCodeShape 是登录码的大致形状（大小写、空格、横线不敏感），用于在本地提示明显的输入错误。
var loginCodeShape = regexp.MustCompile(`^[A-Za-z0-9]{4}[- ]?[A-Za-z0-9]{4}$`)

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
		return status(out)
	case "init":
		return initProject(out, args[1:])
	case "mcp-proxy":
		return runProxy(ctx, in, out, os.Stderr, args[1:])
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return nil
	}
	fmt.Fprint(out, usage)
	return fmt.Errorf("未知命令 %q", args[0])
}

func login(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("server", "", "协作服务地址，例如 https://cohive.example.com")
	code := fs.String("code", "", "私聊机器人 /login 得到的登录码")
	tool := fs.String("tool", "", "这个令牌给哪个编程工具用：claude-code 或 codex")
	tokenStdin := fs.Bool("token-stdin", false, "从标准输入读取管理员签发的令牌（没有 Telegram 时的备用方式）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *tokenStdin {
		return loginWithToken(ctx, in, out, *server)
	}
	switch {
	case *server == "":
		return fmt.Errorf("缺少 --server（协作服务地址）")
	case *code == "":
		return fmt.Errorf("缺少 --code：私聊机器人发 /login 领取登录码")
	case *tool == "":
		return fmt.Errorf("缺少 --tool：claude-code 或 codex")
	}
	srv, err := client.CheckServer(*server)
	if err != nil {
		return err
	}
	if !loginCodeShape.MatchString(strings.TrimSpace(*code)) {
		return fmt.Errorf("登录码 %q 格式不对：应形如 ABCD-EFGH（8 位字母数字），请复制机器人私聊回复里的登录码", *code)
	}
	l, err := client.Exchange(ctx, srv, *code, *tool)
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

func status(out io.Writer) error {
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	if len(creds.Logins) == 0 {
		fmt.Fprintln(out, "还没有登录。私聊机器人发 /login 领取登录码，再执行 cohive login。")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "服务\t工作空间\t成员\t工具\t登录时间")
	for _, l := range creds.Logins {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", l.Server, l.Workspace, l.Member, l.Tool, l.CreatedAt.Local().Format("2006-01-02 15:04"))
	}
	return tw.Flush()
}
