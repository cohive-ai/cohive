package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	qrcode "github.com/skip2/go-qrcode"
)

// DeviceStart 是发起设备授权登录的结果。
type DeviceStart struct {
	RequestID  int64  `json:"request_id"`
	PollSecret string `json:"poll_secret"`
	Link       string `json:"link"`
	ExpiresIn  int    `json:"expires_in"`
}

// DevicePoll 是一次轮询的结果；Status 为 approved 时 Tokens 是各工具的令牌。
type DevicePoll struct {
	Status    string            `json:"status"`
	Member    string            `json:"member"`
	Workspace string            `json:"workspace"`
	Tokens    map[string]string `json:"tokens"`
}

// ErrDeviceGone 表示服务端已找不到这次登录请求。
var ErrDeviceGone = errors.New("这次登录请求已失效，请重新执行 cohive login")

// post 发送 JSON 请求；返回状态码，非 200 时把服务端的错误说明作为 error。
func post(ctx context.Context, server, path string, in, out any) (int, error) {
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
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

// deviceLink 是服务端可以返回的确认链接：只能是 Telegram 机器人的深链。
// 终端会打开并画出这个链接，不校验的话恶意服务可以让终端打开任意程序或文件。
var deviceLink = regexp.MustCompile(`^https://t\.me/[A-Za-z0-9_]{5,32}\?start=dl_[A-Za-z0-9_-]{1,60}$`)

// StartDevice 向服务发起设备授权登录。
func StartDevice(ctx context.Context, server string, tools []string, device, workspace string) (DeviceStart, error) {
	var st DeviceStart
	_, err := post(ctx, server, "/api/v1/device/start",
		map[string]any{"tools": tools, "device_name": device, "workspace": workspace}, &st)
	if err == nil && (!deviceLink.MatchString(st.Link) || st.PollSecret == "" || st.ExpiresIn <= 0) {
		err = errors.New("协作服务返回的登录信息不对（确认链接应是 https://t.me/ 开头的机器人链接），请确认服务地址")
	}
	return st, err
}

// PollDevice 查询一次登录结果。请求已失效时返回 ErrDeviceGone。
func PollDevice(ctx context.Context, server string, st DeviceStart) (DevicePoll, error) {
	var p DevicePoll
	code, err := post(ctx, server, "/api/v1/device/poll",
		map[string]any{"request_id": st.RequestID, "poll_secret": st.PollSecret}, &p)
	if code == http.StatusNotFound {
		return p, ErrDeviceGone
	}
	return p, err
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
