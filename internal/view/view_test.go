package view_test

import (
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/view"
)

type sample struct {
	Name  string   `json:"name"`
	Items []string `json:"items"`
	Inner struct {
		Code *int `json:"code,omitempty"`
	} `json:"inner"`
}

func TestSchemaFillsDescriptions(t *testing.T) {
	s, err := view.Schema[sample](i18n.EN, map[string]string{
		"name":       "mcp.arg.title",
		"inner.code": "mcp.arg.service",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Properties["name"].Description; got != "One-line title. By default it appears in the workspace's group notification" {
		t.Errorf("name 的说明：%q", got)
	}
	if got := s.Properties["inner"].Properties["code"].Description; got != "Filter by service" {
		t.Errorf("inner.code 的说明：%q", got)
	}
}

// 说明表里的路径对不上字段（字段改了名忘了同步）必须报错，不能悄悄丢掉说明。
func TestSchemaRejectsUnknownPath(t *testing.T) {
	_, err := view.Schema[sample](i18n.EN, map[string]string{"nope": "mcp.arg.title"})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("应报路径找不到：%v", err)
	}
}

// 输出结构自带的说明表：路径都对得上，两种语言的文案键都在。
func TestIssueDescsResolve(t *testing.T) {
	for _, l := range []i18n.Lang{i18n.EN, i18n.ZhCN} {
		s, err := view.Schema[view.IssueView](l, view.IssueDescs)
		if err != nil {
			t.Fatal(err)
		}
		if d := s.Properties["version"].Description; d == "" || strings.HasPrefix(d, "mcp.") {
			t.Errorf("%s：version 的说明：%q", l, d)
		}
		if _, err := view.Schema[view.EventView](l, view.EventDescs); err != nil {
			t.Fatal(err)
		}
		for _, key := range view.IssueDescs {
			if !l.Has(key) {
				t.Errorf("%s 缺键 %s", l, key)
			}
		}
	}
}
