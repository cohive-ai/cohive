package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/cohive-ai/cohive/internal/client"
)

// devicePollInterval 是等待机器人确认时的轮询间隔，测试里会调小。
var devicePollInterval = 2 * time.Second

// openURL 用系统默认方式打开链接（t.me 链接会跳到 Telegram）；测试里替换掉。
var openURL = func(link string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", link).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", link).Start()
	default:
		return exec.Command("xdg-open", link).Start()
	}
}

// repoTarget 是仓库里 cohive init 写下的配置：服务地址、工作空间和配置了的工具。
type repoTarget struct {
	Server    string
	Workspace string
	Tools     []string
}

// argValue 取出参数列表里 --name 的值（支持 --name 值 与 --name=值）。
func argValue(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v
		}
	}
	return ""
}

// readRepoTarget 从 .mcp.json 与 .codex/config.toml 里的 cohive 转发器参数读出服务地址与工作空间。
// 文件不存在或无法解析时当作没有配置；两份配置指向不同服务时报错。
func readRepoTarget(dir string) (repoTarget, error) {
	var t repoTarget
	add := func(tool string, args []string) error {
		srv, ws := client.NormalizeServer(argValue(args, "--server")), argValue(args, "--workspace")
		if srv == "" {
			return nil
		}
		if t.Server != "" && (t.Server != srv || t.Workspace != ws) {
			return fmt.Errorf("仓库里 .mcp.json 与 .codex/config.toml 指向的协作服务或工作空间不一致（%s %s 与 %s %s），请用 --server、--workspace 指定，或重新执行 cohive init",
				t.Server, t.Workspace, srv, ws)
		}
		t.Server, t.Workspace = srv, ws
		t.Tools = append(t.Tools, tool)
		return nil
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".mcp.json")); err == nil {
		var cfg struct {
			Servers map[string]struct {
				Args []string `json:"args"`
			} `json:"mcpServers"`
		}
		if json.Unmarshal(b, &cfg) == nil {
			if s, ok := cfg.Servers["cohive"]; ok {
				if err := add("claude-code", s.Args); err != nil {
					return t, err
				}
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(dir, ".codex", "config.toml")); err == nil {
		var cfg struct {
			Servers map[string]struct {
				Args []string `toml:"args"`
			} `toml:"mcp_servers"`
		}
		if _, err := toml.Decode(string(b), &cfg); err == nil {
			if s, ok := cfg.Servers["cohive"]; ok {
				if err := add("codex", s.Args); err != nil {
					return t, err
				}
			}
		}
	}
	return t, nil
}

func login(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("server", "", "协作服务地址；在已执行过 cohive init 的仓库里可省略")
	workspace := fs.String("workspace", "", "工作空间名；在已执行过 cohive init 的仓库里可省略")
	toolsFlag := fs.String("tools", "", "要登录的编程工具，逗号分隔；默认是仓库里配置了的工具，没有时为 claude-code,codex")
	dir := fs.String("dir", ".", "仓库目录，用于读取服务地址")
	open := fs.Bool("open", true, "自动打开 Telegram 确认链接")
	code := fs.String("code", "", "备用：私聊机器人 /login 得到的登录码")
	tool := fs.String("tool", "", "与 --code 一起使用：这个令牌给哪个编程工具用")
	tokenStdin := fs.Bool("token-stdin", false, "备用：从标准输入读取管理员签发的令牌")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repo, err := readRepoTarget(*dir)
	if err != nil {
		return err
	}
	srv := *server
	if srv == "" {
		srv = repo.Server
	}
	if *tokenStdin {
		return loginWithToken(ctx, in, out, srv)
	}
	if srv == "" {
		return errors.New("缺少 --server（协作服务地址）。在已执行过 cohive init 的仓库目录里执行时可以省略")
	}
	srv, err = client.CheckServer(srv)
	if err != nil {
		return err
	}
	if *code != "" || *tool != "" {
		return loginWithCode(ctx, out, srv, *code, *tool)
	}

	ws := *workspace
	if ws == "" && srv == repo.Server {
		ws = repo.Workspace
	}
	var tools []string
	switch {
	case *toolsFlag != "":
		for _, t := range strings.Split(*toolsFlag, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tools = append(tools, t)
			}
		}
	case len(repo.Tools) > 0 && srv == repo.Server:
		tools = repo.Tools
	default:
		tools = knownTools
	}
	return loginWithDevice(ctx, out, srv, ws, tools, *open, len(repo.Tools) > 0 && srv == repo.Server)
}

// loginWithDevice 走设备授权：终端显示链接、二维码与数字，成员在机器人私聊里选中这个数字即完成登录。
func loginWithDevice(ctx context.Context, out io.Writer, srv, ws string, tools []string, open, repoReady bool) error {
	host, _ := os.Hostname()
	st, err := client.StartDevice(ctx, srv, tools, host, ws)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "请在 Telegram 里确认登录（%d 分钟内有效）：\n\n  打开链接：%s\n", st.ExpiresIn/60, st.Link)
	if qr, err := client.QRText(st.Link); err == nil {
		fmt.Fprintf(out, "  或用手机扫码：\n\n%s\n", qr)
	}
	fmt.Fprintf(out, "  在机器人私聊里点选这个数字：%d\n\n", st.MatchNumber)
	if open {
		_ = openURL(st.Link)
	}
	fmt.Fprintln(out, "等待确认……（按 Ctrl+C 放弃）")

	deadline := time.Now().Add(time.Duration(st.ExpiresIn)*time.Second + time.Minute)
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(devicePollInterval):
		}
		if time.Now().After(deadline) {
			return errors.New("等待超时，请重新执行 cohive login")
		}
		p, err := client.PollDevice(ctx, srv, st)
		if errors.Is(err, client.ErrDeviceGone) {
			return err
		}
		if err != nil {
			if failures++; failures >= 5 {
				return err
			}
			continue
		}
		failures = 0
		switch p.Status {
		case "pending":
			continue
		case "approved":
			return saveDeviceLogins(out, srv, p, repoReady)
		case "denied":
			return errors.New("登录没有通过：在机器人里点了取消、选错了数字，或链接被别的账号打开过。请重新执行 cohive login")
		case "expired":
			return errors.New("登录请求已过期，请重新执行 cohive login")
		case "delivered":
			return errors.New("这次登录的令牌已经被领取过，请重新执行 cohive login")
		default:
			return fmt.Errorf("协作服务返回了未知状态 %q", p.Status)
		}
	}
}

func saveDeviceLogins(out io.Writer, srv string, p client.DevicePoll, repoReady bool) error {
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	var saved []string
	for _, t := range knownToolsFirst(p.Tokens) {
		creds.Upsert(client.Login{Server: srv, Workspace: p.Workspace, Member: p.Member, Tool: t, Token: p.Tokens[t], CreatedAt: time.Now()})
		saved = append(saved, t)
	}
	if len(saved) == 0 {
		return errors.New("协作服务没有返回令牌，请重新执行 cohive login")
	}
	if err := creds.Save(); err != nil {
		return err
	}
	fmt.Fprintf(out, "\n已登录：工作空间 %s，成员 %s，工具 %s\n令牌已保存到 %s（仅本人可读）\n",
		p.Workspace, p.Member, strings.Join(saved, "、"), client.CredentialsPath())
	if repoReady {
		fmt.Fprintln(out, "\n这个仓库已经配置好 Cohive：Claude Code 里用 /cohive，Codex 里用 $cohive。")
	} else {
		fmt.Fprintln(out, "\n下一步：在项目目录执行 cohive init")
	}
	return nil
}

// knownToolsFirst 按 claude-code、codex 在前的固定顺序列出令牌对应的工具。
func knownToolsFirst(tokens map[string]string) []string {
	var out []string
	for _, t := range knownTools {
		if tokens[t] != "" {
			out = append(out, t)
		}
	}
	var rest []string
	for t, tok := range tokens {
		if tok != "" && t != "claude-code" && t != "codex" {
			rest = append(rest, t)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}
