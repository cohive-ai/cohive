package main

import (
	"bufio"
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

// devicePollInterval 是等待浏览器授权时的轮询间隔，deviceAdminWait 是等待管理员批准的最长时间；测试里会调小。
var (
	devicePollInterval = 2 * time.Second
	deviceAdminWait    = 10 * time.Minute
)

// openURL 用系统默认浏览器打开链接；测试里替换掉。
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

// findRepoDir 从 dir 往上找到含 Cohive 配置（cohive 条目）的目录，遇到 .git 所在目录（仓库根）就停。
func findRepoDir(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	for d := abs; ; {
		// 只有配置里真有 cohive 条目才算找到；只有别的 MCP 配置的 .mcp.json 不算，继续往上找
		if t, err := readRepoTarget(d); err != nil || t.Server != "" {
			return d
		}
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return abs
		}
		d = parent
	}
}

// confirmServer 在服务地址来自仓库配置、而本机从没登录过它时，请用户确认。
// 仓库文件谁都能改：恶意地址可以把登录请求转发给真正的服务，骗走确认后的令牌。
func confirmServer(in io.Reader, out io.Writer, srv string) error {
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	for _, l := range creds.Logins {
		if l.Server == srv {
			return nil
		}
	}
	fmt.Fprintf(out, "将登录到：%s\n这个地址来自仓库配置，本机还没有登录过。请确认它是你们团队的协作服务，按回车继续，按 Ctrl+C 放弃。\n", srv)
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return errors.New("没有确认服务地址，已放弃登录。确认地址无误后可以用 --server 明确指定")
	}
	return nil
}

func login(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("server", "", "协作服务地址；在已执行过 cohive init 的仓库里可省略")
	toolsFlag := fs.String("tools", "", "要登录的编程工具，逗号分隔；默认是仓库里配置了的工具，没有时为 claude-code,codex")
	dir := fs.String("dir", ".", "仓库目录，用于读取服务地址")
	open := fs.Bool("open", true, "自动打开浏览器授权页")
	resume := fs.Bool("resume", false, "领取之前等待管理员批准的设备授权")
	force := fs.Bool("force", false, "这台电脑已登录时也重新配对")
	tokenStdin := fs.Bool("token-stdin", false, "备用：从标准输入读取管理员签发的令牌")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *resume {
		return resumeLogin(ctx, out)
	}
	repo, err := readRepoTarget(findRepoDir(*dir))
	if err != nil {
		if *server == "" {
			return err
		}
		repo = repoTarget{} // 已明确指定服务地址，不再理会仓库里互相矛盾的配置
	}
	srv := *server
	if srv == "" {
		srv = repo.Server
	}
	if *tokenStdin {
		if *server == "" && srv != "" {
			return fmt.Errorf("用 --token-stdin 时请用 --server 明确指定服务地址（仓库配置里是 %s），避免把令牌发给仓库里被改过的地址", srv)
		}
		return loginWithToken(ctx, in, out, srv)
	}
	if srv == "" {
		return errors.New("缺少 --server（协作服务地址）。网页“个人中心”里有带地址的完整命令；在已执行过 cohive init 的仓库目录里执行时可以省略")
	}
	if srv, err = client.CheckServer(srv); err != nil {
		return err
	}
	repoReady := len(repo.Tools) > 0 && srv == repo.Server
	var tools []string
	switch {
	case *toolsFlag != "":
		for _, t := range strings.Split(*toolsFlag, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tools = append(tools, t)
			}
		}
	case repoReady:
		tools = repo.Tools
	default:
		tools = knownTools
	}

	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	// 这台电脑已配对过这个服务：缺的工具直接用本机令牌补签，不必再去浏览器授权
	if dev, ok := pairedDevice(creds, srv); ok && !*force {
		var missing []string
		for _, t := range tools {
			if _, has := creds.Find(srv, dev.Workspace, t); !has {
				missing = append(missing, t)
			}
		}
		if len(missing) == 0 {
			fmt.Fprintf(out, "这台电脑已登录：%s 工作空间 %s，成员 %s。要重新配对请加 --force。\n", srv, dev.Workspace, dev.Member)
			return nil
		}
		p, err := client.AddTools(ctx, srv, dev.Token, missing)
		if err != nil {
			return err
		}
		return saveDeviceLogins(out, srv, p, repoReady, repo)
	}
	if *server == "" {
		if err := confirmServer(in, out, srv); err != nil {
			return err
		}
	}

	host, _ := os.Hostname()
	st, err := client.StartDevice(ctx, srv, tools, host)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "请在浏览器里授权这台电脑（%d 分钟内有效）：\n\n  %s\n", st.ExpiresIn/60, st.URL)
	if qr, err := client.QRText(st.URL); err == nil {
		fmt.Fprintf(out, "\n  也可以用手机扫码打开：\n\n%s", qr)
	}
	fmt.Fprintln(out, "\n  没有登录网页的话会先让你登录；只授权你自己刚刚发起的这一次。")
	if *open {
		_ = openURL(st.URL)
	}
	fmt.Fprintln(out, "\n等待授权……（按 Ctrl+C 放弃）")
	old, hadOld := pairedDevice(creds, srv)
	if err := waitDevice(ctx, out, client.Pending{Server: srv, RequestID: st.RequestID, PollSecret: st.PollSecret, CreatedAt: time.Now()},
		repoReady, repo); err != nil {
		return err
	}
	if hadOld {
		revokeReplacedDevice(ctx, out, srv, old.DeviceID)
	}
	return nil
}

// revokeReplacedDevice 在 --force 重新配对成功后，用新令牌吊销这台电脑之前的设备，
// 免得旧设备的令牌继续有效；本机凭据里旧设备的条目一并删掉。失败时只提示到网页上手动吊销。
func revokeReplacedDevice(ctx context.Context, out io.Writer, srv string, oldID int64) {
	creds, err := client.LoadCredentials()
	if err != nil {
		return
	}
	var cur client.Login
	for _, l := range creds.Logins {
		if l.Server == srv && l.DeviceID != 0 && l.DeviceID != oldID && l.CreatedAt.After(cur.CreatedAt) {
			cur = l
		}
	}
	if cur.DeviceID == 0 {
		return
	}
	if err := client.RevokeOwnDevice(ctx, srv, cur.Token, oldID); err != nil {
		fmt.Fprintf(out, "\n注意：这台电脑之前的设备（编号 %d）没能自动吊销：%v。请到网页“个人中心 → 我的设备”吊销它。\n", oldID, err)
		return
	}
	fmt.Fprintf(out, "已吊销这台电脑之前的设备（编号 %d）。\n", oldID)
	var keep []client.Login
	var dropped []string
	for _, l := range creds.Logins {
		if l.Server == srv && l.DeviceID == oldID {
			dropped = append(dropped, l.Tool)
			continue
		}
		keep = append(keep, l)
	}
	if len(dropped) == 0 {
		return
	}
	creds.Logins = keep
	if err := creds.Save(); err != nil {
		fmt.Fprintf(out, "更新本机凭据失败：%v\n", err)
		return
	}
	fmt.Fprintf(out, "旧设备上 %s 的登录随之失效，需要的话执行 cohive login --tools %s 补登（不需再授权）。\n",
		strings.Join(dropped, "、"), strings.Join(dropped, ","))
}

// pairedDevice 找出本机在这个服务上已配对的设备登录（有设备编号的那条）。
func pairedDevice(creds *client.Credentials, srv string) (client.Login, bool) {
	for _, l := range creds.Logins {
		if l.Server == srv && l.DeviceID != 0 {
			return l, true
		}
	}
	return client.Login{}, false
}

// resumeLogin 继续领取之前没领完的设备授权（中途退出、等管理员批准超时、网络中断）。
func resumeLogin(ctx context.Context, out io.Writer) error {
	list, err := client.LoadPendings()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return errors.New("没有等待领取的设备授权，请直接执行 cohive login")
	}
	var last error
	for _, p := range list {
		fmt.Fprintf(out, "继续领取 %s 上的设备授权（申请 %d）……\n", p.Server, p.RequestID)
		if err := waitDevice(ctx, out, p, false, repoTarget{}); err != nil {
			fmt.Fprintf(out, "  %v\n", err)
			last = err
		}
	}
	return last
}

// waitDevice 轮询授权结果。发起后立刻在本机保存进度，到终态（领到、拒绝、过期）才删除；
// 中途 Ctrl+C、合盖、网络中断或等管理员超时，之后都能 cohive login --resume 接着领。
func waitDevice(ctx context.Context, out io.Writer, pending client.Pending, repoReady bool, repo repoTarget) error {
	srv, id := pending.Server, pending.RequestID
	if err := client.SavePending(pending); err != nil {
		return err
	}
	done := func() { client.ClearPending(srv, id) }
	failures := 0
	var awaitingSince time.Time
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(devicePollInterval):
		}
		p, err := client.PollDevice(ctx, srv, id, pending.PollSecret)
		if errors.Is(err, client.ErrDeviceGone) {
			done()
			return err
		}
		if err != nil {
			if failures++; failures >= 5 {
				return fmt.Errorf("%w。网络恢复后执行 cohive login --resume 继续领取", err)
			}
			continue
		}
		failures = 0
		switch p.Status {
		case "pending":
			continue
		case "awaiting_admin":
			if awaitingSince.IsZero() {
				awaitingSince = time.Now()
				fmt.Fprintln(out, "你已授权；这不是你的第一台设备，正在等待管理员批准……（可以先按 Ctrl+C 退出，批准后执行 cohive login --resume 领取）")
			}
			if time.Since(awaitingSince) > deviceAdminWait {
				return errors.New("管理员还没有批准。批准后执行 cohive login --resume 领取（24 小时内有效）")
			}
		case "approved":
			done()
			return saveDeviceLogins(out, srv, p, repoReady, repo)
		case "denied":
			done()
			return errors.New("授权被拒绝（你在浏览器里点了拒绝，或管理员没有批准，或设备已被吊销）")
		case "expired":
			done()
			return errors.New("授权已过期，请重新执行 cohive login")
		case "delivered":
			done()
			return errors.New("服务端显示令牌已经交付，但本机没有收到（可能是网络中断）。请重新执行 cohive login --force；多出的设备可在网页“个人中心”吊销")
		default:
			return fmt.Errorf("协作服务返回了未知状态 %q", p.Status)
		}
	}
}

func saveDeviceLogins(out io.Writer, srv string, p client.DevicePoll, repoReady bool, repo repoTarget) error {
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	var saved []string
	for _, t := range knownToolsFirst(p.Tokens) {
		creds.Upsert(client.Login{Server: srv, Workspace: p.Workspace, Member: p.Member, Tool: t, Token: p.Tokens[t],
			DeviceID: p.DeviceID, CreatedAt: time.Now()})
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
	switch {
	case repoReady && repo.Workspace != "" && repo.Workspace != p.Workspace:
		fmt.Fprintf(out, "\n注意：这个仓库配置的是工作空间 %s，而你登录的是 %s，编程工具会找不到登录。请确认仓库配置，或在仓库里重新执行 cohive init。\n",
			repo.Workspace, p.Workspace)
	case repoReady:
		fmt.Fprintln(out, "\n这个仓库已经配置好 Cohive：Claude Code 里用 /cohive，Codex 里用 $cohive。")
	default:
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

// openWeb 实现 cohive web：用本机设备令牌换一次性登录链接，在浏览器里免密打开网页。
func openWeb(ctx context.Context, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	server := fs.String("server", "", "协作服务地址；在仓库里或本机只配对过一个服务时可省略")
	workspace := fs.String("workspace", "", "工作空间名；在仓库里或本机只配对过一个工作空间时可省略")
	dir := fs.String("dir", ".", "仓库目录，用于读取服务地址与工作空间")
	open := fs.Bool("open", true, "自动打开浏览器")
	if err := fs.Parse(args); err != nil {
		return err
	}
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	srv, ws := client.NormalizeServer(*server), *workspace
	// 没有明确指定时，优先用仓库配置里的服务与工作空间
	if repo, err := readRepoTarget(findRepoDir(*dir)); err == nil && repo.Server != "" && (srv == "" || srv == repo.Server) {
		srv = repo.Server
		if ws == "" {
			ws = repo.Workspace
		}
	}
	type key struct{ server, workspace string }
	found := map[key]client.Login{}
	for _, l := range creds.Logins {
		if l.DeviceID != 0 && (srv == "" || l.Server == srv) && (ws == "" || l.Workspace == ws) {
			found[key{l.Server, l.Workspace}] = l
		}
	}
	if len(found) == 0 {
		return errors.New("这台电脑还没有配对这个协作服务或工作空间：先执行 cohive login")
	}
	if len(found) > 1 {
		var list []string
		for k := range found {
			list = append(list, fmt.Sprintf("--server %s --workspace %s", k.server, k.workspace))
		}
		sort.Strings(list)
		return fmt.Errorf("这台电脑配对了多个服务或工作空间，请指定其中一个：\n  %s", strings.Join(list, "\n  "))
	}
	var dev client.Login
	for _, l := range found {
		dev = l
	}
	link, err := client.WebLogin(ctx, dev.Server, dev.Token)
	if err != nil {
		return err
	}
	if *open && openURL(link) == nil {
		fmt.Fprintf(out, "已在浏览器打开 Cohive（链接 2 分钟内有效、只能用一次）：\n  %s\n", link)
		return nil
	}
	fmt.Fprintf(out, "请在浏览器打开下面的链接（2 分钟内有效、只能用一次）：\n  %s\n", link)
	return nil
}
