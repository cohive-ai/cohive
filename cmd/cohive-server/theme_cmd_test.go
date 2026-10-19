package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func themeFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "theme.css")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func wantExit(t *testing.T, err error, code int) {
	t.Helper()
	got := 0
	if err != nil {
		got, _ = exitCode(err)
	}
	if got != code {
		t.Fatalf("exit code %d (err %v), want %d", got, err, code)
	}
}

func TestThemeCheck(t *testing.T) {
	out, err := runCmd(t, "theme", "check")
	wantExit(t, err, 0)
	if !strings.Contains(out, "--ink") || strings.Contains(out, "FAIL") {
		t.Errorf("default theme report:\n%s", out)
	}

	out, err = runCmd(t, "theme", "check", filepath.Join("..", "..", "examples", "themes", "harbor.css"))
	wantExit(t, err, 0)
	if strings.Contains(out, "FAIL") {
		t.Errorf("example theme report:\n%s", out)
	}

	out, err = runCmd(t, "theme", "check", themeFile(t, ":root { --bg: url(x); --brand-blue: #00f; --muted: #aaaaaa }"))
	wantExit(t, err, 1)
	for _, want := range []string{"url(", "--brand-blue", "FAIL"} {
		if !strings.Contains(out, want) {
			t.Errorf("report misses %q:\n%s", want, out)
		}
	}

	// 只有未知变量也算问题：多半是拼错了
	_, err = runCmd(t, "theme", "check", themeFile(t, ":root { --acent: #000000 }"))
	wantExit(t, err, 1)

	_, err = runCmd(t, "theme", "check", filepath.Join(t.TempDir(), "missing.css"))
	wantExit(t, err, 2)
	_, err = runCmd(t, "theme")
	wantExit(t, err, 2)
	_, err = runCmd(t, "theme", "check", "a.css", "b.css")
	wantExit(t, err, 2)
}

func TestCustomThemeAtStartup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("COHIVE_DATA_DIR", dir)
	t.Setenv("COHIVE_THEME_FILE", "")
	var out bytes.Buffer

	// 数据目录里没有 theme.css：和以前完全一样
	css, err := customTheme(&out)
	if err != nil || css != nil || out.Len() != 0 {
		t.Fatalf("no theme file: %q %v %q", css, err, out.String())
	}

	// 数据目录里的 theme.css 自动加载
	if err := os.WriteFile(filepath.Join(dir, "theme.css"), []byte(":root { --accent-fill: #1f6feb; --accent-ink: #ffffff }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if css, err = customTheme(&out); err != nil || !strings.Contains(string(css), "--accent-fill: #1f6feb;") {
		t.Fatalf("data dir theme: %q %v", css, err)
	}

	// 有被拒绝的写法：不加载，照常启动
	out.Reset()
	t.Setenv("COHIVE_THEME_FILE", themeFile(t, ":root { --bg: url(https://evil.example/x) }"))
	if css, err = customTheme(&out); err != nil || css != nil || !strings.Contains(out.String(), "NOT loaded") {
		t.Fatalf("rejected theme: %q %v %q", css, err, out.String())
	}

	// 明确指定的文件读不到：拒绝启动
	t.Setenv("COHIVE_THEME_FILE", filepath.Join(dir, "missing.css"))
	if _, err = customTheme(&out); err == nil {
		t.Fatal("a missing COHIVE_THEME_FILE must stop the server")
	}
}
