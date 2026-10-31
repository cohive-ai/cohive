package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
)

type opencodeCfg struct {
	Schema string `json:"$schema"`
	MCP    map[string]struct {
		Type    string   `json:"type"`
		Command []string `json:"command"`
	} `json:"mcp"`
	Permission map[string]any `json:"permission"`
}

func readOpencode(t *testing.T, proj string) opencodeCfg {
	t.Helper()
	var c opencodeCfg
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(proj, "opencode.json"))), &c); err != nil {
		t.Fatalf("opencode.json 应是合法 JSON：%v", err)
	}
	return c
}

// keyOrder 按出现顺序列出 JSON 对象（路径 path 下）的键。
func keyOrder(t *testing.T, raw []byte, path ...string) []string {
	t.Helper()
	for _, p := range path {
		o, ok := parseOrdered(raw)
		if !ok || o.index(p) < 0 {
			t.Fatalf("找不到 %v", path)
		}
		raw = o[o.index(p)].Val
	}
	o, ok := parseOrdered(raw)
	if !ok {
		t.Fatalf("%v 不是对象：%s", path, raw)
	}
	var keys []string
	for _, f := range o {
		keys = append(keys, f.Key)
	}
	return keys
}

func TestInitOpencodeWritesConfig(t *testing.T) {
	configDir(t)
	saveLogins(t, "opencode")
	proj := t.TempDir()
	out, err := run(t, "init", "--project", "--dir", proj)
	if err != nil {
		t.Fatalf("init 失败：%v\n%s", err, out)
	}
	c := readOpencode(t, proj)
	want := []string{"cohive", "mcp-proxy", "--server", srv, "--workspace", "payments", "--tool", "opencode"}
	if c.Schema != "https://opencode.ai/config.json" || c.MCP["cohive"].Type != "local" || !reflect.DeepEqual(c.MCP["cohive"].Command, want) {
		t.Fatalf("MCP 配置不符：%+v", c)
	}
	for _, tool := range allowTools {
		if c.Permission["cohive_"+tool] != "allow" {
			t.Fatalf("%s 应直接放行：%v", tool, c.Permission)
		}
	}
	for _, tool := range confirmTools {
		if c.Permission["cohive_"+tool] != "ask" {
			t.Fatalf("%s 应每次询问：%v", tool, c.Permission)
		}
	}
	if len(c.Permission) != len(allowTools)+len(confirmTools) {
		t.Fatalf("不应多出别的规则：%v", c.Permission)
	}
	plugin := readFile(t, filepath.Join(proj, ".opencode", "plugins", "cohive.ts"))
	if !strings.HasPrefix(plugin, opencodePluginMarker) ||
		!strings.Contains(plugin, `const HOOK: string[] = ["cohive","hook","--server","`+srv+`","--workspace","payments","--tool","opencode"]`) ||
		strings.Contains(plugin, "__COHIVE_") {
		t.Fatalf("插件内容不符：\n%s", plugin)
	}
	if skill := readFile(t, filepath.Join(proj, ".agents", "skills", "cohive", "SKILL.md")); !strings.Contains(skill, "name: cohive") {
		t.Fatalf("技能文件应写到 .agents/skills：%s", skill)
	}
	for _, s := range []string{"OpenCode：opencode.json、.opencode/plugins/cohive.ts、.agents", "不用信任", "Claude Code、OpenCode 里用 /cohive"} {
		if !strings.Contains(out, s) {
			t.Fatalf("终端提示应包含 %q：\n%s", s, out)
		}
	}
}

// 用户自己的配置：键的顺序、嵌套内容、数字写法都不动；cohive 的规则放到最后，压过用户写在前面的通配；重复执行结果不变。
func TestInitOpencodeMergeKeepsOrder(t *testing.T) {
	configDir(t)
	saveLogins(t, "opencode")
	proj := t.TempDir()
	user := `{
  "model": "deepseek/deepseek-chat",
  "permission": {"*": "ask", "cohive_cohive_submit_issue": "allow", "cohive_cohive_inbox": "deny", "shell": {"rm *": "deny", "*": "ask"}},
  "mcp": {"other": {"type": "remote", "url": "https://x.example/mcp?a=1&b=2", "timeout": 10000}, "cohive": {"type": "remote", "url": "old"}},
  "zzz": 1.50
}`
	os.WriteFile(filepath.Join(proj, "opencode.json"), []byte(user), 0o644)
	if out, err := run(t, "init", "--project", "--dir", proj, "--tools", "opencode"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	raw := []byte(readFile(t, filepath.Join(proj, "opencode.json")))
	if got := keyOrder(t, raw); !reflect.DeepEqual(got, []string{"model", "permission", "mcp", "zzz"}) {
		t.Fatalf("顶层键的顺序应保持，且不加 $schema：%v", got)
	}
	wantPerm := []string{"*", "shell"}
	for _, tool := range append(append([]string{}, allowTools...), confirmTools...) {
		wantPerm = append(wantPerm, "cohive_"+tool)
	}
	if perm := keyOrder(t, raw, "permission"); !reflect.DeepEqual(perm, wantPerm) {
		t.Fatalf("用户的规则在前、cohive 的全部挪到最后：%v", perm)
	}
	if got := keyOrder(t, raw, "permission", "shell"); !reflect.DeepEqual(got, []string{"rm *", "*"}) {
		t.Fatalf("嵌套规则的顺序应保持：%v", got)
	}
	if got := keyOrder(t, raw, "mcp"); !reflect.DeepEqual(got, []string{"other", "cohive"}) {
		t.Fatalf("mcp 里的服务顺序应保持：%v", got)
	}
	s := string(raw)
	for _, keep := range []string{`"zzz": 1.50`, `"timeout": 10000`, `https://x.example/mcp?a=1&b=2`} {
		if !strings.Contains(s, keep) {
			t.Fatalf("用户内容应原样保留 %q：\n%s", keep, s)
		}
	}
	c := readOpencode(t, proj)
	if c.Permission["cohive_cohive_submit_issue"] != "ask" || c.Permission["cohive_cohive_inbox"] != "allow" || c.MCP["cohive"].Type != "local" {
		t.Fatalf("cohive 的条目应换成 init 的写法：%+v", c)
	}
	if _, err := run(t, "init", "--project", "--dir", proj, "--tools", "opencode"); err != nil {
		t.Fatal(err)
	}
	if again := readFile(t, filepath.Join(proj, "opencode.json")); again != s {
		t.Fatalf("重复执行应得到同样的文件：\n%s\n----\n%s", s, again)
	}
}

// permission 整体写成字符串时，等价展开成 {"*": 值} 再加规则。
func TestInitOpencodePermissionString(t *testing.T) {
	configDir(t)
	saveLogins(t, "opencode")
	proj := t.TempDir()
	os.WriteFile(filepath.Join(proj, "opencode.json"), []byte(`{"permission":"ask"}`), 0o644)
	if out, err := run(t, "init", "--project", "--dir", proj, "--tools", "opencode"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	raw := []byte(readFile(t, filepath.Join(proj, "opencode.json")))
	perm := keyOrder(t, raw, "permission")
	if perm[0] != "*" || readOpencode(t, proj).Permission["*"] != "ask" || len(perm) != 1+len(allowTools)+len(confirmTools) {
		t.Fatalf("应展开成 {\"*\": \"ask\"} 加 cohive 规则：%v", perm)
	}
}

// 拒绝处理的情形：什么都不写，原文件不变。
func TestInitOpencodeRefuses(t *testing.T) {
	configDir(t)
	saveLogins(t, "opencode")
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"带注释的 jsonc", map[string]string{"opencode.jsonc": "// mine\n{}"}, "带注释"},
		{"不是合法 JSON", map[string]string{"opencode.json": "{\n  // 注释\n}"}, "opencode.json"},
		{"顶层不是对象", map[string]string{"opencode.json": "[]"}, "array"},
		{"mcp 不是对象", map[string]string{"opencode.json": `{"mcp":["x"]}`}, "opencode.json 的 mcp"},
		{"permission 是数组", map[string]string{"opencode.json": `{"permission":["x"]}`}, "opencode.json 的 permission"},
		{"permission 是别的字符串", map[string]string{"opencode.json": `{"permission":"maybe"}`}, "opencode.json 的 permission"},
		{"用户自己的同名插件", map[string]string{"opencode.json": `{"model":"x"}`, ".opencode/plugins/cohive.ts": "export const Mine = 1\n"}, "不是 cohive init 生成的"},
	}
	for _, c := range cases {
		proj := t.TempDir()
		for rel, body := range c.files {
			os.MkdirAll(filepath.Dir(filepath.Join(proj, rel)), 0o755)
			os.WriteFile(filepath.Join(proj, rel), []byte(body), 0o644)
		}
		out, err := run(t, "init", "--project", "--dir", proj, "--tools", "opencode")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%s：应报错含 %q，实际 %v\n%s", c.name, c.want, err, out)
		}
		for rel, body := range c.files {
			if got := readFile(t, filepath.Join(proj, rel)); got != body {
				t.Fatalf("%s：%s 不应被改动：%s", c.name, rel, got)
			}
		}
		if _, err := os.Stat(filepath.Join(proj, ".agents")); err == nil {
			t.Fatalf("%s：报错时不应写技能文件", c.name)
		}
	}
}

func TestInitOpencodeSymlink(t *testing.T) {
	configDir(t)
	saveLogins(t, "opencode")
	proj := t.TempDir()
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(proj, ".opencode"))
	if _, err := run(t, "init", "--project", "--dir", proj, "--tools", "opencode"); err == nil || !strings.Contains(err.Error(), "符号链接") {
		t.Fatalf("经符号链接写到项目之外应报错：%v", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("不应写到链接指向的目录：%v", entries)
	}
}

// --no-hooks 只移除 cohive init 写的插件；用户自己的同名文件不动，也不因它报错。
func TestInitOpencodeNoHooks(t *testing.T) {
	configDir(t)
	saveLogins(t, "opencode")
	proj := t.TempDir()
	plugin := filepath.Join(proj, ".opencode", "plugins", "cohive.ts")
	if _, err := run(t, "init", "--project", "--dir", proj, "--tools", "opencode"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "init", "--project", "--dir", proj, "--tools", "opencode", "--no-hooks")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plugin); err == nil {
		t.Fatal("--no-hooks 应移除 cohive 的插件")
	}
	if strings.Contains(out, "会话内提醒") || strings.Contains(out, "plugins/cohive.ts") || !strings.Contains(out, "OpenCode：opencode.json、.agents") {
		t.Fatalf("--no-hooks 时不提示提醒、不列插件文件：%s", out)
	}
	if readOpencode(t, proj).MCP["cohive"].Type != "local" {
		t.Fatal("--no-hooks 不影响 MCP 配置")
	}
	os.WriteFile(plugin, []byte("export const Mine = 1\n"), 0o644)
	if _, err := run(t, "init", "--project", "--dir", proj, "--tools", "opencode", "--no-hooks"); err != nil {
		t.Fatalf("--no-hooks 时用户自己的同名文件不应导致报错：%v", err)
	}
	if readFile(t, plugin) != "export const Mine = 1\n" {
		t.Fatal("用户自己的同名文件不应被删")
	}
}

// 三家一起配置：技能文件各写一份（opencode 与 Codex 共用 .agents/skills），login 能从三份配置读回同一个目标。
func TestInitAllThreeAndRepoTarget(t *testing.T) {
	configDir(t)
	saveLogins(t, "claude-code", "codex", "opencode")
	proj := t.TempDir()
	out, err := run(t, "init", "--project", "--dir", proj)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	for _, s := range []string{"Claude Code：", "Codex：", "OpenCode："} {
		if !strings.Contains(out, s) {
			t.Fatalf("应列出 %q：\n%s", s, out)
		}
	}
	rt, err := readRepoTarget(proj)
	if err != nil || rt.Server != srv || rt.Workspace != "payments" || !reflect.DeepEqual(rt.Tools, []string{"claude-code", "codex", "opencode"}) {
		t.Fatalf("readRepoTarget = %+v %v", rt, err)
	}
	// 只有 opencode 的仓库也认得出
	only := t.TempDir()
	saveLogins(t, "opencode")
	run(t, "init", "--project", "--dir", only)
	if rt, err := readRepoTarget(only); err != nil || rt.Server != srv || !reflect.DeepEqual(rt.Tools, []string{"opencode"}) {
		t.Fatalf("只有 opencode：%+v %v", rt, err)
	}
	// opencode.json 指向别的工作空间时报不一致
	c := readFile(t, filepath.Join(proj, "opencode.json"))
	os.WriteFile(filepath.Join(proj, "opencode.json"), []byte(strings.Replace(c, `"payments"`, `"other"`, 1)), 0o644)
	if _, err := readRepoTarget(proj); err == nil || !strings.Contains(err.Error(), "opencode.json") {
		t.Fatalf("应报配置不一致：%v", err)
	}
}

func TestInitToolsUnsupportedListsKnown(t *testing.T) {
	configDir(t)
	saveLogins(t, "opencode")
	_, err := run(t, "init", "--project", "--dir", t.TempDir(), "--tools", "dsh")
	if err == nil || !strings.Contains(err.Error(), "claude-code, codex, opencode") || !strings.Contains(err.Error(), `"dsh"`) {
		t.Fatalf("应列出支持的工具：%v", err)
	}
}

// examples/opencode 里的插件与 init 生成的一致（示例参数），改了模板要同步示例。
func TestOpencodeExampleInSync(t *testing.T) {
	want := opencodePluginSource([]string{"cohive", "hook", "--server", "http://localhost:8787", "--workspace", "default", "--tool", "opencode"})
	got, err := os.ReadFile(filepath.Join("..", "..", "examples", "opencode", ".opencode", "plugins", "cohive.ts"))
	if err != nil || string(got) != string(want) {
		t.Fatalf("examples/opencode/.opencode/plugins/cohive.ts 与生成结果不一致（%v）；重新生成示例", err)
	}
}

func TestInitOpencodeEnglish(t *testing.T) {
	withLang(t, i18n.EN)
	configDir(t)
	saveLogins(t, "opencode")
	proj := t.TempDir()
	out, err := run(t, "init", "--project", "--dir", proj)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"OpenCode: opencode.json, .opencode/plugins/cohive.ts", "no trust step", "Use /cohive in Claude Code and OpenCode"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应包含 %q：\n%s", want, out)
		}
	}
	if hasHan(out) {
		t.Errorf("英文输出里不应有汉字：\n%s", out)
	}
	os.WriteFile(filepath.Join(proj, "opencode.jsonc"), []byte("{}"), 0o644)
	if _, err := run(t, "init", "--project", "--dir", proj); err == nil || hasHan(err.Error()) || !strings.Contains(err.Error(), "comments") {
		t.Fatalf("英文报错：%v", err)
	}
}
