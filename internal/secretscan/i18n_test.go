package secretscan

import (
	"os"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
)

// TestMain：现有测试断言中文文案，固定用中文跑；英文另有用例。
func TestMain(m *testing.M) {
	i18n.SetDefault(i18n.ZhCN)
	os.Exit(m.Run())
}

// 类别表里的键在每种语言里都要有；这些键写在表里而不是调用处，静态检查看不到。
func TestCategoryKeysComplete(t *testing.T) {
	keys := []string{"secretscan.rule.default"}
	for _, c := range categories {
		keys = append(keys, c.key)
	}
	for _, l := range []i18n.Lang{i18n.EN, i18n.ZhCN} {
		for _, k := range keys {
			if !l.Has(k) {
				t.Errorf("%s 缺键 %s", l, k)
			}
		}
	}
}

func TestCategoryIn(t *testing.T) {
	if got := CategoryIn(i18n.EN, "github-fine-grained-pat"); got != "GitHub token" {
		t.Errorf("英文类别：%q", got)
	}
	if got := CategoryIn(i18n.EN, "some-new-rule"); got != "Secret or token" {
		t.Errorf("英文兜底类别：%q", got)
	}
	if got := CategoryIn(i18n.ZhCN, "cohive-token"); got != "Cohive 令牌" {
		t.Errorf("中文类别：%q", got)
	}
	// 旧字段与旧函数固定中文，不受缺省语言影响
	i18n.SetDefault(i18n.EN)
	defer i18n.SetDefault(i18n.ZhCN)
	if c := category("github-pat"); c != "GitHub 令牌" {
		t.Errorf("category 应固定中文：%q", c)
	}
	tok := "ghp_" + pick(alnum, 36)
	got := Scan(Field{Name: "note", Text: "GITHUB_TOKEN=" + tok})
	if len(got) != 1 || got[0].Category != "GitHub 令牌" || !strings.Contains(got[0].CategoryIn(i18n.EN), "GitHub token") {
		t.Errorf("命中结果的类别：%+v", got)
	}
}
