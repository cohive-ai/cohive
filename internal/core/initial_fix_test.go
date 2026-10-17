package core_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// B2：别的账号被重置密码后改密码，不能删掉初始管理员的密码文件；初始管理员本人改密后才删。
func TestInitialPasswordFileOnlyRemovedByInitialAdmin(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	e.svc.DataDir = filepath.Join(t.TempDir(), "data")
	path, err := e.svc.EnsureInitialAdmin(ctx)
	if err != nil || path == "" {
		t.Fatalf("初始管理员：%q %v", path, err)
	}
	other, _ := store.AccountByID(ctx, e.svc.Pool, e.a.AccountID)
	tmp, err := e.svc.ResetPassword(ctx, core.ServerCLI(), other.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ChangePassword(ctx, other, "", tmp, "other-new-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("别的账号改密码不应删除初始密码文件：%v", err)
	}
	admin, err := store.AccountByName(ctx, e.svc.Pool, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.ChangePassword(ctx, admin, "", initialPw(t, path), "admin-new-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("初始管理员改密码后应删除文件：%v", err)
	}
}

func initialPw(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range splitLines(string(b)) {
		if len(line) > len("密码：") && line[:len("密码：")] == "密码：" {
			return line[len("密码："):]
		}
	}
	t.Fatal("文件里没有密码")
	return ""
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
