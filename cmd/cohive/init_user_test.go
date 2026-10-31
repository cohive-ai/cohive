package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/cohive-ai/cohive/internal/client"
)

// userEnv 把“用户主目录”和 claude 命令都隔离掉：测试里绝不碰真实的 ~/.claude、~/.codex、~/.config。
func userEnv(t *testing.T) (home string, claudeCalls *[][]string) {
	t.Helper()
	configDir(t)
	home = realTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	calls := &[][]string{}
	old := claudeCLI
	claudeCLI = func(args ...string) error {
		*calls = append(*calls, args)
		return nil
	}
	t.Cleanup(func() { claudeCLI = old })
	return home, calls
}

func runIn(t *testing.T, input string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runCLI(context.Background(), strings.NewReader(input), &out, args)
	return out.String(), err
}

func gitProject(t *testing.T) string {
	t.Helper()
	d := realTemp(t)
	gitIn(t, d, "init", "-q", ".")
	return d
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// 验收 1：一次 init 后，用户级配置齐全且不含服务地址与工作空间；项目目录里什么都不写。
func TestInitUserLevelWritesNoProjectFiles(t *testing.T) {
	home, calls := userEnv(t)
	saveLogins(t, "claude-code", "codex", "opencode")
	proj := gitProject(t)
	out, err := run(t, "init", "--dir", proj)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, rel := range []string{".mcp.json", ".claude", ".codex", ".agents", "opencode.json", ".opencode", client.SharedFileName} {
		if exists(filepath.Join(proj, rel)) {
			t.Fatalf("用户级模式不应往项目里写 %s", rel)
		}
	}

	settings := readFile(t, filepath.Join(home, ".claude", "settings.json"))
	for _, want := range []string{`cohive hook --tool claude-code`, `mcp__cohive__cohive_status`, `mcp__cohive__cohive_submit_issue`} {
		if !strings.Contains(settings, want) {
			t.Fatalf("~/.claude/settings.json 应含 %s：%s", want, settings)
		}
	}
	if strings.Contains(settings, "--server") || strings.Contains(settings, "enabledMcpjsonServers") {
		t.Fatalf("用户级设置不应带服务地址，也不需要启用 .mcp.json：%s", settings)
	}
	if len(*calls) != 2 || (*calls)[0][1] != "remove" || (*calls)[1][1] != "add-json" || (*calls)[1][3] != "user" ||
		!strings.Contains((*calls)[1][5], `"args":["mcp-proxy","--tool","claude-code"]`) {
		t.Fatalf("应先删后加用户级 MCP：%v", *calls)
	}

	var cfg struct {
		Servers map[string]struct {
			Args []string `toml:"args"`
		} `toml:"mcp_servers"`
	}
	if _, err := toml.Decode(readFile(t, filepath.Join(home, ".codex", "config.toml")), &cfg); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cfg.Servers["cohive"].Args, " "); got != "mcp-proxy --tool codex" {
		t.Fatalf("Codex 用户级条目：%q", got)
	}
	codex := readFile(t, filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(codex, `command = "cohive hook --tool codex"`) || !strings.Contains(codex, "[mcp_servers.cohive.tools.cohive_status]") {
		t.Fatalf("Codex 应有不带参数的钩子和 cohive_status：%s", codex)
	}

	oc := readFile(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if !strings.Contains(oc, `"mcp-proxy"`) || strings.Contains(oc, "--server") || !strings.Contains(oc, "cohive_cohive_status") {
		t.Fatalf("opencode 用户级配置：%s", oc)
	}
	plugin := readFile(t, filepath.Join(home, ".config", "opencode", "plugins", "cohive.ts"))
	if !strings.Contains(plugin, `["cohive","hook","--tool","opencode"]`) {
		t.Fatalf("opencode 插件应调用不带参数的钩子：%s", plugin)
	}
	for _, p := range []string{filepath.Join(".claude", "skills", "cohive", "SKILL.md"), filepath.Join(".agents", "skills", "cohive", "SKILL.md")} {
		if !exists(filepath.Join(home, p)) {
			t.Fatalf("缺少用户级技能 %s", p)
		}
	}

	// 绑定
	b, _ := client.LoadBindings()
	key, _, _ := bindKey(proj)
	if got, ok := b.Find(key); !ok || got.Workspace != "payments" || got.Server != srv {
		t.Fatalf("应记下绑定：%+v", b.Items)
	}
	if !strings.Contains(out, "已把") {
		t.Fatalf("应告诉用户绑定了：%s", out)
	}
}

func TestInitUserLevelIdempotent(t *testing.T) {
	home, _ := userEnv(t)
	saveLogins(t, "claude-code", "codex", "opencode")
	proj := gitProject(t)
	files := []string{
		filepath.Join(home, ".claude", "settings.json"),
		filepath.Join(home, ".codex", "config.toml"),
		filepath.Join(home, ".config", "opencode", "opencode.json"),
		filepath.Join(home, ".config", "opencode", "plugins", "cohive.ts"),
	}
	if _, err := run(t, "init", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	first := map[string]string{}
	for _, f := range files {
		first[f] = readFile(t, f)
	}
	if _, err := run(t, "init", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if readFile(t, f) != first[f] {
			t.Fatalf("重复执行 init 应得到完全一样的 %s", f)
		}
	}
}

func TestInitUserLevelKeepsUsersOwnConfig(t *testing.T) {
	home, _ := userEnv(t)
	saveLogins(t, "claude-code", "codex")
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"),
		[]byte(`{"theme":"dark","hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo mine"}]}]}}`), 0o644)
	os.MkdirAll(filepath.Join(home, ".codex"), 0o755)
	os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("model = \"x\"\n"), 0o644)
	if _, err := run(t, "init", "--dir", gitProject(t)); err != nil {
		t.Fatal(err)
	}
	s := readFile(t, filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(s, `"theme": "dark"`) || !strings.Contains(s, "echo mine") || !strings.Contains(s, "cohive hook --tool claude-code") {
		t.Fatalf("用户自己的设置与钩子应保留：%s", s)
	}
	if c := readFile(t, filepath.Join(home, ".codex", "config.toml")); !strings.HasPrefix(c, "model = \"x\"\n") {
		t.Fatalf("Codex 里用户自己的内容应保留：%s", c)
	}
}

func TestInitClaudeCLIMissingPrintsManualCommand(t *testing.T) {
	userEnv(t)
	claudeCLI = func(args ...string) error { return exec.ErrNotFound }
	saveLogins(t, "claude-code")
	out, err := run(t, "init", "--dir", gitProject(t))
	if err != nil {
		t.Fatalf("没有 claude 命令不应让 init 失败：%v", err)
	}
	if !strings.Contains(out, "claude mcp add-json --scope user cohive") || !strings.Contains(out, "没找到 claude 命令") {
		t.Fatalf("应给出可手工执行的命令：%s", out)
	}
}

func TestInitProjectModeDoesNotBindOrTouchHome(t *testing.T) {
	home, calls := userEnv(t)
	saveLogins(t, "claude-code")
	proj := gitProject(t)
	if _, err := run(t, "init", "--project", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(home, ".claude")) || len(*calls) != 0 {
		t.Fatal("--project 不应写用户级配置")
	}
	if b, _ := client.LoadBindings(); len(b.Items) != 0 {
		t.Fatalf("--project 不应记绑定：%+v", b.Items)
	}
	if !exists(filepath.Join(proj, ".mcp.json")) {
		t.Fatal("--project 应写项目级文件")
	}
	if _, err := run(t, "init", "--project", "--remove-project-config", "--dir", proj); err == nil {
		t.Fatal("--project 与 --remove-project-config 互斥")
	}
}

// 迁移：旧版写的、没提交的项目级配置被清掉，用户自己的条目原样保留。
func TestInitMigratesUncommittedProjectConfig(t *testing.T) {
	userEnv(t)
	saveLogins(t, "claude-code", "codex", "opencode")
	proj := gitProject(t)
	if _, err := run(t, "init", "--project", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	// 在旧配置里混进用户自己的内容
	mcp := filepath.Join(proj, ".mcp.json")
	var m map[string]any
	json.Unmarshal([]byte(readFile(t, mcp)), &m)
	m["mcpServers"].(map[string]any)["other"] = map[string]any{"command": "x"}
	b, _ := json.Marshal(m)
	os.WriteFile(mcp, b, 0o644)
	settings := filepath.Join(proj, ".claude", "settings.json")
	var s map[string]any
	json.Unmarshal([]byte(readFile(t, settings)), &s)
	s["theme"] = "dark"
	s["permissions"].(map[string]any)["allow"] = append(s["permissions"].(map[string]any)["allow"].([]any), "Bash(ls)")
	b, _ = json.Marshal(s)
	os.WriteFile(settings, b, 0o644)
	codex := filepath.Join(proj, ".codex", "config.toml")
	os.WriteFile(codex, []byte("model = \"x\"\n\n"+readFile(t, codex)), 0o644)

	out, err := run(t, "init", "--dir", proj)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if !strings.Contains(out, "已移除旧版") {
		t.Fatalf("应说明清理了什么：%s", out)
	}
	mc := readFile(t, mcp)
	if strings.Contains(mc, "cohive") || !strings.Contains(mc, `"other"`) {
		t.Fatalf(".mcp.json 只应去掉 cohive 条目：%s", mc)
	}
	st := readFile(t, settings)
	for _, gone := range []string{"cohive", "enabledMcpjsonServers"} {
		if strings.Contains(st, gone) {
			t.Fatalf("settings.json 里 cohive 的内容应清掉（%s）：%s", gone, st)
		}
	}
	if !strings.Contains(st, `"theme": "dark"`) || !strings.Contains(st, "Bash(ls)") {
		t.Fatalf("用户自己的设置应保留：%s", st)
	}
	if c := readFile(t, codex); strings.Contains(c, "cohive") || strings.TrimSpace(c) != `model = "x"` {
		t.Fatalf("Codex 配置只应剩用户自己的内容：%q", c)
	}
	for _, rel := range []string{".claude/skills", ".agents", "opencode.json", ".opencode"} {
		if exists(filepath.Join(proj, rel)) {
			t.Fatalf("纯 cohive 生成的 %s 应整个删掉", rel)
		}
	}
}

func TestInitMigrationLeavesCommittedFiles(t *testing.T) {
	userEnv(t)
	saveLogins(t, "claude-code", "codex")
	proj := gitProject(t)
	if _, err := run(t, "init", "--project", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	gitIn(t, proj, "add", ".mcp.json", ".claude", ".codex", ".agents")
	gitIn(t, proj, "commit", "-q", "-m", "share")
	before := map[string]string{}
	for _, rel := range []string{".mcp.json", ".claude/settings.json", ".codex/config.toml"} {
		before[rel] = readFile(t, filepath.Join(proj, rel))
	}
	out, err := run(t, "init", "--dir", proj)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for rel, want := range before {
		if readFile(t, filepath.Join(proj, rel)) != want {
			t.Fatalf("已提交的 %s 不应被改动", rel)
		}
	}
	if !strings.Contains(out, "已提交进仓库") || !strings.Contains(out, "--remove-project-config") {
		t.Fatalf("应提示已提交的文件没动：%s", out)
	}
	// 明确要求时才移除
	if out, err := run(t, "init", "--dir", proj, "--remove-project-config"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, rel := range []string{".mcp.json", ".claude", ".codex", ".agents"} {
		if exists(filepath.Join(proj, rel)) {
			t.Fatalf("--remove-project-config 后 %s 应已移除", rel)
		}
	}
}

func TestInitStaleHints(t *testing.T) {
	userEnv(t)
	saveLogins(t, "claude-code")
	proj := gitProject(t)
	os.WriteFile(filepath.Join(proj, ".worktreeinclude"), []byte(".mcp.json\n.claude/settings.json\n"), 0o644)
	os.WriteFile(filepath.Join(proj, ".git", "info", "exclude"), []byte("/.mcp.json\n"), 0o644)
	out, err := run(t, "init", "--dir", proj)
	if err != nil || !strings.Contains(out, ".worktreeinclude") || !strings.Contains(out, ".git/info/exclude") {
		t.Fatalf("应提示可以删掉旧的复制清单和排除行：%v\n%s", err, out)
	}
	if !strings.Contains(readFile(t, filepath.Join(proj, ".worktreeinclude")), ".mcp.json") {
		t.Fatal("不替用户改这两个文件")
	}
}

func TestInitShareAndDefaultFromSharedFile(t *testing.T) {
	userEnv(t)
	c := &client.Credentials{}
	for _, ws := range []string{"payments", "ops"} {
		c.Upsert(client.Login{Server: srv, Workspace: ws, Member: "lee", Tool: "claude-code", Token: "t_" + ws})
	}
	c.Save()
	proj := gitProject(t)
	if _, err := run(t, "init", "--dir", proj); err == nil || !strings.Contains(err.Error(), "--workspace") {
		t.Fatalf("两个工作空间又没指定应报错：%v", err)
	}
	if _, err := run(t, "init", "--dir", proj, "--workspace", "ops", "--share"); err != nil {
		t.Fatal(err)
	}
	var f struct{ Server, Workspace string }
	if _, err := toml.Decode(readFile(t, filepath.Join(proj, client.SharedFileName)), &f); err != nil || f.Workspace != "ops" || f.Server != srv {
		t.Fatalf(".cohive.toml：%+v %v", f, err)
	}
	if strings.Contains(readFile(t, filepath.Join(proj, client.SharedFileName)), "t_ops") {
		t.Fatal(".cohive.toml 不能含令牌")
	}

	// 队友克隆：有 .cohive.toml、本机没绑定；不带参数的 init 采用它
	other := gitProject(t)
	os.WriteFile(filepath.Join(other, client.SharedFileName), []byte(readFile(t, filepath.Join(proj, client.SharedFileName))), 0o644)
	if _, err := run(t, "init", "--dir", other); err != nil {
		t.Fatalf("应采用 .cohive.toml：%v", err)
	}
	key, _, _ := bindKey(other)
	if b, _ := client.LoadBindings(); func() string { x, _ := b.Find(key); return x.Workspace }() != "ops" {
		t.Fatalf("绑定应是 ops：%+v", b.Items)
	}
}

// 已绑定的目录重新 init 沿用原来的工作空间，不因为“登录了多个工作空间”而报错。
func TestInitRerunKeepsBinding(t *testing.T) {
	userEnv(t)
	c := &client.Credentials{}
	for _, ws := range []string{"payments", "ops"} {
		c.Upsert(client.Login{Server: srv, Workspace: ws, Member: "lee", Tool: "claude-code", Token: "t_" + ws})
	}
	c.Save()
	proj := gitProject(t)
	if _, err := run(t, "init", "--dir", proj, "--workspace", "ops"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "init", "--dir", proj); err != nil {
		t.Fatalf("重新执行应沿用绑定：%v", err)
	}
	key, _, _ := bindKey(proj)
	if b, _ := client.LoadBindings(); func() string { x, _ := b.Find(key); return x.Workspace }() != "ops" {
		t.Fatal("不应改变绑定")
	}
}

// 第二个克隆：同一远程地址已有绑定时提示是否沿用，确认了才写入。
func TestInitSecondCloneAsksBeforeReuse(t *testing.T) {
	userEnv(t)
	c := &client.Credentials{}
	for _, ws := range []string{"payments", "ops"} {
		c.Upsert(client.Login{Server: srv, Workspace: ws, Member: "lee", Tool: "claude-code", Token: "t_" + ws})
	}
	c.Save()
	first, second := gitProject(t), gitProject(t)
	for _, d := range []string{first, second} {
		gitIn(t, d, "remote", "add", "origin", "https://user:secret@example.com/team/repo.git")
	}
	if _, err := run(t, "init", "--dir", first, "--workspace", "ops"); err != nil {
		t.Fatal(err)
	}
	if b, _ := client.LoadBindings(); b.Items[0].Remote != "https://example.com/team/repo.git" {
		t.Fatalf("绑定里记的远程地址应去掉用户信息：%q", b.Items[0].Remote)
	}

	// 不确认（或没有输入）：不沿用，照常报“有多个工作空间”
	out, err := runIn(t, "n\n", "init", "--dir", second)
	if err == nil || !strings.Contains(out, "这个目录也用同一个吗") {
		t.Fatalf("应先询问，且不回答 y 就不沿用：%v\n%s", err, out)
	}
	if b, _ := client.LoadBindings(); len(b.Items) != 1 {
		t.Fatalf("没确认不应写入绑定：%+v", b.Items)
	}
	// 确认
	if out, err := runIn(t, "y\n", "init", "--dir", second); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	key, _, _ := bindKey(second)
	if b, _ := client.LoadBindings(); func() string { x, _ := b.Find(key); return x.Workspace }() != "ops" {
		t.Fatalf("确认后应沿用 ops：%+v", b.Items)
	}
}

func TestInitPrunesMissingBindings(t *testing.T) {
	userEnv(t)
	saveLogins(t, "claude-code")
	bindDir(t, filepath.Join(realTemp(t), "gone"), srv, "payments")
	if _, err := run(t, "init", "--dir", gitProject(t)); err != nil {
		t.Fatal(err)
	}
	if b, _ := client.LoadBindings(); len(b.Items) != 1 {
		t.Fatalf("目录已不存在的绑定应被清掉：%+v", b.Items)
	}
}

func TestUnbindAndStatus(t *testing.T) {
	userEnv(t)
	saveLogins(t, "claude-code")
	proj := gitProject(t)
	t.Chdir(proj)
	out, _ := run(t, "status")
	if !strings.Contains(out, "未绑定") {
		t.Fatalf("没绑定时 status 应说明：%s", out)
	}
	if _, err := run(t, "init", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	out, _ = run(t, "status")
	if !strings.Contains(out, "当前目录") || !strings.Contains(out, "payments") {
		t.Fatalf("status 应显示绑定：%s", out)
	}
	if out, err := run(t, "unbind", "--dir", proj); err != nil || !strings.Contains(out, "已解除") {
		t.Fatalf("%v %s", err, out)
	}
	if b, _ := client.LoadBindings(); len(b.Items) != 0 {
		t.Fatal("应已解除")
	}
	if out, _ := run(t, "unbind", "--dir", proj); !strings.Contains(out, "没有绑定") {
		t.Fatalf("重复解除应说明：%s", out)
	}
}
