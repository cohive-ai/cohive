package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	qrcode "github.com/skip2/go-qrcode"
)

// DeviceStart 是终端发起设备授权的结果。
type DeviceStart struct {
	RequestID  int64  `json:"request_id"`
	PollSecret string `json:"poll_secret"`
	URL        string `json:"url"`
	ExpiresIn  int    `json:"expires_in"`
}

// WorkspaceTokens 是一个工作空间里各工具的令牌。
type WorkspaceTokens struct {
	Workspace string            `json:"workspace"`
	Role      string            `json:"role"`
	Tokens    map[string]string `json:"tokens"`
}

// DevicePoll 是一次轮询的结果；Status 为 approved 时带设备密钥与本人每个工作空间的令牌（只交付一次）。
type DevicePoll struct {
	Status       string            `json:"status"`
	Account      string            `json:"account"`
	DeviceID     int64             `json:"device_id"`
	DeviceSecret string            `json:"device_secret"`
	Workspaces   []WorkspaceTokens `json:"workspaces"`
}

// WorkspaceRef 是账号所在的一个工作空间。
type WorkspaceRef struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Archived bool   `json:"archived"` // 已归档（只读）：不补签令牌，初始化时不作为默认工作空间
}

// ErrDeviceGone 表示服务端已找不到这次授权。
var ErrDeviceGone = i18n.Errorf("cli.client.device_gone")

// post 发送 JSON 请求（token 非空时带令牌或设备密钥）；非 200 时把服务端的错误说明作为 error 返回。
func post(ctx context.Context, server, path, token string, in, out any) (int, error) {
	body, _ := json.Marshal(in)
	return send(ctx, http.MethodPost, server, path, token, bytes.NewReader(body), out)
}

// get 发送 GET 请求，约定同 post。
func get(ctx context.Context, server, path, token string, out any) (int, error) {
	return send(ctx, http.MethodGet, server, path, token, nil, out)
}

// ErrNotMember 表示账号不是该工作空间的成员。
var ErrNotMember = i18n.Errorf("cli.client.not_member")

func send(ctx context.Context, method, server, path, token string, body io.Reader, out any) (int, error) {
	req, err := http.NewRequestWithContext(ctx, method, server+path, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	setLang(req)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return 0, connectError(err)
	}
	defer resp.Body.Close()
	rememberLang(req, resp)
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusNoContent {
		return resp.StatusCode, nil
	}
	if resp.StatusCode == http.StatusUnauthorized && token != "" {
		return resp.StatusCode, i18n.Errorf("cli.client.unauthorized")
	}
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		if json.Unmarshal(raw, &e) != nil || e.Error == "" {
			return resp.StatusCode, unrecognized(resp.StatusCode)
		}
		return resp.StatusCode, errors.New(e.Error)
	}
	if json.Unmarshal(raw, out) != nil {
		return resp.StatusCode, unrecognized(resp.StatusCode)
	}
	return resp.StatusCode, nil
}

var authPath = regexp.MustCompile(`^/device/da_[A-Za-z0-9_-]{1,60}$`)

// StartDevice 向服务发起设备授权。授权页地址只取服务端返回的路径、拼在已知的服务地址后面，
// 终端只会打开这个服务上的授权页，不会打开服务端给的任意地址。
func StartDevice(ctx context.Context, server string, tools []string, device string) (DeviceStart, error) {
	var st DeviceStart
	if _, err := post(ctx, server, "/api/v1/device/start", "", map[string]any{"tools": tools, "device_name": device}, &st); err != nil {
		return st, err
	}
	u, err := url.Parse(st.URL)
	if err != nil || !authPath.MatchString(u.Path) || st.PollSecret == "" || st.ExpiresIn <= 0 {
		return st, i18n.Errorf("cli.client.bad_device_start")
	}
	st.URL = server + u.Path
	return st, nil
}

// PollDevice 查询一次授权结果。授权已失效时返回 ErrDeviceGone。
func PollDevice(ctx context.Context, server string, requestID int64, secret string) (DevicePoll, error) {
	var p DevicePoll
	code, err := post(ctx, server, "/api/v1/device/poll", "", map[string]any{"request_id": requestID, "poll_secret": secret}, &p)
	if code == http.StatusNotFound {
		return p, ErrDeviceGone
	}
	return p, err
}

// IssueTokens 用设备密钥为本人所在的某个工作空间签发令牌（新加入工作空间后不必重新配对）。
// 不是该工作空间成员时返回 ErrNotMember。
func IssueTokens(ctx context.Context, server, secret, workspace string, tools []string) (WorkspaceTokens, error) {
	var w WorkspaceTokens
	code, err := post(ctx, server, "/api/v1/device/tokens", secret, map[string]any{"workspace": workspace, "tools": tools}, &w)
	if code == http.StatusNotFound {
		return w, i18n.Wrap(ErrNotMember, "cli.client.workspace_not_member", i18n.D{"Workspace": workspace})
	}
	return w, err
}

// DeviceWorkspaces 用设备密钥列出本人在册的工作空间。
func DeviceWorkspaces(ctx context.Context, server, secret string) ([]WorkspaceRef, error) {
	var out struct {
		Items []WorkspaceRef `json:"items"`
	}
	_, err := get(ctx, server, "/api/v1/device/workspaces", secret, &out)
	return out.Items, err
}

// RevokeOwnDevice 用本机设备密钥吊销自己名下的另一台设备（重新配对后清理旧设备）。
func RevokeOwnDevice(ctx context.Context, server, secret string, deviceID int64) error {
	_, err := post(ctx, server, fmt.Sprintf("/api/v1/me/devices/%d/revoke", deviceID), secret, map[string]any{}, &struct{}{})
	return err
}

var oncePath = regexp.MustCompile(`^/auth/once/[A-Za-z0-9_-]{1,64}$`)

// WebLogin 用本机设备密钥（或 M5 配对的旧电脑上属于设备的令牌）换一个一次性网页登录链接。
func WebLogin(ctx context.Context, server, token string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if _, err := post(ctx, server, "/api/v1/auth/web-login", token, map[string]any{}, &out); err != nil {
		return "", err
	}
	u, err := url.Parse(out.URL)
	if err != nil || !oncePath.MatchString(u.Path) {
		return "", i18n.Errorf("cli.client.bad_login_link")
	}
	return server + u.Path, nil
}

// Pending 是尚未领取令牌的设备授权，保存在本机，中途退出后可以 cohive login --resume 继续。
type Pending struct {
	Server     string    `json:"server"`
	RequestID  int64     `json:"request_id"`
	PollSecret string    `json:"poll_secret"`
	CreatedAt  time.Time `json:"created_at"`
}

func pendingDir() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pending"), nil
}

// pendingFile 按“服务地址 + 申请编号”给每条记录一个文件，互不覆盖。
func pendingFile(dir, server string, id int64) string {
	h := sha256.Sum256([]byte(NormalizeServer(server)))
	return filepath.Join(dir, fmt.Sprintf("%x-%d.json", h[:6], id))
}

// SavePending 原子地保存一条待领取记录（仅本人可读）。
func SavePending(p Pending) error {
	dir, err := pendingDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(p)
	tmp, err := os.CreateTemp(dir, ".pending-*.json")
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
	return os.Rename(tmp.Name(), pendingFile(dir, p.Server, p.RequestID))
}

// LoadPendings 读取全部待领取记录（按创建时间先后）；格式不对的文件跳过。
func LoadPendings() ([]Pending, error) {
	dir, err := pendingDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Pending
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var p Pending
		if json.Unmarshal(b, &p) == nil && p.Server != "" && p.RequestID > 0 {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

// ClearPending 只删除这一条待领取记录。
func ClearPending(server string, id int64) {
	if dir, err := pendingDir(); err == nil {
		_ = os.Remove(pendingFile(dir, server, id))
	}
}

// QRText 把内容画成终端里的二维码：每个字符表示上下两个模块。
// 按深色背景、浅色文字的终端绘制（浅色字符画出码的白底），这样显示出来是标准的白底黑码。
func QRText(content string) (string, error) {
	q, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		return "", err
	}
	bits := q.Bitmap() // 自带四格静区
	var b strings.Builder
	for y := 0; y < len(bits); y += 2 {
		for x := range bits[y] {
			top := bits[y][x]
			bottom := y+1 < len(bits) && bits[y+1][x]
			switch {
			case top && bottom:
				b.WriteRune(' ')
			case top:
				b.WriteRune('▄')
			case bottom:
				b.WriteRune('▀')
			default:
				b.WriteRune('█')
			}
		}
		b.WriteByte('\n')
	}
	return b.String(), nil
}

// ColorizeQR 给终端二维码每一行加上“白色前景、黑色背景”的颜色，浅色背景的终端里也显示为标准的白底黑码。
// 只在输出到真实终端时使用；写进文件或管道时保持纯文本。
func ColorizeQR(qr string) string {
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(qr, "\n"), "\n") {
		b.WriteString("\x1b[97;40m")
		b.WriteString(line)
		b.WriteString("\x1b[0m\n")
	}
	return b.String()
}

// connectError 把连接失败包成可本地化的错误，去掉 url.Error 外层的方法与地址。
func connectError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	return i18n.Wrap(err, "cli.client.connect_failed")
}

// unrecognized 表示协作服务返回了不认识的内容，多半是服务地址填错了。
func unrecognized(code int) error {
	return i18n.Errorf("cli.client.unrecognized_response", i18n.D{"Code": code})
}
