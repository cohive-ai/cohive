package i18n

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestMatch(t *testing.T) {
	cases := map[string]Lang{
		"zh":                         ZhCN,
		"zh-CN":                      ZhCN,
		"zh-Hans":                    ZhCN,
		"zh-TW":                      ZhCN, // 社区没有繁体前先给简体
		"zh_CN.UTF-8":                ZhCN,
		"en_US.UTF-8":                EN,
		"en-GB":                      EN,
		"fr-FR,zh;q=0.8,en;q=0.5":    ZhCN,
		"en-US,en;q=0.9,zh-CN;q=0.8": EN,
	}
	for in, want := range cases {
		if got, ok := Match(in); !ok || got != want {
			t.Errorf("Match(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "C", "POSIX", "C.UTF-8", "ja", "de-DE", "!!"} {
		if got, ok := Match(in); ok {
			t.Errorf("Match(%q) = %q, want no match", in, got)
		}
	}
}

func TestParse(t *testing.T) {
	if l, err := Parse("zh-CN"); err != nil || l != ZhCN {
		t.Fatalf("Parse(zh-CN) = %q, %v", l, err)
	}
	if _, err := Parse("klingon"); err == nil {
		t.Fatal("Parse should reject unknown languages")
	}
}

func TestLocal(t *testing.T) {
	for _, k := range []string{"COHIVE_LANG", "LC_ALL", "LC_MESSAGES", "LANG"} {
		t.Setenv(k, "")
	}
	t.Setenv("LANG", "zh_CN.UTF-8")
	if got := Local(); got != ZhCN {
		t.Errorf("LANG=zh_CN: Local() = %q", got)
	}
	t.Setenv("COHIVE_LANG", "en")
	if got := Local(); got != EN {
		t.Errorf("COHIVE_LANG=en overrides LANG: Local() = %q", got)
	}
	t.Setenv("COHIVE_LANG", "")
	t.Setenv("LANG", "ja_JP.UTF-8")
	if got := Local(); got != EN {
		t.Errorf("LANG=ja_JP falls back to en: Local() = %q", got)
	}
}

func TestFallbackAndPlural(t *testing.T) {
	if err := load(fstest.MapFS{
		"locales/en/a.json":    {Data: []byte(`{"x.only_en": "only {{.Name}}", "x.items": {"one": "{{.Count}} item", "other": "{{.Count}} items"}}`)},
		"locales/zh-CN/a.json": {Data: []byte(`{"x.items": "{{.Count}} 项"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := load(localeFS); err != nil {
			t.Fatal(err)
		}
	})
	if got := ZhCN.T("x.only_en", D{"Name": "a"}); got != "only a" {
		t.Errorf("fallback to en: %q", got)
	}
	if got := EN.T("x.items", D{"Count": 1}); got != "1 item" {
		t.Errorf("one: %q", got)
	}
	if got := EN.T("x.items", D{"Count": 3}); got != "3 items" {
		t.Errorf("other: %q", got)
	}
	if got := ZhCN.T("x.items", D{"Count": 3}); got != "3 项" {
		t.Errorf("zh: %q", got)
	}
	if got := EN.T("x.missing"); got != "x.missing" {
		t.Errorf("missing key: %q", got)
	}
	if !EN.Has("x.only_en") || ZhCN.Has("x.only_en") || !ZhCN.Has("x.items") {
		t.Error("Has should not count fallback")
	}
	if got := Lang("ja").T("x.items", D{"Count": 2}); got != "2 items" {
		t.Errorf("unknown language falls back to en: %q", got)
	}
}

func TestErrorLocalize(t *testing.T) {
	if err := load(fstest.MapFS{
		"locales/en/a.json":    {Data: []byte(`{"e.outer": "cannot save: {{.Err}}", "e.inner": "#{{.ID}} not found"}`)},
		"locales/zh-CN/a.json": {Data: []byte(`{"e.outer": "无法保存：{{.Err}}", "e.inner": "#{{.ID}} 不存在"}`)},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = load(localeFS) })
	sentinel := errors.New("sentinel")
	inner := Wrap(sentinel, "e.inner", D{"ID": 7})
	err := Wrap(inner, "e.outer")
	if got := Message(ZhCN, err); got != "无法保存：#7 不存在" {
		t.Errorf("zh: %q", got)
	}
	if got := Message(EN, err); got != "cannot save: #7 not found" {
		t.Errorf("en: %q", got)
	}
	if !errors.Is(err, sentinel) {
		t.Error("errors.Is should see the wrapped sentinel")
	}
	if got := Message(EN, sentinel); got != "sentinel" {
		t.Errorf("plain error: %q", got)
	}
	if Wrap(nil, "e.outer") != nil {
		t.Error("Wrap(nil) should be nil")
	}
	plain := Wrap(errors.New("disk full"), "e.outer")
	if got := Message(ZhCN, plain); got != "无法保存：disk full" {
		t.Errorf("wrapping a plain error: %q", got)
	}
}

func TestMiddleware(t *testing.T) {
	var seen Lang
	h := Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = FromContext(r.Context())
		if FromResponse(w) != seen {
			t.Errorf("FromResponse = %q, context = %q", FromResponse(w), seen)
		}
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if seen != ZhCN || rec.Header().Get("Content-Language") != "zh-CN" {
		t.Errorf("seen %q, header %q", seen, rec.Header().Get("Content-Language"))
	}
	SetDefault(ZhCN)
	t.Cleanup(func() { SetDefault(EN) })
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if seen != ZhCN {
		t.Errorf("no header should use the default, got %q", seen)
	}
}
