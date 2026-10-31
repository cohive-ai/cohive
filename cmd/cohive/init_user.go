package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cohive-ai/cohive/internal/client"
	"github.com/cohive-ai/cohive/internal/i18n"
)

// 用户级配置：MCP、确认规则、钩子、技能装在用户主目录下的各工具配置里，命令行不带服务地址与工作空间，
// 运行时由转发器和钩子按所在目录识别（本机绑定表）。同一台电脑上所有项目、所有工作树共用这一份。

// userExtraAllow 是只在用户级装的、本地回应的工具：没绑定项目里 cohive_status 不用每次确认。
var userExtraAllow = []string{"cohive_status"}

// claudeCLI 调用 claude 命令（Claude Code 的用户级 MCP 只能经它写进 ~/.claude.json）；测试里替换掉。
var claudeCLI = func(args ...string) error {
	path, err := exec.LookPath("claude")
	if err != nil {
		return err
	}
	if out, err := exec.Command(path, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func userHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", i18n.Errorf("cli.client.no_home_dir")
	}
	return home, nil
}

// opencodeUserDir 是 opencode 的全局配置目录：XDG_CONFIG_HOME 优先，否则 ~/.config/opencode（macOS 上也是）。
func opencodeUserDir(home string) string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "opencode")
	}
	return filepath.Join(home, ".config", "opencode")
}

// initToolUser 装某个工具的用户级配置，返回给用户看的一行说明。
func initToolUser(out io.Writer, tool, command string, args []string, hookCmd string, hookArgv []string, noHooks bool) (string, error) {
	home, err := userHome()
	if err != nil {
		return "", err
	}
	switch tool {
	case "claude-code":
		settingsPath := filepath.Join(home, ".claude", "settings.json")
		settings, err := readJSONObject(settingsPath)
		if err != nil {
			return "", err
		}
		if err := applyClaudeSettings(settings, hookCmd, false, userExtraAllow); err != nil {
			return "", err
		}
		if err := writeJSON(settingsPath, settings); err != nil {
			return "", err
		}
		if err := writeSkill(filepath.Join(home, ".claude", "skills", "cohive", "SKILL.md")); err != nil {
			return "", err
		}
		addClaudeUserMCP(out, command, args)
		return i18n.T("cli.init.user_written_claude"), nil
	case "codex":
		if err := initCodexAt(home, command, args, hookCmd, userExtraAllow, false); err != nil {
			return "", err
		}
		return i18n.T("cli.init.user_written_codex"), nil
	case "opencode":
		d := opencodeUserDir(home)
		p := opencodePaths{
			config: filepath.Join(d, opencodeConfig),
			jsonc:  filepath.Join(d, opencodeJSONC),
			plugin: filepath.Join(d, "plugins", "cohive.ts"),
			skill:  filepath.Join(home, sharedSkillPath),
		}
		if err := initOpencodeAt(p, command, args, hookArgv, userExtraAllow); err != nil {
			return "", err
		}
		if noHooks {
			return i18n.T("cli.init.user_written_opencode"), nil
		}
		return i18n.T("cli.init.user_written_opencode_plugin"), nil
	}
	return "", nil
}

// addClaudeUserMCP 经 claude 命令把 cohive 登记为用户级 MCP；已存在的先删再加（add-json 不会覆盖）。
// 失败不终止 init：打印可手工执行的命令。
func addClaudeUserMCP(out io.Writer, command string, args []string) {
	entry, _ := marshalNoEscape(claudeServerEntry(command, args))
	manual := shellCommand([]string{"claude", "mcp", "add-json", "--scope", "user", "cohive", string(entry)})
	_ = claudeCLI("mcp", "remove", "--scope", "user", "cohive")
	err := claudeCLI("mcp", "add-json", "--scope", "user", "cohive", string(entry))
	switch {
	case err == nil:
	case errors.Is(err, exec.ErrNotFound):
		fmt.Fprint(out, i18n.T("cli.init.claude_cli_missing", i18n.D{"Cmd": manual}))
	default:
		fmt.Fprint(out, i18n.T("cli.init.claude_cli_failed", i18n.D{"Err": err.Error(), "Cmd": manual}))
	}
}

// defaultTarget 在命令行没指定服务与工作空间时，按目录给出默认目标：
// 已有的绑定 > 仓库里的 .cohive.toml（init 会再核对本人是否是成员）> 旧的项目级配置 > 同一远程地址的另一个克隆（要用户确认）。
func defaultTarget(in io.Reader, out io.Writer, dir string) (client.Target, bool) {
	r := client.Resolve(dir)
	if r.Source != "" {
		return r.Target, true
	}
	if r.Ignored != nil {
		return *r.Ignored, true
	}
	if t, err := readRepoTarget(findRepoDir(dir)); err == nil && t.Server != "" {
		return client.Target{Server: t.Server, Workspace: t.Workspace}, true
	}
	// 同一仓库的另一个克隆：只提示，用户确认才沿用；远程地址是仓库里的内容，不能悄悄决定连哪个工作空间
	info, ok := client.FindRepo(dir)
	if !ok {
		return client.Target{}, false
	}
	b, _ := client.LoadBindings()
	other, ok := b.SameRemote(info.Key, client.GitRemote(info.Key))
	if !ok {
		return client.Target{}, false
	}
	fmt.Fprint(out, i18n.T("cli.init.reuse_prompt", i18n.D{"Dir": other.Key, "Server": other.Server, "Workspace": other.Workspace}))
	line, _ := bufio.NewReader(in).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return client.Target{Server: other.Server, Workspace: other.Workspace}, true
	}
	return client.Target{}, false
}

// bindKey 返回 dir 的绑定键：git 仓库用公共 git 目录，否则用目录自己的真实路径。
func bindKey(dir string) (key, root string, isGit bool) {
	if info, ok := client.FindRepo(dir); ok {
		return info.Key, info.Root, true
	}
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	return dir, dir, false
}

// bindAndTidy 记下绑定，按需写 .cohive.toml，并清理项目里旧版写的、没提交的配置。
func bindAndTidy(out io.Writer, dir, srv, ws string, tools []string, share, removeTracked bool) error {
	key, root, isGit := bindKey(dir)
	remote := ""
	if isGit {
		remote = client.GitRemote(key)
	}
	err := client.UpdateBindings(func(b *client.Bindings) error {
		b.PruneMissing()
		b.Set(client.Binding{Key: key, Server: srv, Workspace: ws, Remote: remote})
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprint(out, i18n.T("cli.init.bound", i18n.D{"Dir": root, "Server": srv, "Workspace": ws}))
	if share {
		if err := writeSharedFile(out, dir, srv, ws); err != nil {
			return err
		}
	}
	if err := cleanProjectConfig(out, []string{dir, root}, tools, removeTracked); err != nil {
		return err
	}
	commonDir := ""
	if isGit {
		commonDir = key
	}
	stalePathHints(out, root, commonDir)
	return nil
}

// writeSharedFile 在仓库根（不在仓库里就写 dir）生成可提交的 .cohive.toml。
func writeSharedFile(out io.Writer, dir, srv, ws string) error {
	_, root, _ := bindKey(dir)
	path := filepath.Join(root, client.SharedFileName)
	if err := checkNoSymlinks(root, client.SharedFileName); err != nil {
		return err
	}
	body := "# Cohive workspace for this project. Safe to commit: it holds no token.\n" +
		"# It only takes effect on computers that have already logged in to this server and workspace.\n" +
		"server = " + tomlString(srv) + "\nworkspace = " + tomlString(ws) + "\n"
	if err := writeFile(path, []byte(body)); err != nil {
		return err
	}
	fmt.Fprint(out, i18n.T("cli.init.shared_written", i18n.D{"Path": path}))
	return nil
}

// unbind 解除某个目录（所在仓库）的本机绑定。
func unbind(out io.Writer, args []string) error {
	fs := flag.NewFlagSet("unbind", flag.ContinueOnError)
	dir := fs.String("dir", ".", i18n.T("cli.flag.init.dir"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return err
	}
	r := client.Resolve(abs)
	if r.Source == "" {
		fmt.Fprint(out, i18n.T("cli.unbind.none", i18n.D{"Dir": abs}))
		return nil
	}
	if r.Source == client.SourceShared {
		fmt.Fprint(out, i18n.T("cli.unbind.shared", i18n.D{"Dir": abs}))
		return nil
	}
	if err := client.UpdateBindings(func(b *client.Bindings) error { b.Remove(r.Key); return nil }); err != nil {
		return err
	}
	fmt.Fprint(out, i18n.T("cli.unbind.done", i18n.D{"Dir": r.Root, "Server": r.Server, "Workspace": r.Workspace}))
	return nil
}

// printBinding 在 cohive status 末尾说明当前目录的绑定。
func printBinding(out io.Writer) {
	wd, err := os.Getwd()
	if err != nil {
		return
	}
	r := client.ResolveAny(candidateDirs(wd)...)
	switch {
	case r.Source == client.SourceBinding:
		fmt.Fprint(out, i18n.T("cli.status.bound", i18n.D{"Server": r.Server, "Workspace": r.Workspace, "Key": r.Key}))
	case r.Source == client.SourceShared:
		fmt.Fprint(out, i18n.T("cli.status.bound_shared", i18n.D{"Server": r.Server, "Workspace": r.Workspace}))
	default:
		fmt.Fprint(out, unboundMessage(r), "\n")
	}
	if b, err := client.LoadBindings(); err == nil {
		stale := 0
		for _, x := range b.Items {
			if _, err := os.Stat(x.Key); err != nil {
				stale++
			}
		}
		if stale > 0 {
			fmt.Fprint(out, i18n.T("cli.status.stale_bindings", i18n.D{"Count": stale}))
		}
	}
}
