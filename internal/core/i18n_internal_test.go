package core

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
)

var coreKey = regexp.MustCompile(`^core\.[a-z0-9_]+(\.[a-z0-9_]+)+$`)

// 本包里以字面量写出的 core. 文案键（inputErr、stateErr、providerKey 的参数等不在 i18n 包的静态检查范围内）必须在中英文语言包里。
func TestCoreKeyLiteralsExist(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	n := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(node ast.Node) bool {
			lit, ok := node.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			key, _ := strconv.Unquote(lit.Value)
			if !coreKey.MatchString(key) {
				return true
			}
			n++
			for _, l := range []i18n.Lang{i18n.EN, i18n.ZhCN} {
				if !l.Has(key) {
					t.Errorf("%s: key %q is missing in locale %s", fset.Position(lit.Pos()), key, l)
				}
			}
			return true
		})
	}
	if n < 100 {
		t.Fatalf("只扫到 %d 个键，扫描方式可能失效", n)
	}
}

// 参数里的 lk（延迟翻译）在渲染时按渲染语言出，而不是创建错误时的语言。
func TestLazyProviderNameFollowsRenderLanguage(t *testing.T) {
	err := stateErr("core.err.ws_bound_unbind_rebind",
		i18n.D{"Provider": providerKey("telegram"), "Target": providerKey("dingtalk")})
	se, ok := err.(*StateError)
	if !ok {
		t.Fatalf("应为 StateError：%T", err)
	}
	if got, want := se.Localize(i18n.ZhCN), "工作空间已绑定 Telegram 群，请先解绑再绑钉钉群"; got != want {
		t.Errorf("中文 = %q，想要 %q", got, want)
	}
	if got, want := se.Localize(i18n.EN), "This workspace is already bound to a Telegram group. Unbind it before binding a DingTalk group."; got != want {
		t.Errorf("英文 = %q，想要 %q", got, want)
	}
	// 不认识的通讯工具原样给代码
	if got := providerNameIn(i18n.EN, "wechat"); got != "wechat" {
		t.Errorf("不认识的通讯工具应原样返回：%q", got)
	}
}

func TestHandoverErrorLocalizes(t *testing.T) {
	e := &HandoverError{Key: "core.err.handover_stale_issue", Data: i18n.D{"ID": 7}}
	if got := e.Localize(i18n.EN); got != "Issue #7 has new activity. Refresh the handover list and try again." {
		t.Errorf("英文 = %q", got)
	}
	if got := e.Error(); got != "问题单 #7 已有新进展，请刷新转交清单后重试" {
		t.Errorf("Error() 应用缺省语言（中文）：%q", got)
	}
}

func TestNeedsMeEnglishCoversEveryState(t *testing.T) {
	for st, key := range boardNeeds {
		if !i18n.EN.Has(key) || !i18n.ZhCN.Has(key) {
			t.Errorf("状态 %s 的看板文案 %s 缺失", st, key)
		}
	}
}
