package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
)

// sourceDir 是本包源码目录（TestMain 切走当前目录之前的位置），读仓库里的示例文件用。
var sourceDir string

// TestMain：现有测试断言中文文案，固定用中文跑；英文另有用例。
//
// 同时把测试与开发者本机隔离：用户主目录、配置目录、缓存目录都指向临时目录，当前目录也切到空的临时目录。
// 否则在带着 .mcp.json 或本机绑定的仓库里跑测试，cohive web、绑定识别会读到真实的配置。
func TestMain(m *testing.M) {
	i18n.SetDefault(i18n.ZhCN)
	sourceDir, _ = os.Getwd()
	tmp, err := os.MkdirTemp("", "cohive-test-*")
	if err != nil {
		panic(err)
	}
	for k, v := range map[string]string{
		"HOME":                tmp,
		"XDG_CONFIG_HOME":     filepath.Join(tmp, "xdg"),
		"COHIVE_CONFIG_DIR":   filepath.Join(tmp, "cohive"),
		"COHIVE_CACHE_DIR":    filepath.Join(tmp, "cache"),
		"CLAUDE_PROJECT_DIR":  "",
		"GIT_CONFIG_NOSYSTEM": "1",
	} {
		os.Setenv(k, v)
	}
	os.Unsetenv("GIT_DIR")
	work := filepath.Join(tmp, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		panic(err)
	}
	if err := os.Chdir(work); err != nil {
		panic(err)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

func TestUsageEnglish(t *testing.T) {
	i18n.SetDefault(i18n.EN)
	defer i18n.SetDefault(i18n.ZhCN)
	var out bytes.Buffer
	err := runCLI(context.Background(), strings.NewReader(""), &out, []string{"bogus"})
	if err == nil || err.Error() != `unknown command "bogus"` {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(out.String(), "Usage: cohive <command>") {
		t.Fatalf("usage = %q", out.String())
	}
}
