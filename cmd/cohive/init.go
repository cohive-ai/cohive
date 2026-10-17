package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/cohive-ai/cohive/internal/client"
	"github.com/cohive-ai/cohive/internal/skill"
)

var knownTools = []string{"claude-code", "codex"}

// 影响他人工作的工具每次都要用户确认；其余直接放行。
var (
	confirmTools = []string{"cohive_submit_issue", "cohive_accept_issue", "cohive_close_issue"}
	allowTools   = []string{"cohive_inbox", "cohive_get_issue", "cohive_search", "cohive_who_owns",
		"cohive_draft_issue", "cohive_update_issue", "cohive_request_verification"}
)

// lookPath 查找命令；测试里替换掉。
var lookPath = exec.LookPath

const (
	blockBegin = "# >>> cohive（由 cohive init 生成，重新执行会覆盖这一段）"
	blockEnd   = "# <<< cohive"
)

func initProject(ctx context.Context, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dir := fs.String("dir", ".", "项目目录")
	server := fs.String("server", "", "协作服务地址；本机只配对过一个时可省略")
	workspace := fs.String("workspace", "", "工作空间名；你只在一个工作空间时可省略")
	toolsFlag := fs.String("tools", "", "要配置的编程工具，逗号分隔：claude-code,codex；默认是已登录的工具")
	command := fs.String("command", "cohive", "编程工具启动转发器时使用的命令，cohive 不在 PATH 里时改成绝对路径")
	noHooks := fs.Bool("no-hooks", false, "不写会话钩子（有新动态时在会话里提醒），并移除以前写的")
	if err := fs.Parse(args); err != nil {
		return err
	}
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	srv, ws, err := pickTarget(ctx, creds, *server, *workspace)
	if err != nil {
		return err
	}
	dev, hasDev := creds.Device(srv)

	var tools []string
	if *toolsFlag == "" {
		// 默认：这台电脑在这个服务上登录过的工具（新加入的工作空间还没有令牌，按其他工作空间的算）
		for _, t := range knownTools {
			for _, l := range creds.Logins {
				if l.Server == srv && l.Tool == t && (hasDev || l.Workspace == ws) {
					tools = append(tools, t)
					break
				}
			}
		}
		if len(tools) == 0 {
			return fmt.Errorf("工作空间 %s 还没有登录 claude-code 或 codex：先执行 cohive login", ws)
		}
	} else {
		for _, t := range strings.Split(*toolsFlag, ",") {
			t = strings.TrimSpace(t)
			if t != "claude-code" && t != "codex" {
				return fmt.Errorf("--tools 只支持 claude-code、codex，收到 %q", t)
			}
			tools = append(tools, t)
		}
	}

	proxyArgs := func(tool string) []string {
		return []string{"mcp-proxy", "--server", srv, "--workspace", ws, "--tool", tool}
	}
	// 会话钩子的命令；为空表示不写（并移除以前写的）
	hookCmd := func(tool string) string {
		if *noHooks {
			return ""
		}
		return shellCommand(append([]string{*command, "hook"}, proxyArgs(tool)[1:]...))
	}
	var written []string
	for _, t := range tools {
		switch t {
		case "claude-code":
			if err := initClaude(*dir, *command, proxyArgs(t), hookCmd(t)); err != nil {
				return err
			}
			written = append(written, "Claude Code：.mcp.json、.claude/settings.json、.claude/skills/cohive/SKILL.md")
		case "codex":
			if err := initCodex(*dir, *command, proxyArgs(t), hookCmd(t)); err != nil {
				return err
			}
			written = append(written, "Codex：.codex/config.toml、.agents/skills/cohive/SKILL.md")
		}
	}

	fmt.Fprintf(out, "已为工作空间 %s（%s）写入配置：\n", ws, srv)
	for _, w := range written {
		fmt.Fprintf(out, "  %s\n", w)
	}
	fmt.Fprintln(out, "\n这些文件不含令牌，可以提交进仓库，团队成员共用。")
	if !*noHooks {
		fmt.Fprintln(out, "\n会话内提醒：已写入会话开始、提交提示词两个钩子。有与你有关的新动态时，会在你下一次发消息时插入一行提醒；即时通知仍看 Telegram 群。")
		for _, t := range tools {
			switch t {
			case "claude-code":
				fmt.Fprintln(out, "  Claude Code：第一次在这个文件夹打开时要选“信任此文件夹”，钩子与 MCP 服务才会生效。")
			case "codex":
				fmt.Fprintln(out, "  Codex：要先信任 cohive 的两条钩子（SessionStart、UserPromptSubmit），否则不会提醒：在项目目录执行一次命令行版 codex，"+
					"启动时提示“Hooks need review”，选“Trust all and continue”。只用桌面版也要这样做一次（桌面版里没有信任钩子的入口，信任后桌面版随之生效）。"+
					"每台电脑、每个克隆目录各要信任一次；换服务地址或工作空间后要重新信任。"+
					"在 git 工作树里 Codex 读的是主检出目录的配置，请把这些文件提交。")
			}
		}
		fmt.Fprintf(out, "  没有提醒时可执行 %s 排查。\n", shellCommand(append([]string{*command, "hook", "--check"}, proxyArgs(tools[0])[1:]...)))
	}
	// 编程工具按配置里的命令启动转发器：命令不在 PATH 里时它会找不到 cohive
	if _, err := lookPath(*command); err != nil {
		fmt.Fprintf(out, "\n注意：当前 PATH 里找不到 %q，编程工具启动时会找不到转发器。"+
			"请把 cohive 所在目录加入 PATH（安装脚本默认装在 ~/.local/bin），并从终端重新启动编程工具。\n", *command)
	}
	// 有设备密钥时顺手补签这个工作空间缺的令牌（新加入的工作空间不必重新配对）
	var missing []string
	for _, t := range tools {
		if _, ok := creds.Find(srv, ws, t); !ok {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 && hasDev {
		wt, err := client.IssueTokens(ctx, srv, dev.Secret, ws, missing)
		if err != nil {
			return err
		}
		for _, t := range knownToolsFirst(wt.Tokens) {
			creds.Upsert(client.Login{Server: srv, Workspace: ws, Member: dev.Account, Tool: t, Token: wt.Tokens[t], DeviceID: dev.DeviceID})
		}
		if err := creds.Save(); err != nil {
			return err
		}
		fmt.Fprintf(out, "已为工作空间 %s 签发 %s 的令牌。\n", ws, strings.Join(missing, "、"))
		missing = nil
	}
	for _, t := range missing {
		fmt.Fprintf(out, "\n注意：%s 还没有登录，执行：\n  cohive login --tools %s\n", t, t)
	}
	fmt.Fprintln(out, "\n下一步：")
	fmt.Fprintln(out, "  1. 在 Claude Code、Codex 里打开这个项目，第一次会询问是否信任项目、是否启用 cohive 这个 MCP 服务，都选同意（Codex 的钩子要在项目目录执行一次命令行版 codex 信任，见上）。")
	fmt.Fprintln(out, "  2. Claude Code 里用 /cohive，Codex 里用 $cohive，查看和处理分给你的问题单。")
	return nil
}

// pickTarget 决定配置哪个服务与工作空间：显式指定优先。服务：本机必须只配对过一个；
// 工作空间：有设备密钥时按服务端返回的“我所在的工作空间”，只有一个时可省略；旧版配对按本机登录过的。
func pickTarget(ctx context.Context, creds *client.Credentials, server, workspace string) (string, string, error) {
	servers := map[string]bool{}
	for _, d := range creds.Devices {
		servers[d.Server] = true
	}
	for _, l := range creds.Logins {
		servers[l.Server] = true
	}
	if len(servers) == 0 {
		return "", "", errors.New("本机还没有登录：先执行 cohive login --server <协作服务地址>")
	}
	srv := client.NormalizeServer(server)
	if srv == "" {
		if len(servers) > 1 {
			var list []string
			for s := range servers {
				list = append(list, "--server "+s)
			}
			sort.Strings(list)
			return "", "", fmt.Errorf("本机配对了多个协作服务，请指定其中一个：\n  %s", strings.Join(list, "\n  "))
		}
		for s := range servers {
			srv = s
		}
	} else if !servers[srv] {
		return "", "", fmt.Errorf("本机还没有登录 %s：先执行 cohive login --server %s", srv, srv)
	}
	var names []string
	dev, hasDev := creds.Device(srv)
	if hasDev {
		wss, err := client.DeviceWorkspaces(ctx, srv, dev.Secret)
		if err != nil {
			return "", "", err
		}
		for _, w := range wss {
			names = append(names, w.Name)
		}
	} else {
		seen := map[string]bool{}
		for _, l := range creds.Logins {
			if l.Server == srv && !seen[l.Workspace] {
				seen[l.Workspace] = true
				names = append(names, l.Workspace)
			}
		}
	}
	if workspace != "" {
		for _, n := range names {
			if n == workspace {
				return srv, workspace, nil
			}
		}
		// 只有设备密钥能向服务端确认“我在哪些工作空间”；没有它时本机只知道登录过的工作空间，
		// 不能断定“不是成员”，先说清楚是这台电脑的配对方式不够用
		if _, legacy := creds.LegacyDevice(srv); legacy {
			return "", "", fmt.Errorf("这台电脑是旧版配对（没有设备密钥），不能为工作空间 %s 签发令牌：请执行 cohive login --server %s --force 重新配对（需要管理员批准），再执行 cohive init", workspace, srv)
		}
		if !hasDev {
			return "", "", fmt.Errorf("本机没有工作空间 %s 的登录（只有管理员手工签发的令牌）：请执行 cohive login --server %s 配对这台电脑，再执行 cohive init", workspace, srv)
		}
		return "", "", fmt.Errorf("你还不是工作空间 %s 的成员：请联系它的管理员，加入后再执行 cohive init", workspace)
	}
	switch len(names) {
	case 0:
		return "", "", errors.New("你还没有加入任何工作空间：请联系工作空间管理员把你加进去")
	case 1:
		return srv, names[0], nil
	}
	sort.Strings(names)
	return "", "", fmt.Errorf("你在多个工作空间里，请用 --workspace 指定这个项目属于哪一个：\n  %s", strings.Join(names, "\n  "))
}

// checkNoSymlinks 确认 rel 路径上的每一级（从项目目录往下）都不是符号链接。
// 防止别人仓库里预先放好的链接让 init 写到项目之外（例如 ~/.zshrc）。
func checkNoSymlinks(dir, rel string) error {
	p := dir
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s 是符号链接，cohive init 不会跟随它写入（可能指向项目之外），请检查后删除再执行", p)
		}
	}
	return nil
}

// readJSONObject 读取 JSON 对象；数字保持原样（不转成浮点），文件不存在时返回空对象。
func readJSONObject(path string) (map[string]any, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]any{}
	if len(strings.TrimSpace(string(b))) == 0 {
		return m, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("%s 不是有效的 JSON 对象，请先修正：%v", path, err)
	}
	return m, nil
}

// writeJSON 写回 JSON：不转义 &、<、> 这类字符，保持用户原有内容可读。
func writeJSON(path string, v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return writeFile(path, buf.Bytes())
}

// writeFile 先写同目录下的临时文件再改名，中途失败不会损坏原文件。
func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".cohive-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// childObject 取出（或创建）子对象；已有值不是对象时报错，不覆盖用户的内容。
func childObject(m map[string]any, key, path string) (map[string]any, error) {
	switch c := m[key].(type) {
	case map[string]any:
		return c, nil
	case nil:
		if _, exists := m[key]; exists {
			return nil, fmt.Errorf("%s 是 null，应为对象，请先修正", path)
		}
		c2 := map[string]any{}
		m[key] = c2
		return c2, nil
	default:
		return nil, fmt.Errorf("%s 应为对象，实际是 %T，请先修正", path, c)
	}
}

// addStrings 往字符串数组里追加缺少的项，保持原有顺序；已有值不是数组时报错。
func addStrings(m map[string]any, key, path string, items ...string) error {
	var list []any
	if v, exists := m[key]; exists {
		existing, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s 应为数组，实际是 %T，请先修正", path, v)
		}
		list = existing
	}
	have := map[string]bool{}
	for _, v := range list {
		if s, ok := v.(string); ok {
			have[s] = true
		}
	}
	for _, it := range items {
		if !have[it] {
			list = append(list, it)
			have[it] = true
		}
	}
	m[key] = list
	return nil
}

func initClaude(dir, command string, args []string, hookCmd string) error {
	for _, rel := range []string{".mcp.json", ".claude/settings.json", ".claude/skills/cohive/SKILL.md"} {
		if err := checkNoSymlinks(dir, rel); err != nil {
			return err
		}
	}
	mcpPath := filepath.Join(dir, ".mcp.json")
	mcp, err := readJSONObject(mcpPath)
	if err != nil {
		return err
	}
	servers, err := childObject(mcp, "mcpServers", ".mcp.json 的 mcpServers")
	if err != nil {
		return err
	}
	argv := make([]any, len(args))
	for i, a := range args {
		argv[i] = a
	}
	servers["cohive"] = map[string]any{"type": "stdio", "command": command, "args": argv}

	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	settings, err := readJSONObject(settingsPath)
	if err != nil {
		return err
	}
	perms, err := childObject(settings, "permissions", ".claude/settings.json 的 permissions")
	if err != nil {
		return err
	}
	prefixed := func(names []string) []string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = "mcp__cohive__" + n
		}
		return out
	}
	if err := addStrings(perms, "allow", ".claude/settings.json 的 permissions.allow", prefixed(allowTools)...); err != nil {
		return err
	}
	if err := addStrings(perms, "ask", ".claude/settings.json 的 permissions.ask", prefixed(confirmTools)...); err != nil {
		return err
	}
	if err := addStrings(settings, "enabledMcpjsonServers", ".claude/settings.json 的 enabledMcpjsonServers", "cohive"); err != nil {
		return err
	}
	if err := setClaudeHooks(settings, hookCmd); err != nil {
		return err
	}
	// 全部校验通过后再写，避免只写了一半
	if err := writeJSON(mcpPath, mcp); err != nil {
		return err
	}
	if err := writeJSON(settingsPath, settings); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, ".claude", "skills", "cohive", "SKILL.md"), skill.Cohive)
}

func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '"':
			b.WriteString(`\"`)
		case r == '\\':
			b.WriteString(`\\`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\u%04X`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func codexBlock(command string, args []string, hookCmd string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = tomlString(a)
	}
	var b strings.Builder
	b.WriteString(blockBegin + "\n")
	b.WriteString("[mcp_servers.cohive]\n")
	fmt.Fprintf(&b, "command = %s\n", tomlString(command))
	fmt.Fprintf(&b, "args = [%s]\n", strings.Join(quoted, ", "))
	tools := append(append([]string{}, confirmTools...), allowTools...)
	sort.Strings(tools)
	confirm := map[string]bool{}
	for _, t := range confirmTools {
		confirm[t] = true
	}
	for _, t := range tools {
		mode := "approve"
		if confirm[t] {
			mode = "prompt" // 影响他人工作，每次都要用户确认
		}
		fmt.Fprintf(&b, "\n[mcp_servers.cohive.tools.%s]\napproval_mode = %q\n", t, mode)
	}
	if hookCmd != "" {
		// 会话钩子：每次生成得一模一样，重复执行 init 不会让 /hooks 里的信任失效
		for _, ev := range hookEvents {
			fmt.Fprintf(&b, "\n[[hooks.%s]]\n[[hooks.%s.hooks]]\ntype = \"command\"\ncommand = %s\ntimeout = %d\n", ev, ev, tomlString(hookCmd), hookTimeout)
		}
	}
	b.WriteString(blockEnd + "\n")
	return b.String()
}

// hasCohiveServer 用 TOML 解析判断配置里是否已有 mcp_servers.cohive（任何写法）。
func hasCohiveServer(path, text string) (bool, error) {
	var cfg map[string]any
	if _, err := toml.Decode(text, &cfg); err != nil {
		return false, fmt.Errorf("%s 不是合法的 TOML，请先修正：%v", path, err)
	}
	servers, _ := cfg["mcp_servers"].(map[string]any)
	_, ok := servers["cohive"]
	return ok, nil
}

func initCodex(dir, command string, args []string, hookCmd string) error {
	for _, rel := range []string{".codex/config.toml", ".agents/skills/cohive/SKILL.md"} {
		if err := checkNoSymlinks(dir, rel); err != nil {
			return err
		}
	}
	path := filepath.Join(dir, ".codex", "config.toml")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text := string(b)

	// 去掉 init 自己管理的那一段，剩下的就是用户自己的配置
	rest, blockAt := text, -1
	if start := strings.Index(text, blockBegin); start >= 0 {
		end := strings.Index(text[start:], blockEnd)
		if end < 0 {
			return fmt.Errorf("%s 里的 cohive 配置块缺少结束标记 %q，请手工修正", path, blockEnd)
		}
		end = start + end + len(blockEnd)
		if end < len(text) && text[end] == '\n' {
			end++
		}
		rest, blockAt = text[:start]+text[end:], start
	}
	if exists, err := hasCohiveServer(path, rest); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("%s 里已经有手写的 mcp_servers.cohive 配置，请先删除它，再执行 cohive init", path)
	}

	block := codexBlock(command, args, hookCmd)
	var next string
	if blockAt >= 0 {
		next = rest[:blockAt] + block + rest[blockAt:]
	} else {
		next = rest
		if next != "" && !strings.HasSuffix(next, "\n") {
			next += "\n"
		}
		if next != "" {
			next += "\n"
		}
		next += block
	}
	if _, err := hasCohiveServer(path, next); err != nil {
		return fmt.Errorf("生成的 Codex 配置无法解析，已放弃写入：%v", err)
	}
	if err := writeFile(path, []byte(next)); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, ".agents", "skills", "cohive", "SKILL.md"), skill.Cohive)
}

// 会话钩子：两家都用会话开始、提交提示词两个事件；外层超时兜底（cohive hook 自己 2 秒内结束）。
var hookEvents = []string{"SessionStart", "UserPromptSubmit"}

const hookTimeout = 5

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

// shellCommand 把参数拼成一条 shell 命令，必要时加单引号（两家都按 shell 执行钩子命令）。
func shellCommand(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if shellSafe.MatchString(a) {
			parts[i] = a
		} else {
			parts[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(parts, " ")
}

// isCohiveHook 判断一条钩子命令是不是 cohive init 写的：第一个词是 cohive（或其路径），第二个词是 hook。
func isCohiveHook(cmd string) bool {
	f := shellWords(cmd)
	if len(f) < 2 || f[1] != "hook" {
		return false
	}
	return filepath.Base(f[0]) == "cohive"
}

// shellWords 按 shellCommand 的写法拆回参数：空白分隔，单引号内原样，引号外的反斜杠转义下一个字符。
func shellWords(s string) []string {
	var out []string
	var cur strings.Builder
	inWord, quoted, escaped := false, false, false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case quoted:
			if r == '\'' {
				quoted = false
			} else {
				cur.WriteRune(r)
			}
		case r == '\'':
			quoted, inWord = true, true
		case r == '\\':
			escaped, inWord = true, true
		case r == ' ' || r == '\t' || r == '\n':
			if inWord {
				out = append(out, cur.String())
				cur.Reset()
				inWord = false
			}
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if inWord {
		out = append(out, cur.String())
	}
	return out
}

// setClaudeHooks 在 .claude/settings.json 的 hooks 里为两个事件各放一组 cohive 钩子：
// 先去掉以前写的那组（只含一条 cohive hook 命令的组），用户自己的钩子与顺序不动；hookCmd 为空时只移除。
func setClaudeHooks(settings map[string]any, hookCmd string) error {
	_, had := settings["hooks"]
	if !had && hookCmd == "" {
		return nil
	}
	hooks, err := childObject(settings, "hooks", ".claude/settings.json 的 hooks")
	if err != nil {
		return err
	}
	for _, ev := range hookEvents {
		path := ".claude/settings.json 的 hooks." + ev
		var groups []any
		if v, exists := hooks[ev]; exists {
			list, ok := v.([]any)
			if !ok {
				return fmt.Errorf("%s 应为数组，实际是 %T，请先修正", path, v)
			}
			groups = list
		}
		kept := make([]any, 0, len(groups)+1)
		for _, g := range groups {
			if !isCohiveGroup(g) {
				kept = append(kept, g)
			}
		}
		if hookCmd != "" {
			kept = append(kept, map[string]any{"hooks": []any{
				map[string]any{"type": "command", "command": hookCmd, "timeout": json.Number(fmt.Sprint(hookTimeout))},
			}})
		}
		if len(kept) == 0 {
			delete(hooks, ev)
		} else {
			hooks[ev] = kept
		}
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	}
	return nil
}

func isCohiveGroup(g any) bool {
	m, ok := g.(map[string]any)
	if !ok {
		return false
	}
	list, ok := m["hooks"].([]any)
	if !ok || len(list) != 1 {
		return false
	}
	h, ok := list[0].(map[string]any)
	if !ok {
		return false
	}
	cmd, _ := h["command"].(string)
	return isCohiveHook(cmd)
}
