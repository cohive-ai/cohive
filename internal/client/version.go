package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cohive-ai/cohive/internal/buildinfo"
)

// VersionWarning 读服务端版本（/api/v1/version），与本机命令行不一致时返回一句提醒和重装命令；
// 一致、任一方是开发构建、或读不到版本（旧服务端、网络问题）时返回空串。只提醒，不阻断。
func VersionWarning(ctx context.Context, server string) string {
	if buildinfo.IsDev() {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/api/v1/version", nil)
	if err != nil {
		return ""
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ""
	}
	var v struct {
		Version string `json:"version"`
		CLI     *struct {
			Version string `json:"version"`
		} `json:"cli"`
	}
	if json.NewDecoder(resp.Body).Decode(&v) != nil {
		return ""
	}
	want := v.Version
	if v.CLI != nil {
		want = v.CLI.Version
	}
	if want == "" || want == "dev" || want == buildinfo.Version {
		return ""
	}
	return fmt.Sprintf("提醒：本机 cohive 是 %s，协作服务 %s 提供的是 %s。建议重新安装：\n"+
		"  macOS / Linux：curl -fsSL %s/install.sh | sh\n  Windows：irm %s/install.ps1 | iex",
		buildinfo.Version, server, want, server, server)
}
