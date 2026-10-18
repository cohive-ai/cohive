// Package secretscan 在问题单正文入库前扫描密钥（安全红线第 5 条）。
//
// 规则取自 gitleaks 的默认规则文件（gitleaks/gitleaks.toml，MIT 许可），
// 引擎只实现“对一段文本跑规则”需要的部分，语义对齐 gitleaks 的 detect 包：
// 关键词预筛、正则、密文分组、熵阈值、全局与规则自带的放行规则。
// 只针对文件路径的规则与放行项跳过：问题单正文没有文件路径。
// Cohive 追加的规则与放行在 cohive.go 里，单独审查。
//
// 扫描结果只含字段、行号与规则，不含密钥本身，调用方可以放心写进错误信息与日志。
package secretscan

import (
	_ "embed"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/BurntSushi/toml"
	"github.com/cohive-ai/cohive/internal/i18n"
)

//go:embed gitleaks/gitleaks.toml
var gitleaksTOML string

// Field 是要扫描的一个字段。
type Field struct {
	Name string // 对调用方可见的字段名，例如 evidence、note
	Text string
}

// Finding 是一处命中：哪个字段、第几行（字段内从 1 开始）、哪条规则。不含密钥。
type Finding struct {
	Field  string
	Line   int
	RuleID string
}

// CategoryIn 返回这条命中的规则类别，按指定语言（例如 GitHub token）。
func (f Finding) CategoryIn(l i18n.Lang) string { return CategoryIn(l, f.RuleID) }

type allowlist struct {
	and       bool
	target    string // secret（缺省）、match、line
	regexes   []*regexp.Regexp
	stopwords []string
	// pathOnly 表示这条放行还要求文件路径或提交号；正文没有这两样，按 gitleaks 的语义永远不放行
	pathOnly bool
}

type rule struct {
	id          string
	re          *regexp.Regexp
	secretGroup int
	entropy     float64
	keywords    []string
	allowlists  []*allowlist
}

type ruleset struct {
	rules  []*rule
	global []*allowlist
}

type tomlAllowlist struct {
	Description string   `toml:"description"`
	Condition   string   `toml:"condition"`
	Commits     []string `toml:"commits"`
	Paths       []string `toml:"paths"`
	RegexTarget string   `toml:"regexTarget"`
	Regexes     []string `toml:"regexes"`
	StopWords   []string `toml:"stopwords"`
	TargetRules []string `toml:"targetRules"`
}

type tomlConfig struct {
	Rules []struct {
		ID          string           `toml:"id"`
		Description string           `toml:"description"`
		Path        string           `toml:"path"`
		Regex       string           `toml:"regex"`
		SecretGroup int              `toml:"secretGroup"`
		Entropy     float64          `toml:"entropy"`
		Keywords    []string         `toml:"keywords"`
		Allowlist   *tomlAllowlist   `toml:"allowlist"`
		Allowlists  []*tomlAllowlist `toml:"allowlists"`
	} `toml:"rules"`
	Allowlist  *tomlAllowlist   `toml:"allowlist"`
	Allowlists []*tomlAllowlist `toml:"allowlists"`
}

func compileAllowlist(a *tomlAllowlist) (*allowlist, error) {
	out := &allowlist{and: strings.EqualFold(a.Condition, "AND"), target: a.RegexTarget}
	switch out.target {
	case "", "secret", "match", "line":
	default:
		return nil, fmt.Errorf("unknown regexTarget %q", a.RegexTarget)
	}
	for _, s := range a.Regexes {
		re, err := regexp.Compile(s)
		if err != nil {
			return nil, err
		}
		out.regexes = append(out.regexes, re)
	}
	for _, w := range a.StopWords {
		out.stopwords = append(out.stopwords, strings.ToLower(w))
	}
	// OR 条件下路径、提交号与正文无关，只看正则与停用词；AND 条件下它们必须同时满足，正文永远满足不了
	out.pathOnly = (len(out.regexes) == 0 && len(out.stopwords) == 0) ||
		(out.and && (len(a.Paths) > 0 || len(a.Commits) > 0))
	return out, nil
}

func parse(src string) (*ruleset, error) {
	var c tomlConfig
	if _, err := toml.Decode(src, &c); err != nil {
		return nil, err
	}
	rs := &ruleset{}
	targeted := map[string][]*allowlist{}
	globals := c.Allowlists
	if c.Allowlist != nil {
		globals = append([]*tomlAllowlist{c.Allowlist}, globals...)
	}
	for _, g := range globals {
		a, err := compileAllowlist(g)
		if err != nil {
			return nil, fmt.Errorf("global allowlist: %w", err)
		}
		if len(g.TargetRules) == 0 {
			rs.global = append(rs.global, a)
		}
		for _, id := range g.TargetRules {
			targeted[id] = append(targeted[id], a)
		}
	}
	for _, r := range c.Rules {
		if r.Path != "" || r.Regex == "" {
			continue // 只针对文件路径的规则
		}
		re, err := regexp.Compile(r.Regex)
		if err != nil {
			return nil, fmt.Errorf("rule %s: %w", r.ID, err)
		}
		if r.SecretGroup > re.NumSubexp() {
			return nil, fmt.Errorf("rule %s: secretGroup %d exceeds the number of groups", r.ID, r.SecretGroup)
		}
		out := &rule{id: r.ID, re: re, secretGroup: r.SecretGroup, entropy: r.Entropy}
		for _, k := range r.Keywords {
			out.keywords = append(out.keywords, strings.ToLower(k))
		}
		als := r.Allowlists
		if r.Allowlist != nil {
			als = append([]*tomlAllowlist{r.Allowlist}, als...)
		}
		for _, ta := range als {
			a, err := compileAllowlist(ta)
			if err != nil {
				return nil, fmt.Errorf("allowlist of rule %s: %w", r.ID, err)
			}
			out.allowlists = append(out.allowlists, a)
		}
		out.allowlists = append(out.allowlists, targeted[r.ID]...)
		rs.rules = append(rs.rules, out)
	}
	return rs, nil
}

// rules 在首次扫描时解析并编译全部规则；规则文件随程序嵌入，解析失败是构建问题，由测试保证不会发生。
var rules = sync.OnceValue(func() *ruleset {
	rs, err := parse(gitleaksTOML)
	if err != nil {
		panic("secretscan: failed to parse gitleaks rules: " + err.Error())
	}
	if err := rs.addCohive(); err != nil {
		panic("secretscan: Cohive extra rules: " + err.Error())
	}
	return rs
})

// RuleCount 返回生效的规则条数（含 Cohive 追加的），供启动信息与测试使用。
func RuleCount() int { return len(rules().rules) }

func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	counts := map[rune]int{}
	n := 0
	for _, r := range s {
		counts[r]++
		n++
	}
	var e float64
	for _, c := range counts {
		p := float64(c) / float64(n)
		e -= p * math.Log2(p)
	}
	return e
}

func (a *allowlist) allows(secret, match, line string) bool {
	if a.pathOnly {
		return false
	}
	target := secret
	switch a.target {
	case "match":
		target = match
	case "line":
		target = line
	}
	regexOK := false
	if target != "" {
		for _, re := range a.regexes {
			if re.MatchString(target) {
				regexOK = true
				break
			}
		}
	}
	stopOK := false
	if secret != "" {
		low := strings.ToLower(secret)
		for _, w := range a.stopwords {
			if strings.Contains(low, w) {
				stopOK = true
				break
			}
		}
	}
	if a.and {
		ok := true
		if len(a.regexes) > 0 {
			ok = ok && regexOK
		}
		if len(a.stopwords) > 0 {
			ok = ok && stopOK
		}
		return ok
	}
	return regexOK || stopOK
}

// lineAt 返回 text 中 pos 所在行的行号（从 1 开始）与该行内容。
func lineAt(text string, pos int) (int, string) {
	n := strings.Count(text[:pos], "\n") + 1
	start := strings.LastIndexByte(text[:pos], '\n') + 1
	end := strings.IndexByte(text[pos:], '\n')
	if end < 0 {
		end = len(text)
	} else {
		end += pos
	}
	return n, text[start:end]
}

func (rs *ruleset) scanText(field, text string) []Finding {
	low := strings.ToLower(text)
	var out []Finding
	for _, r := range rs.rules {
		if len(r.keywords) > 0 {
			hit := false
			for _, k := range r.keywords {
				if strings.Contains(low, k) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		for _, loc := range r.re.FindAllStringIndex(text, -1) {
			raw := text[loc[0]:loc[1]]
			match := strings.Trim(raw, "\n")
			start := loc[0] + strings.Index(raw, match)
			line, lineText := lineAt(text, start)
			secret := match
			// 与 gitleaks 一致：在去掉首尾换行的匹配上再取分组
			if groups := r.re.FindStringSubmatch(match); len(groups) >= 2 {
				if r.secretGroup > 0 {
					secret = groups[r.secretGroup]
				} else {
					for _, g := range groups[1:] {
						if g != "" {
							secret = g
							break
						}
					}
				}
			}
			if r.entropy != 0 && shannonEntropy(secret) <= r.entropy {
				continue
			}
			allowed := false
			for _, a := range rs.global {
				if a.allows(secret, match, lineText) {
					allowed = true
					break
				}
			}
			for _, a := range r.allowlists {
				if allowed {
					break
				}
				allowed = a.allows(secret, match, lineText)
			}
			if allowed {
				continue
			}
			out = append(out, Finding{Field: field, Line: line, RuleID: r.id})
		}
	}
	return out
}

// genericRule 是通用规则的编号：同一行已有具体规则命中时不再重复报通用规则。
const genericRule = "generic-api-key"

// Scan 扫描各字段，按字段、行号排序返回命中；同一字段同一行同一规则只报一次。
func Scan(fields ...Field) []Finding {
	rs := rules()
	var all []Finding
	for _, f := range fields {
		if f.Text == "" {
			continue
		}
		all = append(all, rs.scanText(f.Name, f.Text)...)
	}
	type key struct {
		field string
		line  int
	}
	specific := map[key]bool{}
	for _, f := range all {
		if f.RuleID != genericRule {
			specific[key{f.Field, f.Line}] = true
		}
	}
	seen := map[string]bool{}
	out := all[:0:0]
	for _, f := range all {
		if f.RuleID == genericRule && specific[key{f.Field, f.Line}] {
			continue
		}
		k := fmt.Sprintf("%s\x00%d\x00%s", f.Field, f.Line, f.RuleID)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, f)
	}
	order := map[string]int{}
	for i, f := range fields {
		if _, ok := order[f.Name]; !ok {
			order[f.Name] = i
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Field != out[j].Field {
			return order[out[i].Field] < order[out[j].Field]
		}
		return out[i].Line < out[j].Line
	})
	return out
}
