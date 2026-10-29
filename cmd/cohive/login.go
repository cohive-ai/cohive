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
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/cohive-ai/cohive/internal/client"
	"github.com/cohive-ai/cohive/internal/i18n"
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

// readRepoTarget 从 .mcp.json、.codex/config.toml 与 opencode.json 里的 cohive 转发器参数读出服务地址与工作空间。
// 文件不存在或无法解析时当作没有配置；两份配置指向不同服务时报错。
func readRepoTarget(dir string) (repoTarget, error) {
	var t repoTarget
	add := func(tool string, args []string) error {
		srv, ws := client.NormalizeServer(argValue(args, "--server")), argValue(args, "--workspace")
		if srv == "" {
			return nil
		}
		if t.Server != "" && (t.Server != srv || t.Workspace != ws) {
			return i18n.Errorf("cli.login.repo_target_mismatch", i18n.D{
				"Server1": t.Server, "Workspace1": t.Workspace, "Server2": srv, "Workspace2": ws})
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
	if b, err := os.ReadFile(filepath.Join(dir, opencodeConfig)); err == nil {
		var cfg struct {
			MCP map[string]struct {
				Command []string `json:"command"`
			} `json:"mcp"`
		}
		if json.Unmarshal(b, &cfg) == nil {
			if s, ok := cfg.MCP["cohive"]; ok && len(s.Command) > 1 {
				if err := add("opencode", s.Command[1:]); err != nil {
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
	fmt.Fprint(out, i18n.T("cli.login.confirm_server", i18n.D{"Server": srv}))
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && line != "") {
		return i18n.Errorf("cli.login.server_not_confirmed")
	}
	return nil
}

func login(ctx context.Context, in io.Reader, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	server := fs.String("server", "", i18n.T("cli.flag.login.server"))
	toolsFlag := fs.String("tools", "", i18n.T("cli.flag.login.tools"))
	dir := fs.String("dir", ".", i18n.T("cli.flag.repo_dir"))
	open := fs.Bool("open", true, i18n.T("cli.flag.login.open"))
	resume := fs.Bool("resume", false, i18n.T("cli.flag.login.resume"))
	force := fs.Bool("force", false, i18n.T("cli.flag.login.force"))
	tokenStdin := fs.Bool("token-stdin", false, i18n.T("cli.flag.login.token_stdin"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *resume {
		return resumeLogin(ctx, out, *dir)
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
			return i18n.Errorf("cli.login.token_stdin_needs_server", i18n.D{"Server": srv})
		}
		return loginWithToken(ctx, in, out, srv)
	}
	if srv == "" {
		return i18n.Errorf("cli.login.missing_server_hint")
	}
	if srv, err = client.CheckServer(srv); err != nil {
		return err
	}
	if w := client.VersionWarning(ctx, srv); w != "" {
		fmt.Fprintln(out, w)
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
	// 这台电脑已配对过这个服务：缺的令牌直接用设备密钥补签，不必再去浏览器授权
	if dev, ok := creds.Device(srv); ok && !*force {
		return topUpTokens(ctx, out, creds, dev, tools, repoReady, repo)
	}
	if legacy, ok := creds.LegacyDevice(srv); ok && !*force {
		for _, t := range tools {
			if _, has := creds.Find(srv, legacy.Workspace, t); !has {
				return i18n.Errorf("cli.login.legacy_cannot_topup", i18n.D{"Tool": t, "Server": srv})
			}
		}
		fmt.Fprint(out, i18n.T("cli.login.legacy_already", i18n.D{"Server": srv, "Account": legacy.Member}))
		return nil
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
	fmt.Fprint(out, i18n.T("cli.login.authorize_prompt", i18n.D{"Minutes": st.ExpiresIn / 60, "URL": st.URL}))
	if qr, err := client.QRText(st.URL); err == nil {
		if isTerminal(out) {
			qr = client.ColorizeQR(qr)
		}
		fmt.Fprint(out, i18n.T("cli.login.qr_hint"), qr)
	}
	fmt.Fprintln(out, i18n.T("cli.login.authorize_note"))
	if *open {
		_ = openURL(st.URL)
	}
	fmt.Fprintln(out, i18n.T("cli.login.waiting"))
	oldID := int64(0)
	if d, ok := creds.Device(srv); ok {
		oldID = d.DeviceID
	} else if l, ok := creds.LegacyDevice(srv); ok {
		oldID = l.DeviceID
	}
	if err := waitDevice(ctx, out, client.Pending{Server: srv, RequestID: st.RequestID, PollSecret: st.PollSecret, CreatedAt: time.Now()},
		repoReady, repo); err != nil {
		return err
	}
	if oldID != 0 {
		revokeReplacedDevice(ctx, out, srv, oldID)
	}
	return nil
}

// topUpTokens 用设备密钥为本人每个工作空间补签缺的工具令牌（新加入的工作空间也一并补上）。
func topUpTokens(ctx context.Context, out io.Writer, creds *client.Credentials, dev client.Device, tools []string, repoReady bool, repo repoTarget) error {
	wss, err := client.DeviceWorkspaces(ctx, dev.Server, dev.Secret)
	if err != nil {
		return err
	}
	var added []string
	for _, ws := range wss {
		if ws.Archived { // 归档的工作空间只读，服务端也不签发令牌
			continue
		}
		var missing []string
		for _, t := range tools {
			if _, has := creds.Find(dev.Server, ws.Name, t); !has {
				missing = append(missing, t)
			}
		}
		if len(missing) == 0 {
			continue
		}
		wt, err := client.IssueTokens(ctx, dev.Server, dev.Secret, ws.Name, missing)
		if err != nil {
			return err
		}
		for _, t := range knownToolsFirst(wt.Tokens) {
			creds.Upsert(client.Login{Server: dev.Server, Workspace: ws.Name, Member: dev.Account, Tool: t, Token: wt.Tokens[t],
				DeviceID: dev.DeviceID, CreatedAt: time.Now()})
			added = append(added, ws.Name+"/"+t)
		}
	}
	if len(added) > 0 {
		if err := creds.Save(); err != nil {
			return err
		}
		fmt.Fprint(out, i18n.T("cli.login.topped_up", i18n.D{"List": listJoin(added)}))
	}
	fmt.Fprint(out, i18n.T("cli.login.already_logged_in", i18n.D{"Server": dev.Server, "Account": dev.Account, "Workspaces": joinWorkspaces(wss)}))
	warnRepoWorkspace(out, repoReady, repo, wss)
	return nil
}

func joinWorkspaces(wss []client.WorkspaceRef) string {
	if len(wss) == 0 {
		return i18n.T("cli.login.no_workspaces")
	}
	var names []string
	for _, w := range wss {
		names = append(names, w.Name)
	}
	return listJoin(names)
}

// listJoin 用当前语言的列举符号把几项连起来（中文“、”，英文“, ”）。
func listJoin(items []string) string {
	return strings.Join(items, i18n.T("cli.list_sep"))
}

// warnRepoWorkspace 在仓库配置的工作空间不在本人所在的工作空间里时提醒。
func warnRepoWorkspace(out io.Writer, repoReady bool, repo repoTarget, wss []client.WorkspaceRef) {
	if !repoReady || repo.Workspace == "" {
		return
	}
	for _, w := range wss {
		if w.Name == repo.Workspace {
			return
		}
	}
	fmt.Fprint(out, i18n.T("cli.login.warn_repo_workspace", i18n.D{"Workspace": repo.Workspace}))
}

// revokeReplacedDevice 在 --force 重新配对成功后，用新设备密钥吊销这台电脑之前的设备，
// 免得旧设备的令牌继续有效；本机凭据里旧设备的条目一并删掉。失败时只提示到网页上手动吊销。
func revokeReplacedDevice(ctx context.Context, out io.Writer, srv string, oldID int64) {
	creds, err := client.LoadCredentials()
	if err != nil {
		return
	}
	cur, ok := creds.Device(srv)
	if !ok || cur.DeviceID == oldID {
		return
	}
	if err := client.RevokeOwnDevice(ctx, srv, cur.Secret, oldID); err != nil {
		fmt.Fprint(out, i18n.T("cli.login.revoke_failed", i18n.D{"ID": oldID, "Err": i18n.Message(i18n.Default(), err)}))
		return
	}
	fmt.Fprint(out, i18n.T("cli.login.revoked", i18n.D{"ID": oldID}))
	dropped := creds.DropDevice(srv, oldID)
	if len(dropped) == 0 {
		return
	}
	if err := creds.Save(); err != nil {
		fmt.Fprint(out, i18n.T("cli.login.update_creds_failed", i18n.D{"Err": i18n.Message(i18n.Default(), err)}))
		return
	}
	var tools []string
	seen := map[string]bool{}
	for _, d := range dropped {
		_, t, _ := strings.Cut(d, "/")
		if !seen[t] {
			seen[t] = true
			tools = append(tools, t)
		}
	}
	fmt.Fprint(out, i18n.T("cli.login.old_device_dropped", i18n.D{"List": listJoin(dropped), "Tools": strings.Join(tools, ",")}))
}

// resumeLogin 继续领取之前没领完的设备授权（中途退出、等管理员批准超时、网络中断）。
func resumeLogin(ctx context.Context, out io.Writer, dir string) error {
	repo, _ := readRepoTarget(findRepoDir(dir))
	list, err := client.LoadPendings()
	if err != nil {
		return err
	}
	if len(list) == 0 {
		return i18n.Errorf("cli.login.resume_none")
	}
	var last error
	for _, p := range list {
		fmt.Fprint(out, i18n.T("cli.login.resuming", i18n.D{"Server": p.Server, "ID": p.RequestID}))
		// 在已配置好 Cohive 的仓库里续领时，不再提示执行 cohive init
		ready := len(repo.Tools) > 0 && repo.Server == client.NormalizeServer(p.Server)
		if err := waitDevice(ctx, out, p, ready, repo); err != nil {
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
				return i18n.Wrap(err, "cli.login.network_retry")
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
				fmt.Fprintln(out, i18n.T("cli.login.awaiting_admin"))
			}
			if time.Since(awaitingSince) > deviceAdminWait {
				return i18n.Errorf("cli.login.admin_timeout")
			}
		case "approved":
			done()
			return saveDeviceLogins(out, srv, p, repoReady, repo)
		case "denied":
			done()
			return i18n.Errorf("cli.login.denied")
		case "expired":
			done()
			return i18n.Errorf("cli.login.expired")
		case "delivered":
			done()
			return i18n.Errorf("cli.login.delivered")
		default:
			return i18n.Errorf("cli.login.unknown_status", i18n.D{"Status": strconv.Quote(p.Status)})
		}
	}
}

func saveDeviceLogins(out io.Writer, srv string, p client.DevicePoll, repoReady bool, repo repoTarget) error {
	if p.DeviceSecret == "" {
		return i18n.Errorf("cli.login.no_device_secret")
	}
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	creds.SetDevice(client.Device{Server: srv, Account: p.Account, DeviceID: p.DeviceID, Secret: p.DeviceSecret, CreatedAt: time.Now()})
	var lines []string
	var wss []client.WorkspaceRef
	for _, w := range p.Workspaces {
		var saved []string
		for _, t := range knownToolsFirst(w.Tokens) {
			creds.Upsert(client.Login{Server: srv, Workspace: w.Workspace, Member: p.Account, Tool: t, Token: w.Tokens[t],
				DeviceID: p.DeviceID, CreatedAt: time.Now()})
			saved = append(saved, t)
		}
		lines = append(lines, i18n.T("cli.login.workspace_line", i18n.D{"Workspace": w.Workspace, "Tools": listJoin(saved)}))
		wss = append(wss, client.WorkspaceRef{Name: w.Workspace, Role: w.Role})
	}
	if err := creds.Save(); err != nil {
		return err
	}
	fmt.Fprint(out, i18n.T("cli.login.done_header", i18n.D{"Account": p.Account}))
	if len(lines) == 0 {
		fmt.Fprintln(out, i18n.T("cli.login.no_workspace_yet"))
	} else {
		fmt.Fprintln(out, strings.Join(lines, "\n"))
	}
	fmt.Fprint(out, i18n.T("cli.login.creds_saved", i18n.D{"Path": client.CredentialsPath()}))
	switch {
	case repoReady:
		warnRepoWorkspace(out, repoReady, repo, wss)
		fmt.Fprintln(out, i18n.T("cli.login.repo_ready"))
	default:
		fmt.Fprintln(out, i18n.T("cli.login.next_init"))
	}
	return nil
}

// knownToolsFirst 按 knownTools 的固定顺序列出令牌对应的工具，其余的按名字排在后面。
func knownToolsFirst(tokens map[string]string) []string {
	var out []string
	for _, t := range knownTools {
		if tokens[t] != "" {
			out = append(out, t)
		}
	}
	var rest []string
	for t, tok := range tokens {
		if tok != "" && !isKnownTool(t) {
			rest = append(rest, t)
		}
	}
	sort.Strings(rest)
	return append(out, rest...)
}

// openWeb 实现 cohive web：用本机设备密钥换一次性登录链接，在浏览器里免密打开网页。
// 网页登录属于账号，不分工作空间；M5 配对的旧电脑没有设备密钥时，用属于设备的令牌兼容。
func openWeb(ctx context.Context, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	server := fs.String("server", "", i18n.T("cli.flag.web.server"))
	dir := fs.String("dir", ".", i18n.T("cli.flag.repo_dir"))
	open := fs.Bool("open", true, i18n.T("cli.flag.web.open"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	srv := client.NormalizeServer(*server)
	if repo, err := readRepoTarget(findRepoDir(*dir)); err == nil && repo.Server != "" && srv == "" {
		srv = repo.Server
	}
	found := map[string]string{} // 服务地址 → 凭据
	for _, d := range creds.Devices {
		if srv == "" || d.Server == srv {
			found[d.Server] = d.Secret
		}
	}
	for _, l := range creds.Logins {
		if l.DeviceID != 0 && (srv == "" || l.Server == srv) && found[l.Server] == "" {
			found[l.Server] = l.Token
		}
	}
	if len(found) == 0 {
		return i18n.Errorf("cli.web.not_paired")
	}
	if len(found) > 1 {
		var list []string
		for k := range found {
			list = append(list, "--server "+k)
		}
		sort.Strings(list)
		return i18n.Errorf("cli.multiple_servers", i18n.D{"List": strings.Join(list, "\n  ")})
	}
	var target, cred string
	for k, v := range found {
		target, cred = k, v
	}
	link, err := client.WebLogin(ctx, target, cred)
	if err != nil {
		return err
	}
	if *open && openURL(link) == nil {
		fmt.Fprint(out, i18n.T("cli.web.opened", i18n.D{"URL": link}))
		return nil
	}
	fmt.Fprint(out, i18n.T("cli.web.open_link", i18n.D{"URL": link}))
	return nil
}

// isTerminal 判断输出是否为真实终端（而不是文件或管道）。
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
