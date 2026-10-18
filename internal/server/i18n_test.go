package server_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/server"
)

// TestMain：现有集成测试断言接口返回的中文报错，固定服务器缺省语言为中文跑；英文另有用例。
func TestMain(m *testing.M) {
	i18n.SetDefault(i18n.ZhCN)
	os.Exit(m.Run())
}

// 看板未构建的提示页按 Accept-Language 渲染；/api 的报错带 code，并在 Content-Language 回告语言。
func TestLanguageNegotiation(t *testing.T) {
	h := server.New(&core.Service{}, fstest.MapFS{}, server.Options{})
	get := func(path, accept string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if accept != "" {
			r.Header.Set("Accept-Language", accept)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := get("/", "en"); w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "The web UI has not been built yet") {
		t.Errorf("英文提示页：%d %s", w.Code, w.Body.String())
	}
	if w := get("/", ""); !strings.Contains(w.Body.String(), "看板尚未构建") {
		t.Errorf("缺省语言（中文）提示页：%s", w.Body.String())
	}
	w := get("/api/v1/me", "en")
	if w.Code != http.StatusUnauthorized || w.Header().Get("Content-Language") != "en" ||
		!strings.Contains(w.Body.String(), `"code":"login_required"`) || !strings.Contains(w.Body.String(), "Sign in first") {
		t.Errorf("英文接口报错：%d %v %s", w.Code, w.Header(), w.Body.String())
	}
	req := httptest.NewRequest("POST", "/", nil)
	wr := httptest.NewRecorder()
	h.ServeHTTP(wr, req)
	if wr.Code != http.StatusMethodNotAllowed || !strings.Contains(wr.Body.String(), "只支持 GET") {
		t.Errorf("非 GET：%d %s", wr.Code, wr.Body.String())
	}
}
