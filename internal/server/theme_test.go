package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/server"
)

func getPage(t *testing.T, url string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

var themedWeb = fstest.MapFS{
	"index.html": {Data: []byte("<!doctype html><html><head><link rel=\"stylesheet\" href=\"/assets/app.css\">\n  </head><body><div id=root></div></body></html>")},
}

// 没有自定义主题时，入口页原样返回，也没有 /theme.css
func TestNoThemeLeavesBoardUnchanged(t *testing.T) {
	srv := httptest.NewServer(server.New(&core.Service{}, themedWeb, server.Options{}))
	defer srv.Close()
	_, body := getPage(t, srv.URL+"/")
	if body != string(themedWeb["index.html"].Data) {
		t.Errorf("index changed without a custom theme:\n%s", body)
	}
	resp, body := getPage(t, srv.URL+"/theme.css")
	if strings.Contains(resp.Header.Get("Content-Type"), "text/css") || strings.Contains(body, "--") {
		t.Errorf("/theme.css served without a custom theme: %s %q", resp.Header.Get("Content-Type"), body)
	}
}

// 有自定义主题时，它的样式表排在默认样式之后，并以 text/css、nosniff 提供
func TestCustomThemeIsLinkedAfterDefaultStyles(t *testing.T) {
	css := ":root {\n  --accent-fill: #1f6feb;\n}\n"
	srv := httptest.NewServer(server.New(&core.Service{}, themedWeb, server.Options{Theme: []byte(css)}))
	defer srv.Close()
	_, body := getPage(t, srv.URL+"/w/default")
	def := strings.Index(body, `href="/assets/app.css"`)
	custom := strings.Index(body, `href="/theme.css?v=`)
	head := strings.Index(body, "</head>")
	if def < 0 || custom < def || head < custom {
		t.Fatalf("custom theme must be linked after the default stylesheet and inside <head>:\n%s", body)
	}
	href := body[custom+len(`href="`):]
	href = href[:strings.Index(href, `"`)]
	resp, got := getPage(t, srv.URL+href)
	if resp.StatusCode != http.StatusOK || got != css {
		t.Errorf("GET %s = %d %q", href, resp.StatusCode, got)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q", ct)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
}
