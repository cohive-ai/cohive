package web

import (
	"bytes"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// 品牌标志只有 docs/brand 一份源文件，其他地方的副本必须和它逐字节一致。

func svgFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.svg"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(p)] = b
	}
	return out
}

func names(m map[string][]byte) []string {
	var s []string
	for k := range m {
		s = append(s, k)
	}
	sort.Strings(s)
	return s
}

// 网站用的 site/assets/brand 是 docs/brand 的副本
func TestSiteBrandMatchesDocs(t *testing.T) {
	docs := svgFiles(t, filepath.Join("..", "docs", "brand"))
	site := svgFiles(t, filepath.Join("..", "site", "assets", "brand"))
	if len(docs) == 0 {
		t.Fatal("docs/brand 里没有 SVG")
	}
	if a, b := strings.Join(names(docs), " "), strings.Join(names(site), " "); a != b {
		t.Fatalf("文件不一致：docs/brand 有 [%s]，site/assets/brand 有 [%s]", a, b)
	}
	for name, want := range docs {
		if !bytes.Equal(site[name], want) {
			t.Errorf("site/assets/brand/%s 和 docs/brand/%s 不一致，改标志时两边要一起改", name, name)
		}
	}
}

// 看板的网站图标内联在 index.html 里，内容必须是 docs/brand/favicon.svg
func TestBoardFaviconMatchesDocs(t *testing.T) {
	html, err := os.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`<link rel="icon" href="data:image/svg\+xml,([^"]+)"`).FindSubmatch(html)
	if m == nil {
		t.Fatal("index.html 里没有内联的 SVG 网站图标")
	}
	got, err := url.PathUnescape(string(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("..", "docs", "brand", "favicon.svg"))
	if err != nil {
		t.Fatal(err)
	}
	// 内联时把双引号换成了单引号，避免和 href 的引号冲突
	if strings.ReplaceAll(got, "'", `"`) != strings.TrimSpace(string(want)) {
		t.Errorf("index.html 的网站图标和 docs/brand/favicon.svg 不一致：\n%s", got)
	}
}
