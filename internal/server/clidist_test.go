package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// 安装脚本与下载接口挂在完整路由上，不被看板的兜底路由吞掉；没有附带安装包时给出明确提示。
func TestInstallRoutesNotSwallowedBySPA(t *testing.T) {
	f := newFixture(t, builtWeb)
	code, ct, body := get(t, f.url+"/install.sh", "")
	if code != http.StatusOK || !strings.HasPrefix(ct, "text/plain") || !strings.Contains(body, "#!/bin/sh") || !strings.Contains(body, "cohive_install") {
		t.Fatalf("/install.sh 应返回安装脚本：%d %s %.80s", code, ct, body)
	}
	if code, _, body := get(t, f.url+"/install.ps1", ""); code != http.StatusOK || strings.Contains(body, "id=root") {
		t.Fatalf("/install.ps1 应返回脚本：%d %.80s", code, body)
	}
	code, ct, body = get(t, f.url+"/dl/cohive/linux-amd64", "")
	if code != http.StatusNotFound || !strings.Contains(ct, "json") || strings.Contains(body, "id=root") {
		t.Fatalf("/dl/ 下缺失的文件应返回 JSON 404，不回落到页面：%d %s %s", code, ct, body)
	}
	v := getJSON(t, f.url+"/api/v1/version", "")
	if v["version"] == nil {
		t.Fatalf("版本接口不需要登录：%v", v)
	}
}
