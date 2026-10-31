package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/cohive-ai/cohive/internal/client"
	"github.com/cohive-ai/cohive/internal/i18n"
)

// candidateDirs 是识别绑定时依次尝试的目录：编程工具告诉钩子的 cwd、进程当前目录、CLAUDE_PROJECT_DIR。
// 桌面应用的工作树会话里，转发器的当前目录和 CLAUDE_PROJECT_DIR 是主检出目录；
// 绑定以公共 git 目录为键，主检出与工作树得到同一个结果。
func candidateDirs(first string) []string {
	var out []string
	add := func(d string) {
		if d == "" || !filepath.IsAbs(d) {
			return
		}
		for _, x := range out {
			if x == d {
				return
			}
		}
		out = append(out, d)
	}
	add(first)
	if wd, err := os.Getwd(); err == nil {
		add(wd)
	}
	add(os.Getenv("CLAUDE_PROJECT_DIR"))
	return out
}

// unboundMessage 是没有绑定时给用户（经由 cohive_status）看的说明。
func unboundMessage(r client.Resolved) string {
	if r.Ignored != nil {
		return i18n.T("cli.proxy.unbound_shared", i18n.D{"Server": r.Ignored.Server, "Workspace": r.Ignored.Workspace})
	}
	return i18n.T("cli.proxy.unbound")
}

// 各工具项目级钩子配置里“带着服务地址与工作空间参数”的特征。
var projectHookFiles = map[string]struct{ rel, needle string }{
	"claude-code": {".claude/settings.json", "hook --server"},
	"codex":       {".codex/config.toml", "hook --server"},
	"opencode":    {opencodePlugin, `"--server"`},
}

// hasProjectHook 判断项目里是否已有旧式（命令行带参数）的 cohive 钩子。有的话用户级钩子让位，免得同一条提醒出现两次。
func hasProjectHook(tool string, dirs []string) bool {
	f, ok := projectHookFiles[tool]
	if !ok {
		return false
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		roots := []string{d}
		if info, ok := client.FindRepo(d); ok {
			roots = append(roots, info.Root)
		}
		for _, r := range roots {
			if seen[r] {
				continue
			}
			seen[r] = true
			if b, err := os.ReadFile(filepath.Join(r, filepath.FromSlash(f.rel))); err == nil && strings.Contains(string(b), f.needle) {
				return true
			}
		}
	}
	return false
}
