// Package theme 读取、过滤并检查自定义主题文件。
//
// 自定义主题只能覆盖默认主题里的 CSS 变量：文件被逐条解析，只留下已知变量的合法取值，
// 然后由本包重新生成样式表，原文一个字节也不会发给浏览器。变量清单与对比度规则来自
// web/theme-spec.json，网页的对比度测试读的是同一份。
package theme

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/cohive-ai/cohive/web"
)

// Token 是一个主题变量。
type Token struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // color、color-alpha、length、shadow
	En   string `json:"en"`
	Zh   string `json:"zh"`
}

// Pair 是一组需要对比度达标的前景与背景。
type Pair struct {
	FG string `json:"fg"`
	BG string `json:"bg"`
	En string `json:"en,omitempty"`
	Zh string `json:"zh,omitempty"`
}

// Spec 是主题变量清单与对比度规则。
type Spec struct {
	MaxFileBytes int     `json:"maxFileBytes"`
	MinText      float64 `json:"minText"`
	MinGraphic   float64 `json:"minGraphic"`
	Tokens       []Token `json:"tokens"`
	Text         []Pair  `json:"text"`
	Graphics     []Pair  `json:"graphics"`

	byName map[string]Token
}

// LoadSpec 读取随程序嵌入的 web/theme-spec.json。
func LoadSpec() (*Spec, error) { return ParseSpec(web.ThemeSpec()) }

// ParseSpec 解析主题规则。
func ParseSpec(b []byte) (*Spec, error) {
	var s Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("theme spec: %w", err)
	}
	if s.MaxFileBytes <= 0 || s.MinText <= 0 || s.MinGraphic <= 0 || len(s.Tokens) == 0 {
		return nil, fmt.Errorf("theme spec: incomplete")
	}
	s.byName = map[string]Token{}
	for _, t := range s.Tokens {
		s.byName[t.Name] = t
	}
	for _, p := range append(append([]Pair{}, s.Text...), s.Graphics...) {
		for _, n := range []string{p.FG, p.BG} {
			if s.byName[n].Kind != "color" {
				return nil, fmt.Errorf("theme spec: contrast pair %s/%s uses %q, which is not a color variable", p.FG, p.BG, n)
			}
		}
	}
	return &s, nil
}

// Token 按名字（不带 --）查变量。
func (s *Spec) Token(name string) (Token, bool) {
	t, ok := s.byName[name]
	return t, ok
}

// Theme 是一套主题的变量取值（不带 --），按浅色、深色分开。取值都已经规范化。
type Theme struct {
	Light map[string]string
	Dark  map[string]string
}

// Problem 是自定义主题文件里的一处问题。
type Problem struct {
	Line int
	// Code：too_large、encoding、syntax、at_rule、selector、property、forbidden、value、unknown
	Code   string
	Name   string // 变量或属性名，选择器或规则问题时是原文片段
	Detail string // 被拒绝的取值等补充信息（已截短）
}

// Parsed 是解析自定义主题文件的结果。Rejected 不为空时整份文件都不应加载；
// Unknown 是没有这个变量的声明，加载时会被丢掉。
type Parsed struct {
	Theme    Theme
	Rejected []Problem
	Unknown  []Problem
}

// 被拒绝的写法：出现任何一个都不接受，不论这个变量是什么类型
var forbiddenParts = []string{
	"url(", "image(", "image-set(", "expression(", "javascript:", "attr(", "env(",
	"@", "\\", "<", ">", "\"", "'", "`", "{", "}", ";", "!", "/*", "*/",
}

// Forbidden 报告取值里是否含有一律拒绝的写法（url()、@import、expression、转义、引号等），返回命中的片段。
func Forbidden(value string) (string, bool) {
	v := strings.ToLower(value)
	for _, p := range forbiddenParts {
		if strings.Contains(v, p) {
			return p, true
		}
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return fmt.Sprintf("U+%04X", r), true
		}
	}
	return "", false
}

var (
	nameRe   = regexp.MustCompile(`^--[a-z][a-z0-9-]*$`)
	hexRe    = regexp.MustCompile(`^#([0-9a-f]{3}|[0-9a-f]{6})$`)
	hexARe   = regexp.MustCompile(`^#[0-9a-f]{8}$`)
	rgbRe    = regexp.MustCompile(`^rgba?\(\s*(\d{1,3})\s*,?\s*(\d{1,3})\s*,?\s*(\d{1,3})\s*(?:[,/]\s*(0|1|0?\.\d{1,3}|\d{1,3}%))?\s*\)$`)
	lengthRe = regexp.MustCompile(`^(0|\d{1,3}(\.\d{1,2})?(px|rem|em))$`)
	offsetRe = regexp.MustCompile(`^(0|-?\d{1,3}(\.\d{1,2})?(px|rem|em))$`)
	varRe    = regexp.MustCompile(`^var\(--([a-z][a-z0-9-]*)\)$`)
)

// normalize 按变量类型检查并规范化取值；不合法时返回 false。
func (s *Spec) normalize(kind, raw string) (string, bool) {
	v := strings.ToLower(strings.Join(strings.Fields(raw), " "))
	switch kind {
	case "color":
		return normalizeHex(v)
	case "color-alpha":
		if h, ok := normalizeHex(v); ok {
			return h, true
		}
		if hexARe.MatchString(v) {
			return v, true
		}
		if m := rgbRe.FindStringSubmatch(v); m != nil {
			for _, c := range m[1:4] {
				if n, _ := strconv.Atoi(c); n > 255 {
					return "", false
				}
			}
			if strings.HasSuffix(m[4], "%") {
				if n, _ := strconv.Atoi(strings.TrimSuffix(m[4], "%")); n > 100 {
					return "", false
				}
			}
			return v, true
		}
	case "length":
		if lengthRe.MatchString(v) {
			return v, true
		}
	case "shadow":
		return s.normalizeShadow(v)
	}
	return "", false
}

func normalizeHex(v string) (string, bool) {
	if !hexRe.MatchString(v) {
		return "", false
	}
	if len(v) == 4 {
		v = "#" + strings.Repeat(v[1:2], 2) + strings.Repeat(v[2:3], 2) + strings.Repeat(v[3:4], 2)
	}
	return v, true
}

// normalizeShadow 只接受 none，或最多两组“2 到 4 个长度 + 一个颜色”（颜色是十六进制或 var(--颜色变量)），可带 inset。
func (s *Spec) normalizeShadow(v string) (string, bool) {
	if v == "none" {
		return v, true
	}
	parts := strings.Split(v, ",")
	if len(parts) > 2 {
		return "", false
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		f := strings.Fields(p)
		if len(f) > 0 && f[0] == "inset" {
			f = f[1:]
		}
		if len(f) < 3 || len(f) > 5 {
			return "", false
		}
		for _, l := range f[:len(f)-1] {
			if !offsetRe.MatchString(l) {
				return "", false
			}
		}
		c := f[len(f)-1]
		if _, ok := normalizeHex(c); !ok {
			m := varRe.FindStringSubmatch(c)
			if m == nil || s.byName[m[1]].Kind != "color" {
				return "", false
			}
		}
		out = append(out, strings.TrimSpace(p))
	}
	return strings.Join(out, ", "), true
}

// selectorMode 把允许的选择器对应到浅色或深色；其余选择器一律拒绝。
func selectorMode(sel string) (string, bool) {
	// 去掉空白和引号再比：[data-theme="dark"]、[data-theme='dark']、[data-theme=dark] 都算
	s := strings.NewReplacer("'", "", `"`, "").Replace(strings.Join(strings.Fields(sel), ""))
	switch s {
	case ":root", ":root[data-theme=light]", "[data-theme=light]":
		return "light", true
	case ":root[data-theme=dark]", "[data-theme=dark]":
		return "dark", true
	}
	return "", false
}

// Parse 解析并过滤一份自定义主题文件。
func (s *Spec) Parse(src []byte) Parsed {
	res := Parsed{Theme: Theme{Light: map[string]string{}, Dark: map[string]string{}}}
	reject := func(line int, code, name, detail string) {
		res.Rejected = append(res.Rejected, Problem{Line: line, Code: code, Name: clip(name), Detail: clip(detail)})
	}
	if len(src) > s.MaxFileBytes {
		reject(0, "too_large", "", strconv.Itoa(len(src)))
		return res
	}
	if !utf8.Valid(src) || strings.ContainsRune(string(src), 0) {
		reject(0, "encoding", "", "")
		return res
	}
	text, ok := stripComments(string(src))
	if !ok {
		reject(lineAt(string(src), len(src)), "syntax", "/*", "")
		return res
	}
	lines := func(off int) int { return lineAt(text, off) }
	i := 0
	for {
		for i < len(text) && isSpace(text[i]) {
			i++
		}
		if i >= len(text) {
			break
		}
		start := i
		// 规则开头：读到 { 或 ; 为止
		j := strings.IndexAny(text[i:], "{;}")
		if j < 0 {
			reject(lines(start), "syntax", strings.TrimSpace(text[start:]), "")
			break
		}
		prelude := strings.TrimSpace(text[i : i+j])
		i += j
		if text[i] == '}' {
			reject(lines(i), "syntax", "}", "")
			i++
			continue
		}
		if text[i] == ';' {
			// 只可能是 @import 一类的语句，或孤立的声明
			if strings.HasPrefix(prelude, "@") {
				reject(lines(start), "at_rule", prelude, "")
			} else {
				reject(lines(start), "syntax", prelude, "")
			}
			i++
			continue
		}
		// 找配对的 }
		depth, k := 0, i
		for ; k < len(text); k++ {
			if text[k] == '{' {
				depth++
			} else if text[k] == '}' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		if k >= len(text) {
			reject(lines(start), "syntax", prelude, "")
			break
		}
		body, bodyStart := text[i+1:k], i+1
		i = k + 1
		if strings.HasPrefix(prelude, "@") {
			reject(lines(start), "at_rule", prelude, "")
			continue
		}
		mode, ok := selectorMode(prelude)
		if !ok {
			reject(lines(start), "selector", prelude, "")
			continue
		}
		if strings.ContainsAny(body, "{}") {
			reject(lines(bodyStart), "syntax", prelude, "")
			continue
		}
		target := res.Theme.Light
		if mode == "dark" {
			target = res.Theme.Dark
		}
		off := bodyStart
		for _, decl := range strings.Split(body, ";") {
			declOff := off
			off += len(decl) + 1
			if strings.TrimSpace(decl) == "" {
				continue
			}
			line := lines(declOff + leadingSpace(decl))
			colon := strings.IndexByte(decl, ':')
			if colon < 0 {
				reject(line, "syntax", strings.TrimSpace(decl), "")
				continue
			}
			name := strings.ToLower(strings.TrimSpace(decl[:colon]))
			value := strings.TrimSpace(decl[colon+1:])
			if !nameRe.MatchString(name) {
				reject(line, "property", name, value)
				continue
			}
			if part, bad := Forbidden(value); bad {
				reject(line, "forbidden", name, part)
				continue
			}
			tok, known := s.byName[strings.TrimPrefix(name, "--")]
			if !known {
				res.Unknown = append(res.Unknown, Problem{Line: line, Code: "unknown", Name: clip(name)})
				continue
			}
			norm, ok := s.normalize(tok.Kind, value)
			if !ok {
				reject(line, "value", name, value)
				continue
			}
			target[tok.Name] = norm
		}
	}
	return res
}

// CSS 生成覆盖默认主题的样式表：浅色写在 :root，深色同时写给“跟随系统”和“手动选深色”两种情况，
// 选择器的优先级与默认主题相同，靠加载顺序覆盖。
func (s *Spec) CSS(t Theme) []byte {
	var b strings.Builder
	b.WriteString("/* Custom theme, regenerated by Cohive from the server's theme file */\n")
	decls := func(m map[string]string, indent string) string {
		var d strings.Builder
		for _, tok := range s.Tokens {
			if v, ok := m[tok.Name]; ok {
				fmt.Fprintf(&d, "%s--%s: %s;\n", indent, tok.Name, v)
			}
		}
		return d.String()
	}
	if len(t.Light) > 0 {
		b.WriteString(":root {\n" + decls(t.Light, "  ") + "}\n")
	}
	if len(t.Dark) > 0 {
		b.WriteString("@media (prefers-color-scheme: dark) {\n  :root:not([data-theme=\"light\"]) {\n" + decls(t.Dark, "    ") + "  }\n}\n")
		b.WriteString(":root[data-theme=\"dark\"] {\n" + decls(t.Dark, "  ") + "}\n")
	}
	return []byte(b.String())
}

// Defaults 从随程序嵌入的默认主题里读出浅色、深色两块的变量。
func Defaults() (Theme, error) { return ParseDefaults(web.DefaultTheme()) }

var declRe = regexp.MustCompile(`--([a-z][a-z0-9-]*):\s*([^;]+);`)

// ParseDefaults 读出默认主题文件里 :root 与 :root[data-theme="dark"] 两块的变量。
func ParseDefaults(css []byte) (Theme, error) {
	src := string(css)
	get := func(sel string) (map[string]string, error) {
		i := strings.Index(src, sel+" {")
		if i < 0 {
			return nil, fmt.Errorf("default theme: missing %s block", sel)
		}
		body := src[strings.Index(src[i:], "{")+i+1:]
		body = body[:strings.Index(body, "}")]
		m := map[string]string{}
		for _, d := range declRe.FindAllStringSubmatch(body, -1) {
			m[d[1]] = strings.ToLower(strings.TrimSpace(d[2]))
		}
		return m, nil
	}
	l, err := get(":root")
	if err != nil {
		return Theme{}, err
	}
	d, err := get(`:root[data-theme="dark"]`)
	if err != nil {
		return Theme{}, err
	}
	return Theme{Light: l, Dark: d}, nil
}

// Resolve 按 CSS 层叠算出某一模式下实际生效的变量：
// 浅色块的覆盖也作用于默认深色块没定义的变量（例如圆角、阴影）。
func Resolve(def, custom Theme, mode string) map[string]string {
	out := map[string]string{}
	for k, v := range def.Light {
		out[k] = v
	}
	if mode == "light" {
		for k, v := range custom.Light {
			out[k] = v
		}
		return out
	}
	for k, v := range custom.Light {
		if _, ok := def.Dark[k]; !ok {
			out[k] = v
		}
	}
	for k, v := range def.Dark {
		out[k] = v
	}
	for k, v := range custom.Dark {
		out[k] = v
	}
	return out
}

// Result 是一组对比度的检查结果。
type Result struct {
	Mode  string // light、dark
	List  string // text、graphics
	Pair  Pair
	Ratio float64
	Min   float64
}

// Pass 报告是否达标。
func (r Result) Pass() bool { return r.Ratio >= r.Min }

// ID 是这组检查的编号，与网页测试用例里的写法一致：模式:类别:前景/背景。
func (r Result) ID() string { return r.Mode + ":" + r.List + ":" + r.Pair.FG + "/" + r.Pair.BG }

// Check 计算默认主题叠加自定义主题后，两种模式下每组对比度。
func (s *Spec) Check(def, custom Theme) []Result {
	var out []Result
	for _, mode := range []string{"light", "dark"} {
		vars := Resolve(def, custom, mode)
		for _, list := range []struct {
			name  string
			pairs []Pair
			min   float64
		}{{"text", s.Text, s.MinText}, {"graphics", s.Graphics, s.MinGraphic}} {
			for _, p := range list.pairs {
				out = append(out, Result{Mode: mode, List: list.name, Pair: p, Ratio: Contrast(vars[p.FG], vars[p.BG]), Min: list.min})
			}
		}
	}
	return out
}

// Contrast 按 WCAG 公式计算两种十六进制颜色的对比度；无法解析时返回 0（按不达标处理）。
func Contrast(a, b string) float64 {
	la, ok1 := luminance(a)
	lb, ok2 := luminance(b)
	if !ok1 || !ok2 {
		return 0
	}
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(hex string) (float64, bool) {
	h, ok := normalizeHex(strings.ToLower(hex))
	if !ok {
		return 0, false
	}
	var c [3]float64
	for i := range c {
		n, _ := strconv.ParseUint(h[1+2*i:3+2*i], 16, 8)
		v := float64(n) / 255
		if v <= 0.04045 {
			c[i] = v / 12.92
		} else {
			c[i] = math.Pow((v+0.055)/1.055, 2.4)
		}
	}
	return 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2], true
}

func stripComments(s string) (string, bool) {
	var b strings.Builder
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			b.WriteString(s)
			return b.String(), true
		}
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			return "", false
		}
		b.WriteString(s[:i])
		// 注释换成等长的空白，保留换行，行号不变
		for _, r := range s[i : i+2+j+2] {
			if r == '\n' {
				b.WriteByte('\n')
			} else {
				b.WriteByte(' ')
			}
		}
		s = s[i+2+j+2:]
	}
}

func lineAt(s string, off int) int {
	if off > len(s) {
		off = len(s)
	}
	return strings.Count(s[:off], "\n") + 1
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }

func leadingSpace(s string) int {
	n := 0
	for n < len(s) && isSpace(s[n]) {
		n++
	}
	return n
}

func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > 60 {
		r := []rune(s)
		return string(r[:60]) + "…"
	}
	return s
}
