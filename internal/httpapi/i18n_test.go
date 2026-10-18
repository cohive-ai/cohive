package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// TestMain：现有测试断言中文文案，固定用中文跑；英文另有用例。
func TestMain(m *testing.M) {
	i18n.SetDefault(i18n.ZhCN)
	os.Exit(m.Run())
}

type errResp struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func decodeErr(t *testing.T, w *httptest.ResponseRecorder) errResp {
	t.Helper()
	var e errResp
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("响应不是 JSON：%v %s", err, w.Body.String())
	}
	return e
}

// 每种错误都带稳定的 code，状态码不变；code 不随语言变化。
func TestWriteErrorCodes(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{&core.SecretError{}, http.StatusBadRequest, "secret_detected"},
		{core.ErrNotAdmin, http.StatusForbidden, "forbidden"},
		{core.ErrNotServerAdmin, http.StatusForbidden, "forbidden"},
		{store.ErrAccountDisabled, http.StatusForbidden, "account_disabled"},
		{&core.ConflictError{IssueID: 1, Expected: 2, Current: 3}, http.StatusConflict, "version_conflict"},
		{&core.RetryError{Code: "40P01"}, http.StatusConflict, "version_conflict"},
		{&core.StateError{Msg: "x"}, http.StatusConflict, "conflict"},
		{store.ErrAlreadyMember, http.StatusConflict, "already_member"},
		{core.ErrJoinPending, http.StatusConflict, "join_pending"},
		{core.ErrNotMember, http.StatusNotFound, "not_member"},
		{store.ErrUnauthorized, http.StatusUnauthorized, "unauthorized"},
		{core.ErrDecided, http.StatusConflict, "decided"},
		{core.ErrInviteInvalid, http.StatusNotFound, "link_invalid"},
		{core.ErrNoDevice, http.StatusBadRequest, "no_device"},
		{store.ErrNotFound, http.StatusNotFound, "not_found"},
		{&core.InputError{Msg: "x"}, http.StatusBadRequest, "invalid_input"},
		{errors.New("boom: db password=hunter2"), http.StatusInternalServerError, "internal"},
	}
	for _, lang := range []string{"", "en"} {
		for _, c := range cases {
			h := i18n.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeError(w, c.err) }))
			r := httptest.NewRequest("GET", "/", nil)
			if lang != "" {
				r.Header.Set("Accept-Language", lang)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			e := decodeErr(t, w)
			if w.Code != c.status || e.Code != c.code {
				t.Errorf("%T(%q) lang=%q：应为 %d/%s，得到 %d/%s", c.err, c.err, lang, c.status, c.code, w.Code, e.Code)
			}
			if c.code == "internal" && strings.Contains(w.Body.String(), "hunter2") {
				t.Errorf("内部错误不应透出原文：%s", w.Body.String())
			}
		}
	}
}

// 接口报错跟随 Accept-Language，并在 Content-Language 回告；没有语言头时用服务器缺省语言。
func TestHandlerLanguage(t *testing.T) {
	h := Handler(&core.Service{}, Options{})
	for _, c := range []struct {
		accept, wantLang, wantText string
	}{
		{"en", "en", "Sign in first"},
		{"en-US,en;q=0.9", "en", "Sign in first"},
		{"zh-CN", "zh-CN", "请先登录"},
		{"", "zh-CN", "请先登录"},
	} {
		r := httptest.NewRequest("GET", "/api/v1/me", nil)
		if c.accept != "" {
			r.Header.Set("Accept-Language", c.accept)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		e := decodeErr(t, w)
		if w.Code != http.StatusUnauthorized || e.Code != "login_required" || e.Error != c.wantText {
			t.Errorf("Accept-Language=%q：得到 %d %+v", c.accept, w.Code, e)
		}
		if got := w.Header().Get("Content-Language"); got != c.wantLang {
			t.Errorf("Accept-Language=%q：Content-Language 应为 %s，得到 %q", c.accept, c.wantLang, got)
		}
	}
}

// 状态机报错（*issue.TransitionError）回 409，文字按请求语言渲染，不再当成内部错误。
func TestWriteErrorTransition(t *testing.T) {
	_, err := issue.Apply(issue.StatusOpen, issue.ActAccept, issue.Roles(true, false))
	for _, c := range []struct{ lang, want string }{
		{"en", "so accept is not allowed: only the owner can do this"},
		{"zh-CN", "只有负责人可以执行"},
	} {
		h := i18n.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeError(w, err) }))
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Language", c.lang)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		e := decodeErr(t, w)
		if w.Code != http.StatusConflict || e.Code != "conflict" || !strings.Contains(e.Error, c.want) {
			t.Errorf("%s：%d %+v", c.lang, w.Code, e)
		}
	}
}
