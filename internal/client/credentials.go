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

	"github.com/cohive-ai/cohive/internal/i18n"
)

// Login 是一次登录得到的令牌，按“服务地址 + 工作空间 + 工具”区分。
type Login struct {
	Server    string    `json:"server"`
	Workspace string    `json:"workspace"`
	Member    string    `json:"member"` // 账号的登录名
	Tool      string    `json:"tool"`
	Token     string    `json:"token"`
	DeviceID  int64     `json:"device_id,omitempty"` // 这台电脑在服务端的设备编号；管理员手工签发的令牌为 0
	CreatedAt time.Time `json:"created_at"`
}

// Device 是这台电脑在某个协作服务上的设备密钥：证明“这是某账号的一台已批准的电脑”，
// 用来为本人所在的工作空间签发令牌、cohive web 免密登录。M5 配对的旧电脑没有。
type Device struct {
	Server    string    `json:"server"`
	Account   string    `json:"account"`
	DeviceID  int64     `json:"device_id"`
	Secret    string    `json:"secret"`
	CreatedAt time.Time `json:"created_at"`
}

// Credentials 是本机凭据文件的内容。文件权限 600，目录权限 700。
type Credentials struct {
	Devices []Device `json:"devices,omitempty"`
	Logins  []Login  `json:"logins"`
}

// Device 返回这台电脑在该服务上的设备密钥。
func (c *Credentials) Device(server string) (Device, bool) {
	server = NormalizeServer(server)
	for _, d := range c.Devices {
		if d.Server == server {
			return d, true
		}
	}
	return Device{}, false
}

// SetDevice 保存（替换）这台电脑在该服务上的设备密钥。
func (c *Credentials) SetDevice(d Device) {
	d.Server = NormalizeServer(d.Server)
	if d.CreatedAt.IsZero() {
		d.CreatedAt = time.Now()
	}
	for i, x := range c.Devices {
		if x.Server == d.Server {
			c.Devices[i] = d
			return
		}
	}
	c.Devices = append(c.Devices, d)
}

// LegacyDevice 返回 M5 配对、没有设备密钥的旧设备上的一条登录（有设备编号的令牌）。
func (c *Credentials) LegacyDevice(server string) (Login, bool) {
	server = NormalizeServer(server)
	if _, ok := c.Device(server); ok {
		return Login{}, false
	}
	for _, l := range c.Logins {
		if l.Server == server && l.DeviceID != 0 {
			return l, true
		}
	}
	return Login{}, false
}

// DropDevice 删除某台设备（已吊销）在该服务上的全部登录，返回被删掉的“工作空间/工具”。
func (c *Credentials) DropDevice(server string, deviceID int64) []string {
	server = NormalizeServer(server)
	var keep []Login
	var dropped []string
	for _, l := range c.Logins {
		if l.Server == server && l.DeviceID == deviceID {
			dropped = append(dropped, l.Workspace+"/"+l.Tool)
			continue
		}
		keep = append(keep, l)
	}
	c.Logins = keep
	var devs []Device
	for _, d := range c.Devices {
		if !(d.Server == server && d.DeviceID == deviceID) {
			devs = append(devs, d)
		}
	}
	c.Devices = devs
	return dropped
}

// ConfigDir 返回配置目录，可用 COHIVE_CONFIG_DIR 覆盖（默认 ~/.config/cohive）。
// 找不到用户主目录时报错，而不是退回当前目录（那可能是项目目录，令牌会被提交进仓库）。
func ConfigDir() (string, error) {
	if d := os.Getenv("COHIVE_CONFIG_DIR"); d != "" {
		return d, nil
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", i18n.Errorf("cli.client.no_home_dir")
	}
	return filepath.Join(home, ".config", "cohive"), nil
}

// CredentialsPath 返回凭据文件路径；无法确定时返回空字符串。
func CredentialsPath() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "credentials.json")
}

// NormalizeServer 去掉服务地址末尾的斜杠。
func NormalizeServer(s string) string {
	return strings.TrimRight(strings.TrimSpace(s), "/")
}

// LoadCredentials 读取凭据文件；文件不存在时返回空。
func LoadCredentials() (*Credentials, error) {
	dir, err := ConfigDir()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return &Credentials{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Credentials
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, i18n.Errorf("cli.client.creds_bad_format", i18n.D{"Path": CredentialsPath()})
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
	dir, err := ConfigDir()
	if err != nil {
		return err
	}
	// 只在自己新建目录时设成 700；已有目录（例如用 COHIVE_CONFIG_DIR 指定的）不改它的权限
	if _, statErr := os.Stat(dir); errors.Is(statErr, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	} else if statErr != nil {
		return statErr
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
	return os.Rename(tmp.Name(), filepath.Join(dir, "credentials.json"))
}
