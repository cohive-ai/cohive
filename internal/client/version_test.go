package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/buildinfo"
)

func TestVersionWarning(t *testing.T) {
	body := `{"version":"v1.2.0","cli":{"version":"v1.2.0"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/version" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	ctx := context.Background()
	old := buildinfo.Version
	defer func() { buildinfo.Version = old }()

	buildinfo.Version = "dev"
	if w := VersionWarning(ctx, srv.URL); w != "" {
		t.Fatalf("开发构建不提醒：%s", w)
	}
	buildinfo.Version = "v1.2.0"
	if w := VersionWarning(ctx, srv.URL); w != "" {
		t.Fatalf("版本一致不提醒：%s", w)
	}
	buildinfo.Version = "v1.1.0"
	w := VersionWarning(ctx, srv.URL)
	if !strings.Contains(w, "v1.1.0") || !strings.Contains(w, "v1.2.0") || !strings.Contains(w, srv.URL+"/install.sh") {
		t.Fatalf("版本不一致应提醒并给出重装命令：%s", w)
	}
	body = `{"version":"v1.2.0","cli":null}`
	if w := VersionWarning(ctx, srv.URL); !strings.Contains(w, "v1.2.0") {
		t.Fatalf("没有安装包时按服务端版本比较：%s", w)
	}
	if w := VersionWarning(ctx, srv.URL+"/nope"); w != "" {
		t.Fatalf("读不到版本（旧服务端）时不提醒：%s", w)
	}
}
