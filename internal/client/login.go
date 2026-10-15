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
	"time"
)

// CheckServer 校验服务地址形如 https://cohive.example.com。
func CheckServer(server string) (string, error) {
	server = NormalizeServer(server)
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("服务地址 %q 不对：应形如 https://cohive.example.com（带 http:// 或 https://）", server)
	}
	return server, nil
}

// Exchange 用登录码向服务换取令牌。
func Exchange(ctx context.Context, server, code, tool string) (Login, error) {
	body, _ := json.Marshal(map[string]string{"code": code, "tool": tool})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/api/v1/login", bytes.NewReader(body))
	if err != nil {
		return Login{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return Login{}, fmt.Errorf("连接协作服务失败：%v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out struct {
		Token, Member, Workspace, Tool, Error string
	}
	if json.Unmarshal(raw, &out) != nil {
		return Login{}, fmt.Errorf("协作服务返回了无法识别的内容（HTTP %d），请确认服务地址", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK || out.Token == "" {
		if out.Error == "" {
			out.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return Login{}, errors.New(out.Error)
	}
	return Login{Server: server, Workspace: out.Workspace, Member: out.Member, Tool: out.Tool, Token: out.Token, CreatedAt: time.Now()}, nil
}
