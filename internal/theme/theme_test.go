package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/web"
)

func spec(t *testing.T) *Spec {
	t.Helper()
	s, err := LoadSpec()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func defaults(t *testing.T) Theme {
	t.Helper()
	d, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func failingIDs(rs []Result) []string {
	out := []string{}
	for _, r := range rs {
		if !r.Pass() {
			out = append(out, r.ID())
		}
	}
	return out
}

// 变量清单必须和默认主题定义的变量一一对应
func TestSpecMatchesDefaultTheme(t *testing.T) {
	s, d := spec(t), defaults(t)
	var all []string
	for _, m := range regexp.MustCompile(`(?m)^\s*--([a-z-]+):`).FindAllStringSubmatch(string(web.DefaultTheme()), -1) {
		all = append(all, m[1])
	}
	seen := map[string]bool{}
	var defined []string
	for _, n := range all {
		if !seen[n] {
			seen[n] = true
			defined = append(defined, n)
		}
	}
	var listed []string
	for _, tok := range s.Tokens {
		listed = append(listed, tok.Name)
		if _, ok := d.Light[tok.Name]; !ok {
			t.Errorf("--%s is in theme-spec.json but not in the :root block of theme.css", tok.Name)
		}
		if v, ok := d.Light[tok.Name]; ok {
			if _, valid := s.normalize(tok.Kind, v); !valid {
				t.Errorf("default value of --%s (%s) is not a valid %s", tok.Name, v, tok.Kind)
			}
		}
		if tok.Kind == "color" && tok.Name != "brand-yellow" {
			if _, ok := d.Dark[tok.Name]; !ok {
				t.Errorf("color --%s has no dark value", tok.Name)
			}
		}
	}
	sort.Strings(defined)
	sort.Strings(listed)
	if !reflect.DeepEqual(defined, listed) {
		t.Errorf("theme.css defines %v, theme-spec.json lists %v", defined, listed)
	}
}

func TestDefaultThemePasses(t *testing.T) {
	s := spec(t)
	if f := failingIDs(s.Check(defaults(t), Theme{})); len(f) > 0 {
		t.Errorf("default theme fails %v", f)
	}
}

// 与 web/src/contrast.test.ts 共用的用例：两边必须报出完全相同的不达标项
func TestSharedContrastCases(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "web", "theme-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v struct {
		Cases []struct {
			Name    string            `json:"name"`
			Light   map[string]string `json:"light"`
			Dark    map[string]string `json:"dark"`
			Failing []string          `json:"failing"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Cases) < 5 {
		t.Fatalf("only %d shared cases", len(v.Cases))
	}
	s, d := spec(t), defaults(t)
	for _, c := range v.Cases {
		got := failingIDs(s.Check(d, Theme{Light: c.Light, Dark: c.Dark}))
		want := c.Failing
		if want == nil {
			want = []string{}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: failing %v, want %v", c.Name, got, want)
		}
	}
}

func TestForbidden(t *testing.T) {
	for _, v := range []string{
		"url(https://evil.example/x.png)", "URL(x)", "#fff url(x)", "image-set(x 1x)", "expression(alert(1))",
		"javascript:alert(1)", "@import", "\\75rl(x)", "<script>", `"x"`, "'x'", "red}body{color:red", "#fff;",
		"#fff !important", "/* x */", "attr(title)", "env(safe-area-inset-top)", "#fff\x01",
	} {
		if _, bad := Forbidden(v); !bad {
			t.Errorf("Forbidden(%q) = false", v)
		}
	}
	for _, v := range []string{"#ffcc00", "rgb(0 0 0 / 0.4)", "2px 2px 0 var(--ink)", "4px", "none"} {
		if part, bad := Forbidden(v); bad {
			t.Errorf("Forbidden(%q) rejected %q", v, part)
		}
	}
}

func codes(ps []Problem) []string {
	out := []string{}
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func TestParse(t *testing.T) {
	s := spec(t)
	cases := []struct {
		name     string
		src      string
		rejected []string
		unknown  int
		light    map[string]string
		dark     map[string]string
	}{
		{name: "light and dark blocks", src: ":root { --bg: #FFF; --accent-fill: #1F6FEB }\n:root[data-theme=\"dark\"] { --bg: #101010; }",
			light: map[string]string{"bg": "#ffffff", "accent-fill": "#1f6feb"}, dark: map[string]string{"bg": "#101010"}},
		{name: "short selectors and single quotes", src: "[data-theme='dark'] { --ink: #eeeeee; } [data-theme=\"light\"] { --ink: #111 }",
			light: map[string]string{"ink": "#111111"}, dark: map[string]string{"ink": "#eeeeee"}},
		{name: "comments are fine", src: "/* brand */\n:root { /* text */ --ink: #222222; }", light: map[string]string{"ink": "#222222"}},
		{name: "shapes", src: ":root { --radius-control: 0; --radius-panel: 6px; --hard-shadow: none; --focus-ring: 0 0 0 3px var(--accent); --backdrop: rgb(0 0 0 / 0.5) }",
			light: map[string]string{"radius-control": "0", "radius-panel": "6px", "hard-shadow": "none", "focus-ring": "0 0 0 3px var(--accent)", "backdrop": "rgb(0 0 0 / 0.5)"}},
		{name: "url() is rejected", src: ":root { --bg: url(https://evil.example/x.png); }", rejected: []string{"forbidden"}},
		{name: "url() in a shadow is rejected", src: ":root { --hard-shadow: 0 0 0 url(x); }", rejected: []string{"forbidden"}},
		{name: "@import is rejected", src: "@import url(evil.css);\n:root { --bg: #fff }", rejected: []string{"at_rule"}, light: map[string]string{"bg": "#ffffff"}},
		{name: "@media is rejected", src: "@media print { :root { --bg: #fff } }", rejected: []string{"at_rule"}},
		{name: "non-variable property is rejected", src: ":root { color: red; --bg: #fff; }", rejected: []string{"property"}, light: map[string]string{"bg": "#ffffff"}},
		{name: "other selectors are rejected", src: "body { --bg: #fff } :root, body { --bg: #000 }", rejected: []string{"selector", "selector"}},
		{name: "breaking out of a declaration is rejected", src: ":root { --bg: #fff; } } body { background: url(x) }", rejected: []string{"syntax", "selector"}, light: map[string]string{"bg": "#ffffff"}},
		{name: "expression is rejected", src: ":root { --bg: expression(alert(1)) }", rejected: []string{"forbidden"}},
		{name: "!important is rejected", src: ":root { --bg: #fff !important }", rejected: []string{"forbidden"}},
		{name: "a word where a color belongs is rejected", src: ":root { --bg: red }", rejected: []string{"value"}},
		{name: "unknown variable in a shadow is rejected", src: ":root { --hard-shadow: 2px 2px 0 var(--nope) }", rejected: []string{"value"}},
		{name: "nested blocks are rejected", src: ":root { --bg: #fff; .x { --bg: #000 } }", rejected: []string{"syntax"}},
		{name: "unclosed comment is rejected", src: ":root { --bg: #fff } /* open", rejected: []string{"syntax"}},
		{name: "unknown variables are only reported", src: ":root { --brand-blue: #0000ff; --bg: #fff }", unknown: 1, light: map[string]string{"bg": "#ffffff"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := s.Parse([]byte(c.src))
			want := c.rejected
			if want == nil {
				want = []string{}
			}
			if got := codes(p.Rejected); !reflect.DeepEqual(got, want) {
				t.Errorf("rejected %v (%+v), want %v", got, p.Rejected, want)
			}
			if len(p.Unknown) != c.unknown {
				t.Errorf("unknown %v, want %d", p.Unknown, c.unknown)
			}
			for mode, want := range map[string]map[string]string{"light": c.light, "dark": c.dark} {
				got := p.Theme.Light
				if mode == "dark" {
					got = p.Theme.Dark
				}
				if want == nil {
					want = map[string]string{}
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s = %v, want %v", mode, got, want)
				}
			}
		})
	}
}

func TestParseReportsLines(t *testing.T) {
	p := spec(t).Parse([]byte("/* a\n b */\n:root {\n  --bg: #fff;\n  --bg2: #fff;\n  color: red;\n}\n"))
	if len(p.Unknown) != 1 || p.Unknown[0].Line != 5 {
		t.Errorf("unknown = %+v, want line 5", p.Unknown)
	}
	if len(p.Rejected) != 1 || p.Rejected[0].Line != 6 {
		t.Errorf("rejected = %+v, want line 6", p.Rejected)
	}
}

func TestTooLarge(t *testing.T) {
	s := spec(t)
	src := ":root { --bg: #fff; }\n" + strings.Repeat(" ", s.MaxFileBytes)
	if got := codes(s.Parse([]byte(src)).Rejected); !reflect.DeepEqual(got, []string{"too_large"}) {
		t.Errorf("rejected %v, want too_large", got)
	}
}

// 生成的样式表只有固定的选择器和规范化后的取值；深色同时写给“跟随系统”和“手动选深色”
func TestCSS(t *testing.T) {
	s := spec(t)
	p := s.Parse([]byte("/* <script> */ :root { --ink: #111; --bg: #FAFAFA }\n[data-theme=dark] { --bg: #000 }"))
	got := string(s.CSS(p.Theme))
	want := "/* Custom theme, regenerated by Cohive from the server's theme file */\n" +
		":root {\n  --bg: #fafafa;\n  --ink: #111111;\n}\n" +
		"@media (prefers-color-scheme: dark) {\n  :root:not([data-theme=\"light\"]) {\n    --bg: #000000;\n  }\n}\n" +
		":root[data-theme=\"dark\"] {\n  --bg: #000000;\n}\n"
	if got != want {
		t.Errorf("CSS =\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(got, "script") {
		t.Error("comments from the source leaked into the generated CSS")
	}
}

func TestLoad(t *testing.T) {
	s, d := spec(t), defaults(t)
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	l, err := Load(s, d, write("ok.css", ":root { --accent-fill: #1f6feb; --accent-ink: #ffffff }"))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.CSS) == 0 || !strings.Contains(string(l.CSS), "--accent-fill: #1f6feb;") {
		t.Errorf("CSS = %q", l.CSS)
	}
	l, err = Load(s, d, write("bad.css", ":root { --bg: url(x) }"))
	if err != nil {
		t.Fatal(err)
	}
	if l.CSS != nil {
		t.Errorf("a file with rejected parts must not be loaded, got %q", l.CSS)
	}
	big := write("big.css", ":root{--bg:#fff}"+strings.Repeat(" ", s.MaxFileBytes))
	if l, err = Load(s, d, big); err != nil || l.CSS != nil || len(l.Parsed.Rejected) != 1 {
		t.Errorf("oversized file: %v %+v", err, l)
	}
	if _, err := Load(s, d, dir); err == nil {
		t.Error("a directory must not be accepted")
	}
	if _, err := Load(s, d, filepath.Join(dir, "missing.css")); err == nil {
		t.Error("a missing file must be an error")
	}
}

// 指南里给的示例主题必须能通过检查
func TestExampleThemePasses(t *testing.T) {
	s, d := spec(t), defaults(t)
	l, err := Load(s, d, filepath.Join("..", "..", "examples", "themes", "harbor.css"))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Parsed.Rejected) > 0 || len(l.Parsed.Unknown) > 0 {
		t.Errorf("example theme: rejected %+v unknown %+v", l.Parsed.Rejected, l.Parsed.Unknown)
	}
	if f := failingIDs(l.Results); len(f) > 0 {
		t.Errorf("example theme fails %v", f)
	}
}

// 中英两份主题指南都要列出全部变量
func TestGuidesListEveryToken(t *testing.T) {
	s := spec(t)
	for _, g := range []string{"theming.md", "theming.en.md"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "docs", "guides", g))
		if err != nil {
			t.Fatal(err)
		}
		for _, tok := range s.Tokens {
			if !strings.Contains(string(b), "`--"+tok.Name+"`") {
				t.Errorf("%s does not document --%s", g, tok.Name)
			}
		}
	}
}

// 层叠：浅色块里写的变量，默认深色块没定义的（圆角、阴影）在深色主题也生效；深色块定义了的不受影响
func TestResolveCascade(t *testing.T) {
	d := defaults(t)
	custom := Theme{Light: map[string]string{"radius-panel": "8px", "bg": "#fafafa"}, Dark: map[string]string{"ink": "#eeeeee"}}
	light, dark := Resolve(d, custom, "light"), Resolve(d, custom, "dark")
	if light["radius-panel"] != "8px" || dark["radius-panel"] != "8px" {
		t.Errorf("radius-panel light %q dark %q, want 8px in both", light["radius-panel"], dark["radius-panel"])
	}
	if light["bg"] != "#fafafa" || dark["bg"] != d.Dark["bg"] {
		t.Errorf("bg light %q dark %q", light["bg"], dark["bg"])
	}
	if dark["ink"] != "#eeeeee" || light["ink"] != d.Light["ink"] {
		t.Errorf("ink light %q dark %q", light["ink"], dark["ink"])
	}
}
