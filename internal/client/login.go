package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
)

// CheckServer 校验服务地址形如 https://cohive.example.com。
func CheckServer(server string) (string, error) {
	server = NormalizeServer(server)
	u, err := url.Parse(server)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", i18n.Errorf("cli.client.bad_server", i18n.D{"Server": strconv.Quote(server)})
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
	setLang(req)
	resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return Login{}, connectError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return Login{}, i18n.Errorf("cli.client.token_invalid")
	}
	var out struct {
		Account   struct{ Name string } `json:"account"`
		Workspace struct{ Name string } `json:"workspace"`
		Tool      string                `json:"tool"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&out) != nil ||
		out.Account.Name == "" || out.Workspace.Name == "" {
		return Login{}, unrecognized(resp.StatusCode)
	}
	return Login{Server: server, Workspace: out.Workspace.Name, Member: out.Account.Name, Tool: out.Tool, Token: token, CreatedAt: time.Now()}, nil
}
