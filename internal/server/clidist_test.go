package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cohive-ai/cohive/internal/clidist"
	"github.com/cohive-ai/cohive/internal/server"
)

// 安装脚本与下载接口挂在完整路由上，不被看板的兜底路由吞掉；没有附带安装包时给出明确提示。
// 显式传入“没有安装包”，结果不取决于本机是否执行过 make cli-dist。
func TestInstallRoutesNotSwallowedBySPA(t *testing.T) {
	f := newFixture(t, builtWeb)
	none, err := clidist.New(fstest.MapFS{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(f.svc, builtWeb, server.Options{CLI: none}))
	t.Cleanup(srv.Close)

	code, ct, body := get(t, srv.URL+"/install.sh", "")
	if code != http.StatusOK || !strings.HasPrefix(ct, "text/plain") || !strings.Contains(body, "#!/bin/sh") || !strings.Contains(body, "没有附带命令行安装包") {
		t.Fatalf("/install.sh 应返回提示安装包缺失的脚本：%d %s %.80s", code, ct, body)
	}
	if code, _, body := get(t, srv.URL+"/install.ps1", ""); code != http.StatusOK || strings.Contains(body, "id=root") {
		t.Fatalf("/install.ps1 应返回脚本：%d %.80s", code, body)
	}
	for _, p := range []string{"/dl/cohive/linux-amd64", "/dl/cohive/nope", "/dl/other"} {
		code, ct, body := get(t, srv.URL+p, "")
		if code != http.StatusNotFound || !strings.Contains(ct, "json") || strings.Contains(body, "id=root") {
			t.Fatalf("%s 应返回 JSON 404，不回落到页面：%d %s %.80s", p, code, ct, body)
		}
	}
	v := getJSON(t, srv.URL+"/api/v1/version", "")
	if v["version"] == nil || v["cli"] != nil {
		t.Fatalf("版本接口不需要登录，没有安装包时 cli 为 null：%v", v)
	}
	// 默认（Options.CLI 为空）用嵌入的安装包，路由同样存在
	if code, _, _ := get(t, f.url+"/install.sh", ""); code != http.StatusOK {
		t.Fatalf("默认路由也应提供安装脚本：%d", code)
	}
}
