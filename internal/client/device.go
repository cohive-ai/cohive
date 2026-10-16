package client

import (
	"bytes"
	"context"
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
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// DeviceStart 是终端发起设备授权的结果。
type DeviceStart struct {
	RequestID  int64  `json:"request_id"`
	PollSecret string `json:"poll_secret"`
	URL        string `json:"url"`
	ExpiresIn  int    `json:"expires_in"`
}

// DevicePoll 是一次轮询的结果；Status 为 approved 时 Tokens 是各工具的令牌。
type DevicePoll struct {
	Status    string            `json:"status"`
	Member    string            `json:"member"`
	Workspace string            `json:"workspace"`
	DeviceID  int64             `json:"device_id"`
	Tokens    map[string]string `json:"tokens"`
}

// ErrDeviceGone 表示服务端已找不到这次授权。
var ErrDeviceGone = errors.New("这次设备授权已失效，请重新执行 cohive login")

// post 发送 JSON 请求（token 非空时带令牌）；非 200 时把服务端的错误说明作为 error 返回。
func post(ctx context.Context, server, path, token string, in, out any) (int, error) {
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return 0, fmt.Errorf("连接协作服务失败：%v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusUnauthorized && token != "" {
		return resp.StatusCode, errors.New("本机保存的令牌无效或已吊销")
	}
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		if json.Unmarshal(raw, &e) != nil || e.Error == "" {
			return resp.StatusCode, fmt.Errorf("协作服务返回了无法识别的内容（HTTP %d），请确认服务地址", resp.StatusCode)
		}
		return resp.StatusCode, errors.New(e.Error)
	}
	if json.Unmarshal(raw, out) != nil {
		return resp.StatusCode, fmt.Errorf("协作服务返回了无法识别的内容（HTTP %d），请确认服务地址", resp.StatusCode)
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
		return st, errors.New("协作服务返回的授权信息不对，请确认服务地址")
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

// AddTools 用本机已有的设备令牌为其他工具补签令牌。
func AddTools(ctx context.Context, server, token string, tools []string) (DevicePoll, error) {
	var p DevicePoll
	_, err := post(ctx, server, "/api/v1/devices/self/tools", token, map[string]any{"tools": tools}, &p)
	return p, err
}

var oncePath = regexp.MustCompile(`^/auth/once/[A-Za-z0-9_-]{1,64}$`)

// WebLogin 用本机设备令牌换一个一次性网页登录链接。
func WebLogin(ctx context.Context, server, token string) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if _, err := post(ctx, server, "/api/v1/auth/web-login", token, map[string]any{}, &out); err != nil {
		return "", err
	}
	u, err := url.Parse(out.URL)
	if err != nil || !oncePath.MatchString(u.Path) {
		return "", errors.New("协作服务返回的登录链接不对")
	}
	return server + u.Path, nil
}

// Pending 是等待管理员批准、尚未领取的设备授权，保存在本机以便稍后 cohive login --resume。
type Pending struct {
	Server     string    `json:"server"`
	RequestID  int64     `json:"request_id"`
	PollSecret string    `json:"poll_secret"`
	CreatedAt  time.Time `json:"created_at"`
}

func pendingPath() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pending.json"), nil
}

// SavePending 保存待领取的授权（仅本人可读）。
func SavePending(p Pending) error {
	path, err := pendingPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(p)
	return os.WriteFile(path, b, 0o600)
}

// LoadPending 读取待领取的授权；没有时返回 nil。
func LoadPending() (*Pending, error) {
	path, err := pendingPath()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Pending
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, nil
	}
	return &p, nil
}

// ClearPending 删除待领取的授权记录。
func ClearPending() {
	if path, err := pendingPath(); err == nil {
		_ = os.Remove(path)
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
