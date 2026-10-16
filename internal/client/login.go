package client

import (
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

// Whoami 用令牌向服务端确认它属于哪个账号、工作空间与工具。
func Whoami(ctx context.Context, server, token string) (Login, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/v1/me", nil)
	if err != nil {
		return Login{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return Login{}, fmt.Errorf("连接协作服务失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return Login{}, errors.New("令牌无效或已吊销")
	}
	var out struct {
		Account   struct{ Name string } `json:"account"`
		Workspace struct{ Name string } `json:"workspace"`
		Tool      string                `json:"tool"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out) != nil ||
		out.Account.Name == "" || out.Workspace.Name == "" {
		return Login{}, fmt.Errorf("协作服务返回了无法识别的内容（HTTP %d），请确认服务地址", resp.StatusCode)
	}
	return Login{Server: server, Workspace: out.Workspace.Name, Member: out.Account.Name, Tool: out.Tool, Token: token, CreatedAt: time.Now()}, nil
}
