package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/skill"
)

// 迁移：装好用户级配置后，清掉项目里旧版 cohive init 写的、没有提交进仓库的那部分。
// 只动 cohive 自己写的条目；用户自己的配置原样保留。已提交的文件（团队共用）默认不动，只提示。

// isTracked 判断文件是否被 git 跟踪（已提交或已暂存）。不在仓库里、没有 git 命令都当作没跟踪。
var isTracked = func(root, rel string) bool {
	if _, err := exec.LookPath("git"); err != nil {
		return false
	}
	cmd := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", filepath.ToSlash(rel))
	return cmd.Run() == nil
}

type migrator struct {
	root          string
	removeTracked bool
	removed       []string
	kept          []string
}

// group 处理一组必须一起动的文件：其中任何一个被跟踪（且没要求移除）就整组跳过。
func (m *migrator) group(rels []string, fn func() ([]string, error)) error {
	var existing []string
	for _, r := range rels {
		if _, err := os.Lstat(filepath.Join(m.root, filepath.FromSlash(r))); err == nil {
			existing = append(existing, r)
		}
	}
	if len(existing) == 0 {
		return nil
	}
	if !m.removeTracked {
		var tracked []string
		for _, r := range existing {
			if isTracked(m.root, r) {
				tracked = append(tracked, r)
			}
		}
		if len(tracked) > 0 {
			m.kept = append(m.kept, tracked...)
			return nil
		}
	}
	changed, err := fn()
	m.removed = append(m.removed, changed...)
	return err
}

func (m *migrator) path(rel string) string { return filepath.Join(m.root, filepath.FromSlash(rel)) }

// isOurProxyEntry：条目是 cohive init 写的转发器（命令行带 mcp-proxy 与 --server）。
func isOurProxyEntry(args []any) bool {
	var strs []string
	for _, a := range args {
		if s, ok := a.(string); ok {
			strs = append(strs, s)
		}
	}
	return len(strs) > 0 && strs[0] == "mcp-proxy" && slices.Contains(strs, "--server")
}

func (m *migrator) claude() error {
	if err := m.group([]string{".mcp.json", ".claude/settings.json"}, func() ([]string, error) {
		var changed []string
		// .mcp.json
		mcpPath := m.path(".mcp.json")
		if mcp, err := readJSONObject(mcpPath); err == nil {
			if servers, ok := mcp["mcpServers"].(map[string]any); ok {
				if e, ok := servers["cohive"].(map[string]any); ok {
					args, _ := e["args"].([]any)
					if isOurProxyEntry(args) {
						delete(servers, "cohive")
						if len(servers) == 0 {
							delete(mcp, "mcpServers")
						}
						if err := m.writeOrRemoveJSON(mcpPath, mcp); err != nil {
							return changed, err
						}
						changed = append(changed, ".mcp.json")
					}
				}
			}
		}
		// .claude/settings.json
		setPath := m.path(".claude/settings.json")
		if settings, err := readJSONObject(setPath); err == nil {
			before, _ := json.Marshal(settings)
			if setClaudeHooks(settings, "") == nil {
				if perms, ok := settings["permissions"].(map[string]any); ok {
					for _, k := range []string{"allow", "ask"} {
						dropStrings(perms, k, func(s string) bool { return strings.HasPrefix(s, "mcp__cohive__") })
					}
					if len(perms) == 0 {
						delete(settings, "permissions")
					}
				}
				dropStrings(settings, "enabledMcpjsonServers", func(s string) bool { return s == "cohive" })
				if after, _ := json.Marshal(settings); string(after) != string(before) {
					if err := m.writeOrRemoveJSON(setPath, settings); err != nil {
						return changed, err
					}
					changed = append(changed, ".claude/settings.json")
				}
			}
		}
		return changed, nil
	}); err != nil {
		return err
	}
	return m.skill(".claude/skills/cohive/SKILL.md")
}

// dropStrings 从字符串数组里删掉满足条件的项；删空了就去掉这个键。
func dropStrings(m map[string]any, key string, drop func(string) bool) {
	list, ok := m[key].([]any)
	if !ok {
		return
	}
	kept := make([]any, 0, len(list))
	for _, v := range list {
		if s, ok := v.(string); ok && drop(s) {
			continue
		}
		kept = append(kept, v)
	}
	if len(kept) == 0 {
		delete(m, key)
	} else {
		m[key] = kept
	}
}

// writeOrRemoveJSON：对象删空了就删文件（以前是 cohive 新建的），否则写回。
func (m *migrator) writeOrRemoveJSON(path string, v map[string]any) error {
	if len(v) == 0 {
		return removeAndPrune(m.root, path)
	}
	return writeJSON(path, v)
}

// removeAndPrune 删文件，再把因此变空的上级目录（不超出 root）一并删掉。
func removeAndPrune(root, path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for d := filepath.Dir(path); d != root && strings.HasPrefix(d, root); d = filepath.Dir(d) {
		if os.Remove(d) != nil { // 非空就停
			break
		}
	}
	return nil
}

func (m *migrator) codex() error {
	if err := m.group([]string{".codex/config.toml"}, func() ([]string, error) {
		path := m.path(".codex/config.toml")
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, nil
		}
		text := string(b)
		start := strings.Index(text, blockBeginPrefix)
		if start < 0 {
			return nil, nil
		}
		end := strings.Index(text[start:], blockEnd)
		if end < 0 {
			return nil, nil
		}
		end = start + end + len(blockEnd)
		if end < len(text) && text[end] == '\n' {
			end++
		}
		rest := strings.TrimRight(text[:start], "\n") + "\n" + text[end:]
		rest = strings.TrimLeft(rest, "\n")
		if strings.TrimSpace(rest) == "" {
			return []string{".codex/config.toml"}, removeAndPrune(m.root, path)
		}
		return []string{".codex/config.toml"}, writeFile(path, []byte(rest))
	}); err != nil {
		return err
	}
	return m.skill(sharedSkillPath)
}

func (m *migrator) opencode() error {
	if err := m.group([]string{opencodeConfig, opencodePlugin}, func() ([]string, error) {
		var changed []string
		cfgPath := m.path(opencodeConfig)
		if cfg, err := readOrdered(cfgPath); err == nil && len(cfg) > 0 {
			dirty := false
			if mcp, err := cfg.child("mcp", ""); err == nil && mcp.index("cohive") >= 0 {
				if ourOpencodeEntry(mcp[mcp.index("cohive")].Val) {
					mcp.remove("cohive")
					dirty = true
					if len(mcp) == 0 {
						cfg.remove("mcp")
					} else if cfg.setChild("mcp", mcp) != nil {
						return nil, nil
					}
				}
			}
			if perm, err := cfg.child("permission", ""); err == nil && len(perm) > 0 {
				n := len(perm)
				for _, t := range append(append([]string{}, allowTools...), confirmTools...) {
					perm.remove(opencodeToolPrefix + t)
				}
				if len(perm) != n {
					dirty = true
					if len(perm) == 0 {
						cfg.remove("permission")
					} else if cfg.setChild("permission", perm) != nil {
						return nil, nil
					}
				}
			}
			if dirty {
				if len(cfg) == 0 || (len(cfg) == 1 && cfg.index("$schema") == 0) {
					if err := removeAndPrune(m.root, cfgPath); err != nil {
						return changed, err
					}
				} else {
					b, err := cfg.encode()
					if err != nil {
						return changed, err
					}
					if err := writeFile(cfgPath, b); err != nil {
						return changed, err
					}
				}
				changed = append(changed, opencodeConfig)
			}
		}
		pluginPath := m.path(opencodePlugin)
		if old, err := os.ReadFile(pluginPath); err == nil && strings.HasPrefix(string(old), opencodePluginMarker) {
			if err := removeAndPrune(m.root, pluginPath); err != nil {
				return changed, err
			}
			changed = append(changed, opencodePlugin)
		}
		return changed, nil
	}); err != nil {
		return err
	}
	return m.skill(sharedSkillPath)
}

func ourOpencodeEntry(raw json.RawMessage) bool {
	var e struct {
		Command []string `json:"command"`
	}
	if json.Unmarshal(raw, &e) != nil || len(e.Command) < 2 {
		return false
	}
	return e.Command[1] == "mcp-proxy" && slices.Contains(e.Command, "--server")
}

// skill 删掉内容与 cohive init 生成的完全一样的技能文件（用户改过的不动）。
func (m *migrator) skill(rel string) error {
	return m.group([]string{rel}, func() ([]string, error) {
		path := m.path(rel)
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, nil
		}
		for _, l := range []i18n.Lang{i18n.EN, i18n.ZhCN} {
			if string(b) == string(skill.For(l)) {
				return []string{rel}, removeAndPrune(m.root, path)
			}
		}
		return nil, nil
	})
}

// cleanProjectConfig 清理 roots 里指定工具的旧项目级配置，并把结果和后续提示写到 out。
func cleanProjectConfig(out io.Writer, roots []string, tools []string, removeTracked bool) error {
	var removed, kept []string
	seen := map[string]bool{}
	for _, root := range roots {
		if seen[root] {
			continue
		}
		seen[root] = true
		m := &migrator{root: root, removeTracked: removeTracked}
		for _, t := range tools {
			var err error
			switch t {
			case "claude-code":
				err = m.claude()
			case "codex":
				err = m.codex()
			case "opencode":
				err = m.opencode()
			}
			if err != nil {
				return err
			}
		}
		removed = append(removed, uniq(m.removed)...)
		kept = append(kept, uniq(m.kept)...)
	}
	if len(removed) > 0 {
		fmt.Fprint(out, i18n.T("cli.init.migrate_removed", i18n.D{"Files": strings.Join(uniq(removed), ", ")}))
	}
	if len(kept) > 0 {
		fmt.Fprint(out, i18n.T("cli.init.migrate_tracked", i18n.D{"Files": strings.Join(uniq(kept), ", ")}))
	}
	return nil
}

func uniq(in []string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// stalePathHints 检查 .worktreeinclude 与 .git/info/exclude 里是否还留着为复制配置而加的行；有则提示可以删掉（不替用户改）。
func stalePathHints(out io.Writer, root, commonDir string) {
	needles := []string{".mcp.json", ".claude/settings.json", ".claude/skills"}
	has := func(path string) bool {
		b, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		for _, n := range needles {
			if strings.Contains(string(b), n) {
				return true
			}
		}
		return false
	}
	if root != "" && has(filepath.Join(root, ".worktreeinclude")) {
		fmt.Fprint(out, i18n.T("cli.init.stale_hint", i18n.D{"File": ".worktreeinclude"}))
	}
	if commonDir != "" && has(filepath.Join(commonDir, "info", "exclude")) {
		fmt.Fprint(out, i18n.T("cli.init.stale_hint", i18n.D{"File": ".git/info/exclude"}))
	}
}
