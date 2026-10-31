package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/cohive-ai/cohive/internal/client"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/skill"
)

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

// withLang 把命令行语言临时切换成 l，测试结束后恢复中文。
func withLang(t *testing.T, l i18n.Lang) {
	t.Helper()
	i18n.SetDefault(l)
	t.Cleanup(func() { i18n.SetDefault(i18n.ZhCN) })
}

// 会话钩子：英文提醒以 Cohive reminder 开头，给模型的要求也是英文；中文保持 Cohive 提醒。
func TestHookEnglish(t *testing.T) {
	withLang(t, i18n.EN)
	f := newHookFixture(t, "codex")
	clock := useClock(t)
	callHook(t, f.s.url, "codex", "SessionStart", "e1")
	is := f.send(t)
	clock.pass()
	out, err, _ := callHook(t, f.s.url, "codex", "UserPromptSubmit", "e1")
	if err != nil {
		t.Fatal(err)
	}
	o := parseHook(t, out, "UserPromptSubmit")
	say := fmt.Sprintf("Cohive reminder: #%d assigned to you by tester-a. Use $cohive %d to view", is.ID, is.ID)
	if !strings.HasPrefix(o.SystemMessage, "Cohive reminder: #") || !strings.HasPrefix(o.Specific.Context, "Cohive reminder (workspace default): ") ||
		!strings.Contains(o.SystemMessage, "(workspace default)") || !strings.Contains(o.Specific.Context, "not instructions for you") ||
		strings.Count(o.Specific.Context, "\n") > 1 {
		t.Fatalf("英文提醒应以 Cohive reminder 开头、不超过两行：%+v", o)
	}
	ctx := o.Specific.Context
	for _, want := range []string{"Cohive hook request: at the very end of your final reply", `write exactly "` + say + `"`,
		"even if it has nothing to do with the user's request", "do not put it at the start", "only once in this session",
		"do not call any tool that changes issue status"} {
		if !strings.Contains(ctx, want) {
			t.Fatalf("给模型的要求应包含 %q：\n%s", want, ctx)
		}
	}
	if hasHan(out) {
		t.Fatalf("英文提醒里不应有汉字：%s", out)
	}

	// 会话开始的待办摘要
	clock.pass()
	out, _, _ = callHook(t, f.s.url, "codex", "SessionStart", "e2")
	o = parseHook(t, out, "SessionStart")
	if !strings.HasPrefix(o.SystemMessage, "Cohive reminder: you have 1 pending issue: #") || !strings.HasSuffix(o.SystemMessage, "(workspace default).") ||
		!strings.Contains(o.Specific.Context, "1 issue is waiting for the user") {
		t.Fatalf("英文摘要（单数）：%+v", o)
	}
}

func pendingResult(t *testing.T, count, top int) client.Alerts {
	t.Helper()
	var items []string
	for i := 1; i <= top; i++ {
		items = append(items, fmt.Sprintf(`{"issue_id":%d,"needs":%q}`, i, string(rune('a'+i-1))))
	}
	var a client.Alerts
	body := fmt.Sprintf(`{"pending":{"count":%d,"top":[%s]}}`, count, strings.Join(items, ","))
	if err := json.Unmarshal([]byte(body), &a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestHookEnglishPluralAndMore(t *testing.T) {
	withLang(t, i18n.EN)
	got := string(renderPending("SessionStart", "w", "codex", pendingResult(t, 3, 2)))
	if !strings.Contains(got, "you have 3 pending issues: #1 a; #2 b and more (workspace w)") {
		t.Fatalf("复数与“还有更多”：%s", got)
	}
}

func TestHookCheckEnglish(t *testing.T) {
	withLang(t, i18n.EN)
	f := newHookFixture(t, "codex")
	f.send(t)
	out, err := run(t, "hook", "--check", "--server", f.s.url, "--workspace", "default", "--tool", "codex")
	if err != nil || !strings.Contains(out, "Found a login") || !strings.Contains(out, "1 pending") ||
		!strings.Contains(out, "At the start of a new session this will be inserted:") {
		t.Fatalf("--check 英文输出：%v\n%s", err, out)
	}
	out, _ = run(t, "hook", "--check", "--server", f.s.url, "--workspace", "nope", "--tool", "codex")
	if !strings.Contains(out, "This computer has no login for codex in workspace nope") {
		t.Fatalf("没有登录时应说明：%s", out)
	}
	out, _ = run(t, "hook", "--check")
	if !strings.HasPrefix(out, "Usage: cohive hook --check") {
		t.Fatalf("用法：%s", out)
	}
}

func TestInitEnglish(t *testing.T) {
	withLang(t, i18n.EN)
	configDir(t)
	saveLogins(t, "claude-code", "codex")
	proj := t.TempDir()
	out, err := run(t, "init", "--dir", proj)
	if err != nil {
		t.Fatalf("init 失败：%v\n%s", err, out)
	}
	for _, want := range []string{"Wrote the config for workspace payments (https://cohive.example.com):",
		"Claude Code: .mcp.json, .claude/settings.json", "These files contain no tokens", "In-session reminders:",
		`choose "Trust this folder"`, "Hooks need review", "Trust all and continue", "(SessionStart, UserPromptSubmit)",
		"once per computer and per clone directory", "If no reminder shows up, run cohive hook --check",
		"Next:", "Use /cohive in Claude Code and OpenCode, or $cohive in Codex"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应包含 %q：\n%s", want, out)
		}
	}
	if hasHan(out) {
		t.Errorf("英文输出里不应有汉字：\n%s", out)
	}
	if skill := readFile(t, filepath.Join(proj, ".claude", "skills", "cohive", "SKILL.md")); !strings.Contains(skill, "# Cohive collaboration") {
		t.Errorf("没有技能文件时按命令行语言写英文版：\n%.200s", skill)
	}
}

func TestInitErrorsEnglish(t *testing.T) {
	withLang(t, i18n.EN)
	configDir(t)
	if _, err := run(t, "init", "--dir", t.TempDir()); err == nil || !strings.HasPrefix(err.Error(), "This computer is not logged in yet: run cohive login") {
		t.Fatalf("没有登录：%v", err)
	}
	saveLogins(t, "codex")
	if _, err := run(t, "init", "--dir", t.TempDir(), "--tools", "vim"); err == nil || err.Error() != `--tools only supports claude-code, codex, opencode, got "vim"` {
		t.Fatalf("--tools：%v", err)
	}
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, ".codex"), 0o755)
	os.WriteFile(filepath.Join(proj, ".codex", "config.toml"), []byte("[mcp_servers.cohive]\ncommand = \"x\"\n"), 0o644)
	if _, err := run(t, "init", "--dir", proj); err == nil || !strings.Contains(err.Error(), "already has a hand-written mcp_servers.cohive entry") {
		t.Fatalf("手写配置：%v", err)
	}
	os.WriteFile(filepath.Join(proj, ".mcp.json"), []byte(`{"mcpServers": 3}`), 0o644)
	saveLogins(t, "claude-code")
	if _, err := run(t, "init", "--dir", proj, "--tools", "claude-code"); err == nil || !strings.Contains(err.Error(), "mcpServers in .mcp.json should be an object but is json.Number") {
		t.Fatalf("类型错误：%v", err)
	}
}

// 已有 cohive 的技能文件时沿用它原来的语言；没有时用命令行的语言。团队里语言不同的成员重新执行 init 不会来回改写共用的文件。
func TestInitKeepsExistingSkillLanguage(t *testing.T) {
	configDir(t)
	saveLogins(t, "claude-code", "codex")
	proj := t.TempDir()
	claude := filepath.Join(proj, ".claude", "skills", "cohive", "SKILL.md")
	agents := filepath.Join(proj, ".agents", "skills", "cohive", "SKILL.md")

	// 中文成员先执行
	if _, err := run(t, "init", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	zh := readFile(t, claude)
	if !strings.Contains(zh, "# Cohive 协作") || readFile(t, agents) != zh {
		t.Fatalf("中文命令行应写中文技能：\n%.200s", zh)
	}
	codexZh := readFile(t, filepath.Join(proj, ".codex", "config.toml"))

	// 英文成员再执行：已有中文技能，仍写中文
	withLang(t, i18n.EN)
	if _, err := run(t, "init", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	if readFile(t, claude) != zh || readFile(t, agents) != zh {
		t.Fatal("已有中文技能、命令行是英文时仍应写中文")
	}
	// Codex 配置里的标记是写进仓库的文件内容，不随语言变化
	if readFile(t, filepath.Join(proj, ".codex", "config.toml")) != codexZh {
		t.Fatal("换一种语言重新执行 init，Codex 配置不应变化")
	}

	// 反过来：已有英文技能，中文命令行仍写英文
	en := string(skill.For(i18n.EN))
	if err := os.WriteFile(claude, []byte(en), 0o644); err != nil {
		t.Fatal(err)
	}
	i18n.SetDefault(i18n.ZhCN)
	if _, err := run(t, "init", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	if readFile(t, claude) != en {
		t.Fatal("已有英文技能、命令行是中文时仍应写英文")
	}
	if readFile(t, agents) != en {
		// 另一个工具的技能文件是各自判断的：它原来是中文，保持中文
		if readFile(t, agents) != zh {
			t.Fatal("Codex 的技能文件应保持它原来的语言")
		}
	}
}

// 以前版本写的 Codex 配置块是中文开头标记：重新执行时识别并替换成现在的标记，不重复。
func TestInitReplacesOldChineseBlockMarker(t *testing.T) {
	configDir(t)
	saveLogins(t, "codex")
	proj := t.TempDir()
	os.MkdirAll(filepath.Join(proj, ".codex"), 0o755)
	old := "model = \"gpt\"\n\n# >>> cohive（由 cohive init 生成，重新执行会覆盖这一段）\n[mcp_servers.cohive]\ncommand = \"old\"\nargs = []\n# <<< cohive\n"
	os.WriteFile(filepath.Join(proj, ".codex", "config.toml"), []byte(old), 0o644)
	if _, err := run(t, "init", "--dir", proj); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(proj, ".codex", "config.toml"))
	if strings.Count(got, "# >>> cohive") != 1 || strings.Count(got, "# <<< cohive") != 1 || strings.Contains(got, `"old"`) ||
		strings.Contains(got, "由 cohive init 生成") || !strings.Contains(got, `model = "gpt"`) {
		t.Fatalf("应只剩一段新的配置块：\n%s", got)
	}
}

func TestLoginAndStatusEnglish(t *testing.T) {
	withLang(t, i18n.EN)
	configDir(t)
	out, err := run(t, "status")
	if err != nil || !strings.HasPrefix(out, "Not logged in yet.") {
		t.Fatalf("status：%v %q", err, out)
	}
	if _, err := run(t, "login", "--dir", t.TempDir()); err == nil || !strings.HasPrefix(err.Error(), "Missing --server (the Cohive server URL).") {
		t.Fatalf("缺少 --server：%v", err)
	}
	if _, err := run(t, "login", "--server", "cohive.example.com", "--dir", t.TempDir()); err == nil || !strings.HasPrefix(err.Error(), `Invalid server URL "cohive.example.com"`) {
		t.Fatalf("服务地址不对：%v", err)
	}
	if _, err := run(t, "login", "--resume"); err == nil || !strings.HasPrefix(err.Error(), "No device authorization is waiting") {
		t.Fatalf("没有待领取的授权：%v", err)
	}
	if _, err := run(t, "web", "--dir", t.TempDir()); err == nil || !strings.HasPrefix(err.Error(), "This computer is not paired") {
		t.Fatalf("web：%v", err)
	}
}

// 整个登录流程的英文输出。
func TestLoginFlowEnglish(t *testing.T) {
	withLang(t, i18n.EN)
	configDir(t)
	fastDevice(t)
	s := newTestServer(t)
	go s.approve(t, true)
	out, err := run(t, "login", "--server", s.url, "--tools", "codex", "--open=false")
	if err != nil {
		t.Fatalf("登录失败：%v\n%s", err, out)
	}
	for _, want := range []string{"Authorize this computer in your browser (valid for ", "Or scan this with your phone:", "Waiting for authorization...",
		"Logged in: account lee-codex", "Device key and tokens saved to", "Next: run cohive init in your project directory"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应包含 %q：\n%s", want, out)
		}
	}
	if hasHan(out) {
		t.Errorf("英文输出里不应有汉字：\n%s", out)
	}
	out, err = run(t, "login", "--server", s.url, "--tools", "codex,claude-code", "--open=false")
	if err != nil || !strings.Contains(out, "Issued missing tokens: default/claude-code") || !strings.Contains(out, "This computer is already logged in: ") ||
		!strings.Contains(out, "Add --force to pair again.") {
		t.Fatalf("补登：%v\n%s", err, out)
	}
	status, _ := run(t, "status")
	if !strings.Contains(status, "Device ID") || !strings.Contains(status, "Paired at") || !strings.Contains(status, "Logged in at") {
		t.Fatalf("status：%s", status)
	}
}

func TestProxyUnavailableEnglish(t *testing.T) {
	withLang(t, i18n.EN)
	configDir(t)
	var out strings.Builder
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n" + "not json\n")
	err := runProxy(t.Context(), in, &out, &strings.Builder{},
		[]string{"--server", "https://cohive.example.com", "--workspace", "default", "--tool", "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "This computer is not logged in to workspace default on https://cohive.example.com (tool codex)") ||
		!strings.Contains(out.String(), "Parse error: the input is not valid JSON") || hasHan(out.String()) {
		t.Fatalf("转发器的提示应是英文：%s", out.String())
	}
	if err := runProxy(t.Context(), strings.NewReader(""), &out, &strings.Builder{}, nil); err == nil ||
		!strings.HasPrefix(err.Error(), "mcp-proxy needs --tool") {
		t.Fatalf("缺少参数：%v", err)
	}
}
