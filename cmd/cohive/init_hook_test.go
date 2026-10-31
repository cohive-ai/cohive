package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

type claudeHookCfg struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher,omitempty"`
		Hooks   []struct {
			Type    string `json:"type"`
			Command string `json:"command"`
			Timeout int    `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func claudeHooks(t *testing.T, proj string) claudeHookCfg {
	t.Helper()
	var c claudeHookCfg
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(proj, ".claude", "settings.json"))), &c); err != nil {
		t.Fatal(err)
	}
	return c
}

type codexHookCfg struct {
	Hooks map[string][]struct {
		Hooks []struct {
			Type    string `toml:"type"`
			Command string `toml:"command"`
			Timeout int    `toml:"timeout"`
		} `toml:"hooks"`
	} `toml:"hooks"`
}

func codexHooks(t *testing.T, proj string) codexHookCfg {
	t.Helper()
	var c codexHookCfg
	if _, err := toml.DecodeFile(filepath.Join(proj, ".codex", "config.toml"), &c); err != nil {
		t.Fatalf("Codex 配置应是合法 TOML：%v", err)
	}
	return c
}

// 6.2 第 12、13 条：两家都写会话开始、提交提示词钩子；终端提示里说清两家的信任步骤。
func TestInitWritesHooks(t *testing.T) {
	configDir(t)
	saveLogins(t, "claude-code", "codex")
	proj := t.TempDir()
	out, err := run(t, "init", "--project", "--dir", proj)
	if err != nil {
		t.Fatalf("init 失败：%v\n%s", err, out)
	}
	cc, cx := claudeHooks(t, proj), codexHooks(t, proj)
	for _, ev := range hookEvents {
		g := cc.Hooks[ev]
		if len(g) != 1 || len(g[0].Hooks) != 1 || g[0].Hooks[0].Type != "command" || g[0].Hooks[0].Timeout != 5 ||
			g[0].Hooks[0].Command != "cohive hook --server https://cohive.example.com --workspace payments --tool claude-code" {
			t.Fatalf("Claude Code 的 %s 钩子不符：%+v", ev, g)
		}
		x := cx.Hooks[ev]
		if len(x) != 1 || len(x[0].Hooks) != 1 || x[0].Hooks[0].Timeout != 5 ||
			x[0].Hooks[0].Command != "cohive hook --server https://cohive.example.com --workspace payments --tool codex" {
			t.Fatalf("Codex 的 %s 钩子不符：%+v", ev, x)
		}
	}
	for _, want := range []string{"Hooks need review", "Trust all and continue", "SessionStart、UserPromptSubmit", "每台电脑、每个克隆目录", "信任此文件夹", "主检出目录", "hook --check"} {
		if !strings.Contains(out, want) {
			t.Fatalf("终端提示应包含 %q：\n%s", want, out)
		}
	}
}

// 6.2 第 12 条：用户自己的钩子与顺序保留；重复执行、换命令路径都不重复；--no-hooks 移除干净。
func TestInitHooksMergeAndRemove(t *testing.T) {
	configDir(t)
	saveLogins(t, "claude-code", "codex")
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, ".claude"), 0o755)
	user := `{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":"./mine.sh"}]}],` +
		`"Stop":[{"hooks":[{"type":"command","command":"say done"}]}]}}`
	os.WriteFile(filepath.Join(proj, ".claude", "settings.json"), []byte(user), 0o644)

	if _, err := run(t, "init", "--project", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "init", "--project", "--dir", proj, "--command", "/Users/me/My Tools/cohive"); err != nil {
		t.Fatal(err)
	}
	cc := claudeHooks(t, proj)
	ss := cc.Hooks["SessionStart"]
	if len(ss) != 2 || ss[0].Hooks[0].Command != "./mine.sh" || ss[0].Matcher != "startup" ||
		ss[1].Hooks[0].Command != "'/Users/me/My Tools/cohive' hook --server https://cohive.example.com --workspace payments --tool claude-code" {
		t.Fatalf("用户的钩子应在前、cohive 的只有一组且命令路径加了引号：%+v", ss)
	}
	if len(cc.Hooks["UserPromptSubmit"]) != 1 || len(cc.Hooks["Stop"]) != 1 {
		t.Fatalf("其他事件不应受影响：%+v", cc.Hooks)
	}
	if x := codexHooks(t, proj).Hooks["UserPromptSubmit"]; len(x) != 1 || !strings.HasPrefix(x[0].Hooks[0].Command, "'/Users/me/My Tools/cohive' hook ") {
		t.Fatalf("Codex 钩子应随命令路径更新且不重复：%+v", x)
	}

	out, err := run(t, "init", "--project", "--dir", proj, "--no-hooks")
	if err != nil {
		t.Fatal(err)
	}
	cc = claudeHooks(t, proj)
	if len(cc.Hooks["SessionStart"]) != 1 || cc.Hooks["SessionStart"][0].Hooks[0].Command != "./mine.sh" ||
		cc.Hooks["UserPromptSubmit"] != nil || len(cc.Hooks["Stop"]) != 1 {
		t.Fatalf("--no-hooks 应只移除 cohive 的钩子：%+v", cc.Hooks)
	}
	if strings.Contains(readFile(t, filepath.Join(proj, ".codex", "config.toml")), "[[hooks.") {
		t.Fatal("--no-hooks 应移除 Codex 的 cohive 钩子")
	}
	if strings.Contains(out, "会话内提醒") {
		t.Fatalf("--no-hooks 时不提示钩子：%s", out)
	}

	// 只有 cohive 的钩子时，移除后 hooks 键也不留
	proj2 := t.TempDir()
	run(t, "init", "--project", "--dir", proj2, "--tools", "claude-code")
	run(t, "init", "--project", "--dir", proj2, "--tools", "claude-code", "--no-hooks")
	if strings.Contains(readFile(t, filepath.Join(proj2, ".claude", "settings.json")), `"hooks"`) {
		t.Fatal("只有 cohive 的钩子时，移除后不应留下空的 hooks")
	}
}

// 6.2 第 12、14 条：用户配置里钩子的类型不对、Codex 里有冲突的写法 → 报错，什么都不写。
func TestInitHooksRefuseBadConfig(t *testing.T) {
	configDir(t)
	saveLogins(t, "claude-code", "codex")
	for _, bad := range []string{`{"hooks":[]}`, `{"hooks":{"SessionStart":{}}}`} {
		proj := t.TempDir()
		os.MkdirAll(filepath.Join(proj, ".claude"), 0o755)
		os.WriteFile(filepath.Join(proj, ".claude", "settings.json"), []byte(bad), 0o644)
		if _, err := run(t, "init", "--project", "--dir", proj, "--tools", "claude-code"); err == nil || !strings.Contains(err.Error(), "hooks") {
			t.Fatalf("%s 应报错：%v", bad, err)
		}
		if readFile(t, filepath.Join(proj, ".claude", "settings.json")) != bad {
			t.Fatal("报错时不应改写设置")
		}
		if _, err := os.Stat(filepath.Join(proj, ".mcp.json")); err == nil {
			t.Fatal("报错时不应写 .mcp.json")
		}
	}
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, ".codex"), 0o755)
	bad := "hooks = \"off\"\n"
	os.WriteFile(filepath.Join(proj, ".codex", "config.toml"), []byte(bad), 0o644)
	if _, err := run(t, "init", "--project", "--dir", proj, "--tools", "codex"); err == nil {
		t.Fatal("Codex 配置里 hooks 写法冲突时应报错")
	}
	if readFile(t, filepath.Join(proj, ".codex", "config.toml")) != bad {
		t.Fatal("报错时不应改写 Codex 配置")
	}
}

func TestShellWordsRoundTrip(t *testing.T) {
	for _, argv := range [][]string{{"cohive", "hook"}, {"/a b/cohive", "hook", "--server", "https://x/y?z=1&w"}, {"it's", "a'b'c"}} {
		if got := shellWords(shellCommand(argv)); !reflect.DeepEqual(got, argv) {
			t.Fatalf("%v → %q → %v", argv, shellCommand(argv), got)
		}
	}
	if isCohiveHook("cohivex hook") || isCohiveHook("cohive mcp-proxy") || !isCohiveHook("/usr/local/bin/cohive hook --tool codex") {
		t.Fatal("isCohiveHook 判断不对")
	}
}
