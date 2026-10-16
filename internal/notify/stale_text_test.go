package notify_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 已废弃的入会与登录方式（群里 /join、私聊 /login 登录码、cohive link、--code、加入审批开关）
// 不应再出现在面向用户的代码与页面里。
func TestNoStaleOnboardingText(t *testing.T) {
	stale := []string{"/join", "私聊机器人发 /login", "cohive link", "--code", "加入申请", "join-approval", "领取登录码"}
	roots := []string{"../../internal", "../../cmd", "../../web/src"}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "migrations" || d.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			ext := filepath.Ext(path)
			if strings.HasSuffix(path, "_test.go") || (ext != ".go" && ext != ".ts" && ext != ".tsx") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, s := range stale {
				if strings.Contains(string(b), s) {
					t.Errorf("%s 里还有旧说法 %q", path, s)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
