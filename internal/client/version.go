package client

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cohive-ai/cohive/internal/buildinfo"
	"github.com/cohive-ai/cohive/internal/i18n"
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
	setLang(req)
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
	return i18n.T("cli.client.version_warning", i18n.D{"Local": buildinfo.Version, "Server": server, "Want": want})
}
