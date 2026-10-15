package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

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

const (
	blockBegin = "# >>> cohive（由 cohive init 生成，重新执行会覆盖这一段）"
	blockEnd   = "# <<< cohive"
)

func initProject(out io.Writer, args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dir := fs.String("dir", ".", "项目目录")
	server := fs.String("server", "", "协作服务地址；本机只登录过一个时可省略")
	workspace := fs.String("workspace", "", "工作空间名；本机只登录过一个时可省略")
	toolsFlag := fs.String("tools", "", "要配置的编程工具，逗号分隔：claude-code,codex；默认是已登录的工具")
	command := fs.String("command", "cohive", "编程工具启动转发器时使用的命令，cohive 不在 PATH 里时改成绝对路径")
	if err := fs.Parse(args); err != nil {
		return err
	}
	creds, err := client.LoadCredentials()
	if err != nil {
		return err
	}
	srv, ws, err := pickTarget(creds, *server, *workspace)
	if err != nil {
		return err
	}

	var tools []string
	if *toolsFlag == "" {
		for _, t := range knownTools {
			if _, ok := creds.Find(srv, ws, t); ok {
				tools = append(tools, t)
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
	var written []string
	for _, t := range tools {
		switch t {
		case "claude-code":
			if err := initClaude(*dir, *command, proxyArgs(t)); err != nil {
				return err
			}
			written = append(written, "Claude Code：.mcp.json、.claude/settings.json、.claude/skills/cohive/SKILL.md")
		case "codex":
			if err := initCodex(*dir, *command, proxyArgs(t)); err != nil {
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
	for _, t := range tools {
		if _, ok := creds.Find(srv, ws, t); !ok {
			fmt.Fprintf(out, "\n注意：%s 还没有登录，私聊机器人发 /login 领取登录码后执行：\n  cohive login --server %s --code <登录码> --tool %s\n", t, srv, t)
		}
	}
	fmt.Fprintln(out, "\n下一步：")
	fmt.Fprintln(out, "  1. 在 Claude Code、Codex 里打开这个项目，第一次会询问是否信任项目、是否启用 cohive 这个 MCP 服务，都选同意。")
	fmt.Fprintln(out, "  2. Claude Code 里用 /cohive，Codex 里用 $cohive，查看和处理分给你的问题单。")
	return nil
}

// pickTarget 决定配置哪个服务与工作空间：显式指定优先，否则本机必须只有一个。
func pickTarget(creds *client.Credentials, server, workspace string) (string, string, error) {
	if len(creds.Logins) == 0 {
		return "", "", errors.New("本机还没有登录：私聊机器人发 /login 领取登录码，再执行 cohive login")
	}
	type key struct{ s, w string }
	seen := map[key]bool{}
	var matches []key
	for _, l := range creds.Logins {
		k := key{l.Server, l.Workspace}
		if seen[k] {
			continue
		}
		seen[k] = true
		if (server == "" || client.NormalizeServer(server) == l.Server) && (workspace == "" || workspace == l.Workspace) {
			matches = append(matches, k)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0].s, matches[0].w, nil
	case 0:
		return "", "", fmt.Errorf("本机没有 %s 工作空间 %s 的登录，先执行 cohive login", server, workspace)
	}
	var list []string
	for _, k := range matches {
		list = append(list, fmt.Sprintf("--server %s --workspace %s", k.s, k.w))
	}
	return "", "", fmt.Errorf("本机登录了多个工作空间，请指定其中一个：\n  %s", strings.Join(list, "\n  "))
}

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
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s 不是有效的 JSON，请先修正：%v", path, err)
	}
	return m, nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(path, append(b, '\n'))
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func childObject(m map[string]any, key string) map[string]any {
	if c, ok := m[key].(map[string]any); ok {
		return c
	}
	c := map[string]any{}
	m[key] = c
	return c
}

// addStrings 往字符串数组里追加缺少的项，保持原有顺序。
func addStrings(m map[string]any, key string, items ...string) {
	var list []any
	if existing, ok := m[key].([]any); ok {
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
}

func initClaude(dir, command string, args []string) error {
	mcpPath := filepath.Join(dir, ".mcp.json")
	mcp, err := readJSONObject(mcpPath)
	if err != nil {
		return err
	}
	argv := make([]any, len(args))
	for i, a := range args {
		argv[i] = a
	}
	childObject(mcp, "mcpServers")["cohive"] = map[string]any{"type": "stdio", "command": command, "args": argv}
	if err := writeJSON(mcpPath, mcp); err != nil {
		return err
	}

	settingsPath := filepath.Join(dir, ".claude", "settings.json")
	settings, err := readJSONObject(settingsPath)
	if err != nil {
		return err
	}
	perms := childObject(settings, "permissions")
	prefixed := func(names []string) []string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = "mcp__cohive__" + n
		}
		return out
	}
	addStrings(perms, "allow", prefixed(allowTools)...)
	addStrings(perms, "ask", prefixed(confirmTools)...)
	addStrings(settings, "enabledMcpjsonServers", "cohive")
	if err := writeJSON(settingsPath, settings); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, ".claude", "skills", "cohive", "SKILL.md"), skill.Cohive)
}

func tomlString(s string) string {
	b, _ := json.Marshal(s) // JSON 字符串转义与 TOML 基本字符串兼容
	return string(b)
}

func codexBlock(command string, args []string) string {
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
	b.WriteString(blockEnd + "\n")
	return b.String()
}

func initCodex(dir, command string, args []string) error {
	path := filepath.Join(dir, ".codex", "config.toml")
	b, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	text := string(b)
	block := codexBlock(command, args)
	start := strings.Index(text, blockBegin)
	if start >= 0 {
		end := strings.Index(text[start:], blockEnd)
		if end < 0 {
			return fmt.Errorf("%s 里的 cohive 配置块缺少结束标记 %q，请手工修正", path, blockEnd)
		}
		end = start + end + len(blockEnd)
		if end < len(text) && text[end] == '\n' {
			end++
		}
		text = text[:start] + block + text[end:]
	} else {
		if strings.Contains(text, "[mcp_servers.cohive") {
			return fmt.Errorf("%s 里已经有手写的 [mcp_servers.cohive] 配置，请先删除它，再执行 cohive init", path)
		}
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		if text != "" {
			text += "\n"
		}
		text += block
	}
	if err := writeFile(path, []byte(text)); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, ".agents", "skills", "cohive", "SKILL.md"), skill.Cohive)
}
