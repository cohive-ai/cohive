package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/cohive-ai/cohive/internal/client"
)

const srv = "https://cohive.example.com"

func saveLogins(t *testing.T, tools ...string) {
	t.Helper()
	c := &client.Credentials{}
	for _, tool := range tools {
		c.Upsert(client.Login{Server: srv, Workspace: "payments", Member: "lee", Tool: tool, Token: "cohive_secret_" + tool})
	}
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInitWritesBothTools(t *testing.T) {
	configDir(t)
	saveLogins(t, "claude-code", "codex")
	proj := t.TempDir()
	out, err := run(t, "init", "--dir", proj)
	if err != nil {
		t.Fatalf("init 失败：%v\n%s", err, out)
	}

	var mcp map[string]any
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(proj, ".mcp.json"))), &mcp); err != nil {
		t.Fatal(err)
	}
	entry := mcp["mcpServers"].(map[string]any)["cohive"].(map[string]any)
	args, _ := json.Marshal(entry["args"])
	if entry["command"] != "cohive" || !strings.Contains(string(args), `"mcp-proxy","--server","https://cohive.example.com","--workspace","payments","--tool","claude-code"`) {
		t.Fatalf(".mcp.json 的 cohive 条目不符：%v", entry)
	}

	settings := readFile(t, filepath.Join(proj, ".claude", "settings.json"))
	for _, want := range []string{`"mcp__cohive__cohive_submit_issue"`, `"ask"`, `"enabledMcpjsonServers"`} {
		if !strings.Contains(settings, want) {
			t.Fatalf("Claude Code 设置应包含 %s：%s", want, settings)
		}
	}
	codex := readFile(t, filepath.Join(proj, ".codex", "config.toml"))
	for _, want := range []string{"# >>> cohive", "# <<< cohive", `command = "cohive"`, `"--tool", "codex"`,
		"[mcp_servers.cohive.tools.cohive_close_issue]", `approval_mode = "prompt"`} {
		if !strings.Contains(codex, want) {
			t.Fatalf("Codex 配置应包含 %s：\n%s", want, codex)
		}
	}
	for _, p := range []string{".claude/skills/cohive/SKILL.md", ".agents/skills/cohive/SKILL.md"} {
		if !strings.Contains(readFile(t, filepath.Join(proj, p)), "name: cohive") {
			t.Fatalf("%s 应写入技能", p)
		}
	}

	// 项目文件里不能有令牌
	filepath.Walk(proj, func(path string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && strings.Contains(readFile(t, path), "cohive_secret") {
			t.Errorf("%s 里出现了令牌", path)
		}
		return nil
	})
	if !strings.Contains(out, "信任") {
		t.Fatalf("应提示信任项目、批准 MCP 服务：%s", out)
	}
}

// 生成的 Codex 配置必须是合法 TOML，且确认规则正确。
func TestInitCodexConfigParses(t *testing.T) {
	configDir(t)
	saveLogins(t, "codex")
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, ".codex"), 0o755)
	os.WriteFile(filepath.Join(proj, ".codex", "config.toml"), []byte("model = \"gpt\"\n"), 0o644)
	if _, err := run(t, "init", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Model      string `toml:"model"`
		MCPServers map[string]struct {
			Command string   `toml:"command"`
			Args    []string `toml:"args"`
			Tools   map[string]struct {
				ApprovalMode string `toml:"approval_mode"`
			} `toml:"tools"`
		} `toml:"mcp_servers"`
	}
	if _, err := toml.DecodeFile(filepath.Join(proj, ".codex", "config.toml"), &cfg); err != nil {
		t.Fatalf("生成的 Codex 配置不是合法 TOML：%v", err)
	}
	c := cfg.MCPServers["cohive"]
	if cfg.Model != "gpt" || c.Command != "cohive" || len(c.Args) != 7 || c.Args[0] != "mcp-proxy" {
		t.Fatalf("解析结果不符：%+v", cfg)
	}
	for _, name := range confirmTools {
		if c.Tools[name].ApprovalMode != "prompt" {
			t.Fatalf("%s 应每次确认，得到 %q", name, c.Tools[name].ApprovalMode)
		}
	}
	if c.Tools["cohive_inbox"].ApprovalMode != "approve" {
		t.Fatal("只读工具应直接放行")
	}
}

func TestInitMergesAndIsIdempotent(t *testing.T) {
	configDir(t)
	saveLogins(t, "claude-code", "codex")
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, ".claude"), 0o755)
	os.MkdirAll(filepath.Join(proj, ".codex"), 0o755)
	os.WriteFile(filepath.Join(proj, ".mcp.json"), []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o644)
	os.WriteFile(filepath.Join(proj, ".claude", "settings.json"), []byte(`{"permissions":{"allow":["Bash(make test)"]},"model":"opus"}`), 0o644)
	os.WriteFile(filepath.Join(proj, ".codex", "config.toml"), []byte("model = \"gpt\"\n\n[mcp_servers.other]\ncommand = \"x\"\n"), 0o644)

	if _, err := run(t, "init", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	first := map[string]string{}
	for _, p := range []string{".mcp.json", ".claude/settings.json", ".codex/config.toml"} {
		first[p] = readFile(t, filepath.Join(proj, p))
	}
	if !strings.Contains(first[".mcp.json"], `"other"`) || !strings.Contains(first[".claude/settings.json"], "Bash(make test)") ||
		!strings.Contains(first[".claude/settings.json"], `"model"`) || !strings.Contains(first[".codex/config.toml"], "[mcp_servers.other]") {
		t.Fatalf("应保留原有配置：%v", first)
	}

	if _, err := run(t, "init", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	for p, before := range first {
		if after := readFile(t, filepath.Join(proj, p)); after != before {
			t.Fatalf("%s 重复执行后不应变化：\n%s\n----\n%s", p, before, after)
		}
	}
	if strings.Count(first[".codex/config.toml"], "# >>> cohive") != 1 {
		t.Fatal("Codex 配置块不应重复")
	}
}

func TestInitOnlyLoggedInTools(t *testing.T) {
	configDir(t)
	saveLogins(t, "codex")
	proj := t.TempDir()
	out, err := run(t, "init", "--dir", proj)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(proj, ".mcp.json")); err == nil {
		t.Fatal("没有登录 Claude Code 时，默认不写 Claude Code 配置")
	}
	if _, err := os.Stat(filepath.Join(proj, ".codex", "config.toml")); err != nil {
		t.Fatal("应写 Codex 配置")
	}

	out, err = run(t, "init", "--dir", proj, "--tools", "claude-code")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "cohive login") || !strings.Contains(out, "claude-code") {
		t.Fatalf("指定了未登录的工具时应写配置并提示登录：%s", out)
	}
}

func TestInitErrors(t *testing.T) {
	configDir(t)
	if _, err := run(t, "init", "--dir", t.TempDir()); err == nil || !strings.Contains(err.Error(), "cohive login") {
		t.Fatalf("没有任何登录时应提示先登录，得到 %v", err)
	}

	saveLogins(t, "codex")
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, ".codex"), 0o755)
	os.WriteFile(filepath.Join(proj, ".codex", "config.toml"), []byte("[mcp_servers.cohive]\ncommand = \"old\"\n"), 0o644)
	if _, err := run(t, "init", "--dir", proj); err == nil || !strings.Contains(err.Error(), "mcp_servers.cohive") {
		t.Fatalf("已有手写的 cohive 配置时应报错而不是写出重复配置，得到 %v", err)
	}
}

// init 写完配置后检查转发器命令是否在 PATH 里，不在时提示。
func TestInitWarnsWhenCommandNotOnPath(t *testing.T) {
	configDir(t)
	saveLogins(t, "claude-code")
	old := lookPath
	defer func() { lookPath = old }()
	lookPath = func(string) (string, error) { return "", os.ErrNotExist }
	out, err := run(t, "init", "--dir", t.TempDir(), "--tools", "claude-code")
	if err != nil {
		t.Fatalf("init 失败：%v\n%s", err, out)
	}
	if !strings.Contains(out, "PATH 里找不到") {
		t.Fatalf("应提示 cohive 不在 PATH 里：%s", out)
	}
	lookPath = func(string) (string, error) { return "/usr/local/bin/cohive", nil }
	if out, _ := run(t, "init", "--dir", t.TempDir(), "--tools", "claude-code"); strings.Contains(out, "PATH 里找不到") {
		t.Fatalf("在 PATH 里时不提示：%s", out)
	}
}
