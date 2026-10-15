// Package client 是成员本机 cohive 命令行使用的公共代码：凭据文件、登录、转发。
package client

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Login 是一次登录得到的令牌，按“服务地址 + 工作空间 + 工具”区分。
type Login struct {
	Server    string    `json:"server"`
	Workspace string    `json:"workspace"`
	Member    string    `json:"member"`
	Tool      string    `json:"tool"`
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
}

// Credentials 是本机凭据文件的内容。文件权限 600，目录权限 700。
type Credentials struct {
	Logins []Login `json:"logins"`
}

// ConfigDir 返回配置目录，可用 COHIVE_CONFIG_DIR 覆盖（默认 ~/.config/cohive）。
func ConfigDir() string {
	if d := os.Getenv("COHIVE_CONFIG_DIR"); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".cohive"
	}
	return filepath.Join(home, ".config", "cohive")
}

// CredentialsPath 返回凭据文件路径。
func CredentialsPath() string {
	return filepath.Join(ConfigDir(), "credentials.json")
}

// NormalizeServer 去掉服务地址末尾的斜杠。
func NormalizeServer(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// LoadCredentials 读取凭据文件；文件不存在时返回空。
func LoadCredentials() (*Credentials, error) {
	b, err := os.ReadFile(CredentialsPath())
	if errors.Is(err, fs.ErrNotExist) {
		return &Credentials{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, errors.New("凭据文件格式不对：" + CredentialsPath())
	}
	return &c, nil
}

// Upsert 新增或替换同一服务、工作空间、工具的登录。
func (c *Credentials) Upsert(l Login) {
	l.Server = NormalizeServer(l.Server)
	if l.CreatedAt.IsZero() {
		l.CreatedAt = time.Now()
	}
	for i, x := range c.Logins {
		if x.Server == l.Server && x.Workspace == l.Workspace && x.Tool == l.Tool {
			c.Logins[i] = l
			return
		}
	}
	c.Logins = append(c.Logins, l)
}

// Find 按服务、工作空间、工具查找登录。
func (c *Credentials) Find(server, workspace, tool string) (Login, bool) {
	server = NormalizeServer(server)
	for _, l := range c.Logins {
		if l.Server == server && l.Workspace == workspace && l.Tool == tool {
			return l, true
		}
	}
	return Login{}, false
}

// Save 以只有本人可读的权限原子写入凭据文件。
func (c *Credentials) Save() error {
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".credentials-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), CredentialsPath())
}
