package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v：%v\n%s", args, err, out)
	}
}

func realTemp(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// bindDir 把 dir（非 git 目录）绑到 server 的 default 工作空间。
func bindDir(t *testing.T, key, server, ws string) {
	t.Helper()
	if err := client.UpdateBindings(func(b *client.Bindings) error {
		b.Set(client.Binding{Key: key, Server: server, Workspace: ws})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// 验收 2：没绑定时工具列表只有 cohive_status，调用返回“请执行 cohive init”。
func TestProxyUnboundOnlyStatusTool(t *testing.T) {
	configDir(t)
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Chdir(realTemp(t))
	sess, stop := startProxy(t, "--tool", "codex")
	defer stop()
	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "cohive_status" {
		t.Fatalf("没绑定时只应露出 cohive_status：%+v", tools.Tools)
	}
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "cohive_status", Arguments: map[string]any{}})
	if err != nil || !res.IsError {
		t.Fatalf("%v %+v", err, res)
	}
	if txt := res.Content[0].(*mcp.TextContent).Text; !strings.Contains(txt, "cohive init") || !strings.Contains(txt, "未绑定") {
		t.Fatalf("应提示执行 cohive init：%s", txt)
	}
}

func TestProxyUnboundMentionsIgnoredSharedFile(t *testing.T) {
	configDir(t)
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	repo := realTemp(t)
	gitIn(t, repo, "init", "-q", ".")
	os.WriteFile(filepath.Join(repo, client.SharedFileName), []byte("server = \"https://x.example.com\"\nworkspace = \"pay\"\n"), 0o644)
	t.Chdir(repo)
	sess, stop := startProxy(t, "--tool", "codex")
	defer stop()
	res, err := sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "cohive_status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	txt := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(txt, "https://x.example.com") || !strings.Contains(txt, "还没有它的登录") {
		t.Fatalf("应说明 .cohive.toml 因没登录而没采用：%s", txt)
	}
}

func TestProxyHalfArgsRejected(t *testing.T) {
	configDir(t)
	err := runProxy(context.Background(), strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}, []string{"--server", "https://x.example.com", "--tool", "codex"})
	if err == nil || !strings.Contains(err.Error(), "--tool") {
		t.Fatalf("只给 --server 不给 --workspace 应报用法错误：%v", err)
	}
}

// 验收 1：绑定一次后，工作树（含从 CLAUDE_PROJECT_DIR 指向主检出的情形）直接可用，不复制任何文件。
func TestProxyUsesBindingInWorktree(t *testing.T) {
	configDir(t)
	s := newTestServer(t)
	s.login(t, "codex")
	base := realTemp(t)
	main := filepath.Join(base, "main")
	os.MkdirAll(main, 0o755)
	gitIn(t, main, "init", "-q", ".")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(base, "wt")
	gitIn(t, main, "worktree", "add", "-q", wt)
	info, _ := client.FindRepo(main)
	bindDir(t, info.Key, s.url, "default")

	t.Setenv("CLAUDE_PROJECT_DIR", "")
	t.Chdir(wt)
	sess, stop := startProxy(t, "--tool", "codex")
	tools, err := sess.ListTools(context.Background(), nil)
	if err != nil || len(tools.Tools) < 10 {
		t.Fatalf("工作树里应连上工作空间：%v %d", err, len(tools.Tools))
	}
	stop()

	// 当前目录没绑定、但 CLAUDE_PROJECT_DIR 指向已绑定的目录
	t.Chdir(realTemp(t))
	t.Setenv("CLAUDE_PROJECT_DIR", main)
	sess, stop = startProxy(t, "--tool", "codex")
	defer stop()
	if tools, err := sess.ListTools(context.Background(), nil); err != nil || len(tools.Tools) < 10 {
		t.Fatalf("应回退到 CLAUDE_PROJECT_DIR：%v", err)
	}
}

func hookIn(t *testing.T, tool, cwd, event, session string) (string, time.Duration) {
	t.Helper()
	in := fmt.Sprintf(`{"session_id":%q,"hook_event_name":%q,"cwd":%q,"source":"startup"}`, session, event, cwd)
	var out bytes.Buffer
	start := time.Now()
	if err := runCLI(context.Background(), strings.NewReader(in), &out, []string{"hook", "--tool", tool}); err != nil {
		t.Fatal(err)
	}
	return out.String(), time.Since(start)
}

// 验收 1/2：不带参数的钩子按 stdin 的 cwd 识别；工作树里有提醒；没绑定时静默。
func TestHookResolvesFromCwd(t *testing.T) {
	f := newHookFixture(t, "codex")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	base := realTemp(t)
	main := filepath.Join(base, "main")
	os.MkdirAll(main, 0o755)
	gitIn(t, main, "init", "-q", ".")
	gitIn(t, main, "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(base, "wt")
	gitIn(t, main, "worktree", "add", "-q", wt)
	t.Chdir(realTemp(t)) // 进程当前目录与项目无关，必须靠 stdin 的 cwd

	if out, _ := hookIn(t, "codex", wt, "SessionStart", "s1"); out != "" {
		t.Fatalf("没绑定应静默：%s", out)
	}
	info, _ := client.FindRepo(main)
	bindDir(t, info.Key, f.s.url, "default")
	f.send(t)
	out, _ := hookIn(t, "codex", wt, "SessionStart", "s2")
	parseHook(t, out, "SessionStart")
}

// 验收 2：没绑定时钩子几乎不耗时（不联网、不读凭据）。
func TestHookUnboundFast(t *testing.T) {
	hookEnv(t)
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	dir := realTemp(t)
	var ds []time.Duration
	for i := 0; i < 100; i++ {
		out, d := hookIn(t, "claude-code", dir, "UserPromptSubmit", "s")
		if out != "" {
			t.Fatal(out)
		}
		ds = append(ds, d)
	}
	sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
	if p99 := ds[98]; p99 > 10*time.Millisecond {
		t.Fatalf("没绑定时 P99 应小于 10 毫秒，实际 %v", p99)
	}
}

// 迁移期间：项目里已有带参数的旧钩子，用户级钩子让位，不重复提醒。
func TestHookStepsAsideForProjectHook(t *testing.T) {
	f := newHookFixture(t, "codex")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	dir := realTemp(t)
	bindDir(t, dir, f.s.url, "default")
	f.send(t)
	if err := os.MkdirAll(filepath.Join(dir, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := "command = \"cohive hook --server https://x.example.com --workspace w --tool codex\"\n"
	if err := os.WriteFile(filepath.Join(dir, ".codex", "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _ := hookIn(t, "codex", dir, "SessionStart", "s1"); out != "" {
		t.Fatalf("项目里有旧钩子时用户级钩子应让位：%s", out)
	}
	// 另一个工具没有旧钩子，不受影响
	if !hasProjectHook("codex", []string{dir}) || hasProjectHook("claude-code", []string{dir}) {
		t.Fatal("hasProjectHook 判断不对")
	}
}

func TestHookCheckShowsResolution(t *testing.T) {
	f := newHookFixture(t, "codex")
	t.Setenv("CLAUDE_PROJECT_DIR", "")
	dir := realTemp(t)
	t.Chdir(dir)
	out, _ := run(t, "hook", "--check", "--tool", "codex")
	if !strings.Contains(out, "未绑定") {
		t.Fatalf("没绑定时应说明：%s", out)
	}
	bindDir(t, dir, f.s.url, "default")
	out, _ = run(t, "hook", "--check", "--tool", "codex")
	if !strings.Contains(out, "识别到绑定") || !strings.Contains(out, "工作空间 default") || !strings.Contains(out, "找到登录") {
		t.Fatalf("应打印识别过程：%s", out)
	}
}
