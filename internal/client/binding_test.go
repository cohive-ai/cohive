package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitRun(t *testing.T, dir string, args ...string) {
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

func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func cfgEnv(t *testing.T) {
	t.Helper()
	t.Setenv("COHIVE_CONFIG_DIR", filepath.Join(realDir(t), "cohive"))
}

func TestRepoKeyMainAndWorktreeAreSame(t *testing.T) {
	base := realDir(t)
	main := filepath.Join(base, "main")
	os.MkdirAll(main, 0o755)
	gitRun(t, main, "init", "-q", ".")
	gitRun(t, main, "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(base, "wt")
	gitRun(t, main, "worktree", "add", "-q", wt)
	os.MkdirAll(filepath.Join(wt, "a", "b"), 0o755)

	k1, ok1 := FindRepo(main)
	k2, ok2 := FindRepo(filepath.Join(wt, "a", "b"))
	if !ok1 || !ok2 {
		t.Fatal("应识别为仓库")
	}
	if k1.Key != k2.Key || k1.Key != filepath.Join(main, ".git") {
		t.Fatalf("主检出与工作树应得到同一个键：%q %q", k1.Key, k2.Key)
	}
	if k2.Root != wt {
		t.Fatalf("工作树的根应是它自己：%q", k2.Root)
	}
}

func TestRepoKeySubmoduleIsSeparate(t *testing.T) {
	base := realDir(t)
	sub := filepath.Join(base, "sub")
	parent := filepath.Join(base, "parent")
	os.MkdirAll(sub, 0o755)
	os.MkdirAll(parent, 0o755)
	gitRun(t, sub, "init", "-q", ".")
	gitRun(t, sub, "commit", "-q", "--allow-empty", "-m", "x")
	gitRun(t, parent, "init", "-q", ".")
	gitRun(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "m")
	pk, _ := FindRepo(parent)
	mk, ok := FindRepo(filepath.Join(parent, "m"))
	if !ok || mk.Key == pk.Key {
		t.Fatalf("子模块不应被认成父仓库：%q %q", mk.Key, pk.Key)
	}
}

func TestFindRepoNotGit(t *testing.T) {
	if _, ok := FindRepo(realDir(t)); ok {
		t.Fatal("非 git 目录不应识别为仓库")
	}
	if _, ok := FindRepo(filepath.Join(realDir(t), "nope")); ok {
		t.Fatal("不存在的目录不应识别为仓库")
	}
}

func TestBindingsRoundTripAndCorrupt(t *testing.T) {
	cfgEnv(t)
	b, err := LoadBindings()
	if err != nil || len(b.Items) != 0 {
		t.Fatalf("没有文件应得到空表：%v", err)
	}
	if err := UpdateBindings(func(b *Bindings) error {
		b.Set(Binding{Key: "/k1", Server: "https://a.example.com/", Workspace: "w1"})
		b.Set(Binding{Key: "/k1", Server: "https://a.example.com", Workspace: "w2"})
		b.Set(Binding{Key: "/k2", Server: "https://b.example.com", Workspace: "w3"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	b, _ = LoadBindings()
	if len(b.Items) != 2 {
		t.Fatalf("同一个键应覆盖：%+v", b.Items)
	}
	if got, _ := b.Find("/k1"); got.Workspace != "w2" || got.Server != "https://a.example.com" {
		t.Fatalf("%+v", got)
	}
	if err := os.WriteFile(BindingsPath(), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBindings(); err == nil {
		t.Fatal("损坏的绑定表应报错")
	}
	// 运行时识别把损坏当作没绑定
	if r := Resolve(realDir(t)); r.Source != "" {
		t.Fatalf("损坏时应视为没绑定：%+v", r)
	}
}

func TestResolveBindingWorktreeAndNonGit(t *testing.T) {
	cfgEnv(t)
	base := realDir(t)
	main := filepath.Join(base, "main")
	os.MkdirAll(main, 0o755)
	gitRun(t, main, "init", "-q", ".")
	gitRun(t, main, "commit", "-q", "--allow-empty", "-m", "x")
	wt := filepath.Join(base, "wt")
	gitRun(t, main, "worktree", "add", "-q", wt)

	if r := Resolve(wt); r.Source != "" {
		t.Fatalf("没绑定：%+v", r)
	}
	info, _ := FindRepo(main)
	UpdateBindings(func(b *Bindings) error {
		b.Set(Binding{Key: info.Key, Server: "https://a.example.com", Workspace: "pay"})
		return nil
	})
	r := Resolve(filepath.Join(wt))
	if r.Source != SourceBinding || r.Server != "https://a.example.com" || r.Workspace != "pay" {
		t.Fatalf("工作树应继承主检出的绑定：%+v", r)
	}

	plain := filepath.Join(base, "plain")
	os.MkdirAll(filepath.Join(plain, "x", "y"), 0o755)
	UpdateBindings(func(b *Bindings) error {
		b.Set(Binding{Key: plain, Server: "https://a.example.com", Workspace: "ops"})
		return nil
	})
	if r := Resolve(filepath.Join(plain, "x", "y")); r.Source != SourceBinding || r.Workspace != "ops" {
		t.Fatalf("非 git 目录的子目录应继承：%+v", r)
	}
	if r := Resolve(base); r.Source != "" {
		t.Fatalf("上层目录不应命中：%+v", r)
	}
}

func TestSharedFileNeedsLocalLogin(t *testing.T) {
	cfgEnv(t)
	base := realDir(t)
	repo := filepath.Join(base, "repo")
	os.MkdirAll(filepath.Join(repo, "sub"), 0o755)
	gitRun(t, repo, "init", "-q", ".")
	os.WriteFile(filepath.Join(repo, SharedFileName), []byte("server = \"https://a.example.com/\"\nworkspace = \"pay\"\n"), 0o644)

	r := Resolve(filepath.Join(repo, "sub"))
	if r.Source != "" || r.Ignored == nil || r.Ignored.Workspace != "pay" || r.Ignored.Server != "https://a.example.com" {
		t.Fatalf("没登录过不应生效，但要带回被忽略的目标：%+v", r)
	}

	c := &Credentials{}
	c.Upsert(Login{Server: "https://a.example.com", Workspace: "other", Tool: "codex", Token: "t"})
	c.Save()
	if r := Resolve(repo); r.Source != "" {
		t.Fatalf("登录的是别的工作空间也不应生效：%+v", r)
	}
	c.Upsert(Login{Server: "https://a.example.com", Workspace: "pay", Tool: "codex", Token: "t"})
	c.Save()
	r = Resolve(filepath.Join(repo, "sub"))
	if r.Source != SourceShared || r.Workspace != "pay" {
		t.Fatalf("登录过应生效：%+v", r)
	}

	// 本机绑定优先于 .cohive.toml
	info, _ := FindRepo(repo)
	UpdateBindings(func(b *Bindings) error {
		b.Set(Binding{Key: info.Key, Server: "https://a.example.com", Workspace: "mine"})
		return nil
	})
	if r := Resolve(repo); r.Source != SourceBinding || r.Workspace != "mine" {
		t.Fatalf("本机绑定应优先：%+v", r)
	}
}

func TestSharedFileMalformedIgnored(t *testing.T) {
	cfgEnv(t)
	repo := realDir(t)
	gitRun(t, repo, "init", "-q", ".")
	os.WriteFile(filepath.Join(repo, SharedFileName), []byte("this is = = bad"), 0o644)
	if r := Resolve(repo); r.Source != "" || r.Ignored != nil {
		t.Fatalf("%+v", r)
	}
}

func TestGitRemoteStripsCredentials(t *testing.T) {
	repo := realDir(t)
	gitRun(t, repo, "init", "-q", ".")
	gitRun(t, repo, "remote", "add", "origin", "https://user:secret@example.com/team/repo.git")
	info, _ := FindRepo(repo)
	got := GitRemote(info.Key)
	if got != "https://example.com/team/repo.git" || strings.Contains(got, "secret") {
		t.Fatalf("远程地址应去掉用户信息：%q", got)
	}
	if GitRemote(filepath.Join(realDir(t), ".git")) != "" {
		t.Fatal("没有配置时应为空")
	}
}

func TestAnotherCloneWithSameRemote(t *testing.T) {
	b := &Bindings{}
	b.Set(Binding{Key: "/gone/.git", Server: "s", Workspace: "w", Remote: "https://example.com/r.git"})
	live := realDir(t)
	b.Set(Binding{Key: live, Server: "s", Workspace: "w2", Remote: "https://example.com/r.git"})
	b.Set(Binding{Key: "/other", Server: "s", Workspace: "w3", Remote: "https://example.com/x.git"})
	got, ok := b.SameRemote("/me", "https://example.com/r.git")
	if !ok || got.Workspace != "w2" {
		t.Fatalf("应只返回目录仍存在的同远程绑定：%+v %v", got, ok)
	}
	if _, ok := b.SameRemote(live, "https://example.com/r.git"); ok {
		t.Fatal("不应返回自己")
	}
	if _, ok := b.SameRemote("/me", ""); ok {
		t.Fatal("远程为空不匹配")
	}
}

func TestPruneBindings(t *testing.T) {
	live := realDir(t)
	b := &Bindings{}
	b.Set(Binding{Key: live, Server: "s", Workspace: "w"})
	b.Set(Binding{Key: filepath.Join(live, "gone", ".git"), Server: "s", Workspace: "w"})
	if n := b.PruneMissing(); n != 1 || len(b.Items) != 1 {
		t.Fatalf("%d %+v", n, b.Items)
	}
}
