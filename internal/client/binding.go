package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/cohive-ai/cohive/internal/i18n"
)

// 工作空间绑定：把“这个仓库（或文件夹）属于哪个工作空间”记在本机，不再写进每个目录各自的配置文件。
// 键是仓库的公共 git 目录（主检出和它的所有工作树相同）；不在 git 仓库里的文件夹以它自己的真实路径为键。

const (
	SourceBinding = "binding" // 本机绑定表
	SourceShared  = "shared"  // 仓库里可提交的 .cohive.toml（且本机已登录过）

	// SharedFileName 是仓库根可提交的绑定文件：只有服务地址和工作空间名。
	SharedFileName = ".cohive.toml"
)

// Binding 是绑定表的一项。
type Binding struct {
	Key       string `json:"key"`
	Server    string `json:"server"`
	Workspace string `json:"workspace"`
	// Remote 是 origin 的地址（已去掉用户信息），第二个克隆 init 时据此提示沿用。
	Remote string `json:"remote,omitempty"`
	// Overrides 预留给“同一仓库不同子目录绑不同工作空间”，首期不读不写。
	Overrides []DirOverride `json:"overrides,omitempty"`
}

// DirOverride 是预留的子目录覆盖（Dir 为相对仓库根的路径）。
type DirOverride struct {
	Dir       string `json:"dir"`
	Server    string `json:"server"`
	Workspace string `json:"workspace"`
}

// Bindings 是本机绑定表。
type Bindings struct {
	Version int       `json:"version"`
	Items   []Binding `json:"bindings"`
}

// BindingsPath 返回绑定表路径；无法确定时返回空字符串。
func BindingsPath() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "bindings.json")
}

// LoadBindings 读取绑定表；文件不存在时返回空表，内容损坏时返回空表和错误。
func LoadBindings() (*Bindings, error) {
	b := &Bindings{Version: 1}
	dir, err := ConfigDir()
	if err != nil {
		return b, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "bindings.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return b, nil
	}
	if err != nil {
		return b, err
	}
	if err := json.Unmarshal(raw, b); err != nil {
		return &Bindings{Version: 1}, i18n.Errorf("cli.client.bindings_bad_format", i18n.D{"Path": BindingsPath()})
	}
	for i := range b.Items {
		b.Items[i].Server = NormalizeServer(b.Items[i].Server)
	}
	return b, nil
}

func (b *Bindings) Find(key string) (Binding, bool) {
	for _, x := range b.Items {
		if x.Key == key {
			return x, true
		}
	}
	return Binding{}, false
}

// Set 新增或替换同一个键的绑定。
func (b *Bindings) Set(n Binding) {
	n.Server = NormalizeServer(n.Server)
	for i, x := range b.Items {
		if x.Key == n.Key {
			if n.Overrides == nil {
				n.Overrides = x.Overrides
			}
			b.Items[i] = n
			return
		}
	}
	b.Items = append(b.Items, n)
}

func (b *Bindings) Remove(key string) bool {
	for i, x := range b.Items {
		if x.Key == key {
			b.Items = append(b.Items[:i], b.Items[i+1:]...)
			return true
		}
	}
	return false
}

// SameRemote 找另一个仓库（键不是 self）里远程地址相同、目录还在的绑定。
func (b *Bindings) SameRemote(self, remote string) (Binding, bool) {
	if remote == "" {
		return Binding{}, false
	}
	for _, x := range b.Items {
		if x.Key == self || x.Remote != remote {
			continue
		}
		if _, err := os.Stat(x.Key); err == nil {
			return x, true
		}
	}
	return Binding{}, false
}

// PruneMissing 去掉键指向的目录已不存在的绑定，返回去掉的条数。
func (b *Bindings) PruneMissing() int {
	kept := b.Items[:0]
	n := 0
	for _, x := range b.Items {
		if _, err := os.Stat(x.Key); errors.Is(err, fs.ErrNotExist) {
			n++
			continue
		}
		kept = append(kept, x)
	}
	b.Items = kept
	return n
}

// UpdateBindings 加锁、重新读取、交给 fn 修改、原子写回，免得多个进程互相覆盖。
func UpdateBindings(fn func(*Bindings) error) error {
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	if _, statErr := os.Stat(dir); errors.Is(statErr, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	unlock, err := lockFile(filepath.Join(dir, "bindings.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	b, err := LoadBindings()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		// 损坏的绑定表由这次写入重建；原文件留一份 .bad
		_ = os.Rename(BindingsPath(), BindingsPath()+".bad")
		b = &Bindings{Version: 1}
	}
	if err := fn(b); err != nil {
		return err
	}
	b.Version = 1
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".bindings-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, "bindings.json"))
}

// lockFile 用独占创建的锁文件互斥；超过 10 秒的锁当作遗留的清掉。
func lockFile(path string) (func(), error) {
	deadline := time.Now().Add(3 * time.Second)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, err
		}
		if fi, e := os.Stat(path); e == nil && time.Since(fi.ModTime()) > 10*time.Second {
			os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, i18n.Errorf("cli.client.bindings_locked", i18n.D{"Path": path})
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// RepoInfo 是找到的仓库：Root 是工作目录的根（工作树自己的根），Key 是公共 git 目录。
type RepoInfo struct {
	Root string
	Key  string
}

// FindRepo 从 start 往上找 .git，只读文件、不执行 git 命令（钩子每次提交提示词都要跑）。
// .git 是目录：它就是公共 git 目录；是文件（工作树、子模块）：读 gitdir，再读 commondir 得到公共目录。
func FindRepo(start string) (RepoInfo, bool) {
	d, err := filepath.Abs(start)
	if err != nil {
		return RepoInfo{}, false
	}
	if r, err := filepath.EvalSymlinks(d); err == nil {
		d = r
	}
	for {
		g := filepath.Join(d, ".git")
		if fi, err := os.Lstat(g); err == nil {
			if fi.IsDir() {
				return RepoInfo{Root: d, Key: g}, true
			}
			if key, ok := commonDirFromFile(d, g); ok {
				return RepoInfo{Root: d, Key: key}, true
			}
			return RepoInfo{}, false
		}
		parent := filepath.Dir(d)
		if parent == d {
			return RepoInfo{}, false
		}
		d = parent
	}
}

func commonDirFromFile(dir, gitFile string) (string, bool) {
	raw, err := os.ReadFile(gitFile)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
	p, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return "", false
	}
	gitdir := strings.TrimSpace(p)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	common := gitdir
	if c, err := os.ReadFile(filepath.Join(gitdir, "commondir")); err == nil {
		cd := strings.TrimSpace(string(c))
		if !filepath.IsAbs(cd) {
			cd = filepath.Join(gitdir, cd)
		}
		common = cd
	}
	common = filepath.Clean(common)
	if r, err := filepath.EvalSymlinks(common); err == nil {
		common = r
	}
	return common, true
}

// GitRemote 读公共 git 目录的配置里 origin 的地址，去掉用户信息（可能带令牌）；没有时返回空。
func GitRemote(commonDir string) string {
	f, err := os.Open(filepath.Join(commonDir, "config"))
	if err != nil {
		return ""
	}
	defer f.Close()
	in := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			in = strings.EqualFold(strings.Join(strings.Fields(line), " "), `[remote "origin"]`)
			continue
		}
		if !in {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "url" {
			v = strings.TrimSpace(v)
			if u, err := url.Parse(v); err == nil && u.Scheme != "" && u.User != nil {
				u.User = nil
				return u.String()
			}
			return v
		}
	}
	return ""
}

// Target 是服务地址加工作空间。
type Target struct {
	Server    string
	Workspace string
}

// Resolved 是按目录识别出的绑定。Source 为空表示没绑定；
// 此时 Ignored 非空表示仓库里有 .cohive.toml，但本机没登录过那个服务与工作空间，所以没有采用。
type Resolved struct {
	Target
	Source  string
	Key     string
	Root    string
	Ignored *Target
}

// Resolve 识别 dir 所属的工作空间：本机绑定 > .cohive.toml（本机必须已登录过）。
// 任何读不到、损坏的情况都当作没绑定。
func Resolve(dir string) Resolved {
	var r Resolved
	abs, err := filepath.Abs(dir)
	if err != nil {
		return r
	}
	if p, err := filepath.EvalSymlinks(abs); err == nil {
		abs = p
	}
	info, isGit := FindRepo(abs)
	if isGit {
		r.Key, r.Root = info.Key, info.Root
	}
	b, _ := LoadBindings()
	if isGit {
		if x, ok := b.Find(info.Key); ok {
			r.Server, r.Workspace, r.Source = x.Server, x.Workspace, SourceBinding
			return r
		}
	} else {
		for d := abs; ; {
			if x, ok := b.Find(d); ok {
				r.Key, r.Root = d, d
				r.Server, r.Workspace, r.Source = x.Server, x.Workspace, SourceBinding
				return r
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	}
	// .cohive.toml：从 dir 往上到仓库根（非 git 目录一直到文件系统根）
	for d := abs; ; {
		if t, ok := readShared(filepath.Join(d, SharedFileName)); ok {
			if creds, err := LoadCredentials(); err == nil && creds.hasLogin(t.Server, t.Workspace) {
				r.Target, r.Source = t, SourceShared
			} else {
				r.Ignored = &t
			}
			return r
		}
		if isGit && d == info.Root {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return r
}

func readShared(path string) (Target, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Target{}, false
	}
	var f struct {
		Server    string `toml:"server"`
		Workspace string `toml:"workspace"`
	}
	if _, err := toml.Decode(string(raw), &f); err != nil {
		return Target{}, false
	}
	t := Target{Server: NormalizeServer(f.Server), Workspace: strings.TrimSpace(f.Workspace)}
	if t.Server == "" || t.Workspace == "" {
		return Target{}, false
	}
	return t, true
}

// ReadShared 读 dir 下的 .cohive.toml（不检查登录）。
func ReadShared(dir string) (Target, bool) { return readShared(filepath.Join(dir, SharedFileName)) }

func (c *Credentials) hasLogin(server, workspace string) bool {
	server = NormalizeServer(server)
	for _, l := range c.Logins {
		if l.Server == server && l.Workspace == workspace {
			return true
		}
	}
	return false
}

// ResolveAny 依次在 dirs 里识别，第一个有结果的为准；都没有时带回第一个被忽略的 .cohive.toml。
func ResolveAny(dirs ...string) Resolved {
	var ignored Resolved
	for _, d := range dirs {
		if d == "" {
			continue
		}
		r := Resolve(d)
		if r.Source != "" {
			return r
		}
		if ignored.Ignored == nil && r.Ignored != nil {
			ignored = r
		}
	}
	return ignored
}
