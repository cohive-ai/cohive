package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 仓库里预先放好的符号链接不能让 init 写到项目之外。
func TestInitRefusesSymlinks(t *testing.T) {
	configDir(t)
	saveLogins(t, "claude-code", "codex")
	outside := t.TempDir()
	victim := filepath.Join(outside, "bashrc")
	os.WriteFile(victim, []byte("export SAFE=1\n"), 0o644)

	for _, link := range []string{".agents/skills/cohive/SKILL.md", ".mcp.json", ".claude"} {
		proj := t.TempDir()
		target := victim
		if link == ".claude" {
			target = outside
		}
		os.MkdirAll(filepath.Dir(filepath.Join(proj, link)), 0o755)
		if err := os.Symlink(target, filepath.Join(proj, link)); err != nil {
			t.Fatal(err)
		}
		if _, err := run(t, "init", "--dir", proj); err == nil || !strings.Contains(err.Error(), "符号链接") {
			t.Fatalf("%s 是符号链接时应拒绝，得到 %v", link, err)
		}
		if got := readFile(t, victim); got != "export SAFE=1\n" {
			t.Fatalf("项目外的文件被改写了：%q", got)
		}
		if _, err := os.Stat(filepath.Join(outside, "settings.json")); err == nil {
			t.Fatal("不应在链接指向的目录里写文件")
		}
	}
}

// 合并 JSON 时不能改写用户原有内容。
func TestInitPreservesExistingJSON(t *testing.T) {
	configDir(t)
	saveLogins(t, "claude-code")
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, ".claude"), 0o755)
	orig := `{"hooks":{"PostToolUse":[{"command":"make lint && go test ./... > /tmp/out"}]},"bigNumber":12345678901234567890,"permissions":{"deny":["Bash(rm -rf *)"]}}`
	os.WriteFile(filepath.Join(proj, ".claude", "settings.json"), []byte(orig), 0o644)
	if _, err := run(t, "init", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(proj, ".claude", "settings.json"))
	for _, want := range []string{"make lint && go test ./... > /tmp/out", "12345678901234567890", "Bash(rm -rf *)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("原有内容 %q 应原样保留：\n%s", want, got)
		}
	}
}

func TestInitRefusesWrongJSONTypes(t *testing.T) {
	configDir(t)
	saveLogins(t, "claude-code")
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, ".claude"), 0o755)
	os.WriteFile(filepath.Join(proj, ".claude", "settings.json"), []byte(`{"permissions":{"allow":"Bash(*)"}}`), 0o644)
	if _, err := run(t, "init", "--dir", proj); err == nil || !strings.Contains(err.Error(), "permissions.allow") {
		t.Fatalf("类型不对时应报错而不是覆盖，得到 %v", err)
	}
	if got := readFile(t, filepath.Join(proj, ".claude", "settings.json")); !strings.Contains(got, `"allow":"Bash(*)"`) {
		t.Fatalf("报错时不应改动文件：%s", got)
	}
}

// Codex 配置里已有 cohive 条目要用 TOML 解析判断：不误报相似名字，不漏报其他写法。
func TestInitCodexDetection(t *testing.T) {
	configDir(t)
	saveLogins(t, "codex")
	write := func(content string) string {
		proj := t.TempDir()
		os.MkdirAll(filepath.Join(proj, ".codex"), 0o755)
		os.WriteFile(filepath.Join(proj, ".codex", "config.toml"), []byte(content), 0o644)
		return proj
	}
	if _, err := run(t, "init", "--dir", write("[mcp_servers.cohive-dev]\ncommand = \"x\"\n")); err != nil {
		t.Fatalf("名字相似的 cohive-dev 不应被当成已有条目：%v", err)
	}
	for _, content := range []string{
		"[mcp_servers.\"cohive\"]\ncommand = \"x\"\n",
		"[mcp_servers]\ncohive = { command = \"x\" }\n",
	} {
		if _, err := run(t, "init", "--dir", write(content)); err == nil || !strings.Contains(err.Error(), "mcp_servers.cohive") {
			t.Fatalf("%q 里已有 cohive 条目，应报错，得到 %v", content, err)
		}
	}
	if _, err := run(t, "init", "--dir", write("this is = = not toml\n")); err == nil || !strings.Contains(err.Error(), "TOML") {
		t.Fatalf("原文件不是合法 TOML 时应报错，得到 %v", err)
	}
}
