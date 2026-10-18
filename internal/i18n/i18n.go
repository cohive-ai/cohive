// Package i18n 提供界面文案的多语言：内嵌各语言的语言包，按语言渲染文案与报错。
//
// 语言包在 locales/<语言代码>/*.json，格式为 go-i18n v2 JSON，扁平点分键；
// 英文是翻译基准，en 与 zh-CN 的键必须齐全，其他语言缺词时回退英文。
// 方案见 docs/superpowers/plans/2026-10-18-i18n-community.md。
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync/atomic"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

// Lang 是语言代码（BCP 47），例如 en、zh-CN。
type Lang string

const (
	EN   Lang = "en"
	ZhCN Lang = "zh-CN"
)

//go:embed locales
var localeFS embed.FS

var (
	bundle     *goi18n.Bundle
	supported  []Lang
	matcher    language.Matcher
	localizers map[Lang]*goi18n.Localizer
	def        atomic.Value // Lang
)

func init() {
	if err := load(localeFS); err != nil {
		panic(err)
	}
}

func load(fsys fs.FS) error {
	b := goi18n.NewBundle(language.English)
	b.RegisterUnmarshalFunc("json", json.Unmarshal)
	dirs, err := fs.ReadDir(fsys, "locales")
	if err != nil {
		return err
	}
	var langs []Lang
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		lang := Lang(d.Name())
		if _, err := language.Parse(string(lang)); err != nil {
			return fmt.Errorf("i18n: bad locale directory %q: %w", lang, err)
		}
		files, err := fs.Glob(fsys, path.Join("locales", d.Name(), "*.json"))
		if err != nil {
			return err
		}
		for _, f := range files {
			buf, err := fs.ReadFile(fsys, f)
			if err != nil {
				return err
			}
			// go-i18n 从文件名倒数第二段取语言，这里按目录名拼出 <区域>.<语言>.json
			name := strings.TrimSuffix(path.Base(f), ".json") + "." + string(lang) + ".json"
			if _, err := b.ParseMessageFileBytes(buf, name); err != nil {
				return fmt.Errorf("i18n: %s: %w", f, err)
			}
		}
		langs = append(langs, lang)
	}
	sort.Slice(langs, func(i, j int) bool { return rank(langs[i]) < rank(langs[j]) })
	tags := make([]language.Tag, len(langs))
	locs := make(map[Lang]*goi18n.Localizer, len(langs))
	for i, l := range langs {
		tags[i] = language.MustParse(string(l))
		locs[l] = goi18n.NewLocalizer(b, string(l), string(EN))
	}
	bundle, supported, matcher, localizers = b, langs, language.NewMatcher(tags), locs
	return nil
}

// rank 让英文排第一（匹配不到时的回退）、简体中文第二，其余按代码排序。
func rank(l Lang) string {
	switch l {
	case EN:
		return "0"
	case ZhCN:
		return "1"
	}
	return "2" + string(l)
}

// Supported 返回有语言包的全部语言，英文在最前。
func Supported() []Lang { return append([]Lang(nil), supported...) }

// Match 把 Accept-Language 头、语言代码或系统区域设置（如 zh_CN.UTF-8）匹配到已有的语言；匹配不到返回 false。
func Match(prefs string) (Lang, bool) {
	prefs = normalize(prefs)
	if prefs == "" {
		return "", false
	}
	tags, _, err := language.ParseAcceptLanguage(prefs)
	if err != nil || len(tags) == 0 {
		return "", false
	}
	_, i, conf := matcher.Match(tags...)
	if conf == language.No {
		return "", false
	}
	// 匹配器对毫不相干的语言有时也给低置信度（如 tlh 配到 en）：要求主语言与偏好里的某一项一致
	want, _ := language.MustParse(string(supported[i])).Base()
	for _, t := range tags {
		if b, _ := t.Base(); b == want {
			return supported[i], true
		}
	}
	return "", false
}

// normalize 把 POSIX 区域设置（zh_CN.UTF-8、en_US@euro）转成语言代码；C 与 POSIX 视为没有设置。
func normalize(s string) string {
	s = strings.TrimSpace(s)
	if s == "C" || s == "POSIX" || strings.HasPrefix(s, "C.") {
		return ""
	}
	if !strings.ContainsAny(s, ",;") {
		if i := strings.IndexAny(s, ".@"); i >= 0 {
			s = s[:i]
		}
		s = strings.ReplaceAll(s, "_", "-")
	}
	return s
}

// Parse 解析配置里写的语言代码，必须匹配到已有的语言。
func Parse(s string) (Lang, error) {
	if l, ok := Match(s); ok {
		return l, nil
	}
	codes := make([]string, len(supported))
	for i, l := range supported {
		codes[i] = string(l)
	}
	return "", fmt.Errorf("unsupported language %q (available: %s)", s, strings.Join(codes, ", "))
}

// Pick 取账号、工作空间上保存的语言代码；为空或已没有这种语言包时用本进程的缺省语言（服务器缺省语言）。
func Pick(code string) Lang {
	if code != "" {
		if l, ok := Match(code); ok {
			return l
		}
	}
	return Default()
}

// SetDefault 设置本进程的缺省语言：服务端是服务器缺省语言，命令行是本机判断出的语言。
func SetDefault(l Lang) { def.Store(l) }

// Default 返回本进程的缺省语言，没有设置时为英文。
func Default() Lang {
	if l, ok := def.Load().(Lang); ok {
		return l
	}
	return EN
}

// Name 返回语言用它自己的文字写的名称，例如 English、简体中文。
func (l Lang) Name() string { return l.T("language.name") }
