package i18n

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// entry 是语言包里的一条：普通文案只有 other，复数文案有多种形式。
type entry struct {
	file   string
	plural bool // 值写成了复数对象
	forms  map[string]string
}

var pluralForms = []string{"zero", "one", "two", "few", "many", "other"}

// readCatalog 读一种语言的全部文件，检查格式：扁平点分键、值是字符串或复数对象、同一语言里键不重复。
func readCatalog(t *testing.T, fsys fs.FS, dir string) map[string]entry {
	t.Helper()
	files, err := fs.Glob(fsys, path.Join(dir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]entry{}
	for _, f := range files {
		buf, err := fs.ReadFile(fsys, f)
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(buf))
		if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
			t.Fatalf("%s: top level must be an object", f)
		}
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				t.Fatalf("%s: %v", f, err)
			}
			key := tok.(string)
			if !keyPattern.MatchString(key) {
				t.Errorf("%s: key %q must be lower-case dotted words, e.g. cli.login.done", f, key)
			}
			if prev, dup := out[key]; dup {
				t.Errorf("%s: key %q already defined in %s", f, key, prev.file)
			}
			var raw json.RawMessage
			if err := dec.Decode(&raw); err != nil {
				t.Fatalf("%s: %s: %v", f, key, err)
			}
			e := entry{file: path.Base(f), forms: map[string]string{}}
			var s string
			if json.Unmarshal(raw, &s) == nil {
				e.forms["other"] = s
			} else {
				e.plural = true
				var m map[string]string
				if err := json.Unmarshal(raw, &m); err != nil {
					t.Errorf("%s: %s: value must be a string or a plural object of strings", f, key)
					continue
				}
				for k, v := range m {
					switch {
					case k == "description":
					case slices.Contains(pluralForms, k):
						e.forms[k] = v
					default:
						t.Errorf("%s: %s: unknown field %q (allowed: description, %s)", f, key, k, strings.Join(pluralForms, ", "))
					}
				}
				if _, ok := e.forms["other"]; !ok {
					t.Errorf("%s: %s: plural object needs an \"other\" form", f, key)
				}
			}
			out[key] = e
		}
	}
	return out
}

var (
	keyPattern         = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)
	placeholderPattern = regexp.MustCompile(`\{\{\s*\.([A-Za-z0-9_]+)`)
)

func placeholders(e entry) []string {
	set := map[string]bool{}
	for _, v := range e.forms {
		for _, m := range placeholderPattern.FindAllStringSubmatch(v, -1) {
			set[m[1]] = true
		}
	}
	var out []string
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func localeDirs(t *testing.T, fsys fs.FS, root string) []string {
	t.Helper()
	ds, err := fs.ReadDir(fsys, root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range ds {
		if d.IsDir() {
			out = append(out, d.Name())
		}
	}
	return out
}

// TestCatalogComplete：en 与 zh-CN 的键必须完全一致；其他语言允许缺词，但不能多出英文里没有的键；
// 同一个键在各语言里的占位符必须一致；英文的复数文案要有 one 与 other。
func TestCatalogComplete(t *testing.T) {
	en := readCatalog(t, localeFS, "locales/en")
	if len(en) == 0 {
		t.Fatal("en catalog is empty")
	}
	for _, lang := range localeDirs(t, localeFS, "locales") {
		cat := readCatalog(t, localeFS, "locales/"+lang)
		for key, e := range cat {
			base, ok := en[key]
			if !ok {
				t.Errorf("%s/%s: key %q is not in the en catalog", lang, e.file, key)
				continue
			}
			if e.file != base.file {
				t.Errorf("%s: key %q is in %s, but en has it in %s", lang, key, e.file, base.file)
			}
			if got, want := placeholders(e), placeholders(base); !slices.Equal(got, want) {
				t.Errorf("%s: key %q uses placeholders %v, en uses %v", lang, key, got, want)
			}
		}
		if lang == string(EN) || lang == string(ZhCN) {
			for key, e := range en {
				if _, ok := cat[key]; !ok {
					t.Errorf("%s: missing key %q (en has it in %s); en and zh-CN must both be complete", lang, key, e.file)
				}
			}
		}
	}
	for key, e := range en {
		if e.plural {
			if _, ok := e.forms["one"]; !ok {
				t.Errorf("en: plural key %q needs a \"one\" form", key)
			}
		}
	}
}

// TestWebLocalesMatch：网页与服务端提供同一组语言。
func TestWebLocalesMatch(t *testing.T) {
	web := localeDirs(t, os.DirFS(filepath.Join("..", "..", "web", "src")), "locales")
	srv := localeDirs(t, localeFS, "locales")
	if !slices.Equal(web, srv) {
		t.Fatalf("web/src/locales has %v, internal/i18n/locales has %v; add or remove a language in both places", web, srv)
	}
}

func TestLoadedLanguages(t *testing.T) {
	got := Supported()
	if len(got) < 2 || got[0] != EN || got[1] != ZhCN {
		t.Fatalf("Supported() = %v, want en, zh-CN first", got)
	}
	for _, l := range got {
		if l.Name() == "language.name" || l.Name() == "" {
			t.Errorf("%s: missing language.name", l)
		}
	}
	if EN.Name() != "English" || ZhCN.Name() != "简体中文" {
		t.Errorf("names = %q %q", EN.Name(), ZhCN.Name())
	}
}
