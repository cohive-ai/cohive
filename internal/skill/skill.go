// Package skill 内嵌 /cohive 技能文件，供 cohive init 写入项目。
//
// 每种语言一个目录：<语言代码>/SKILL.md，语言代码与 internal/i18n/locales 的目录名一致。
// 社区新增语言时只要加一个目录，没有对应技能文件的语言回退英文。
// 选目录而不是 SKILL.en.md 这样的后缀：与语言包的组织一致，且技能文件本身的名字必须是 SKILL.md（编程工具按这个名字找）。
package skill

import (
	"embed"
	"unicode"

	"github.com/cohive-ai/cohive/internal/i18n"
)

//go:embed */SKILL.md
var files embed.FS

// For 返回某种语言的 /cohive（Claude Code、OpenCode）与 $cohive（Codex）共用的技能内容；没有该语言的版本时用英文。
func For(l i18n.Lang) []byte {
	if b, err := files.ReadFile(string(l) + "/SKILL.md"); err == nil {
		return b
	}
	b, err := files.ReadFile(string(i18n.EN) + "/SKILL.md")
	if err != nil {
		panic(err) // 英文版随程序嵌入，读不到只可能是构建有误
	}
	return b
}

// Detect 按内容判断一份已有的技能文件是哪种语言：汉字占比超过 10% 为简体中文，否则为英文。
// 不能“含汉字就算中文”：英文版里引用了中文的提醒开头“Cohive 提醒：”。
// cohive init 用它沿用项目里原有技能文件的语言，免得团队里语言不同的成员来回改写共用的文件。
func Detect(content []byte) i18n.Lang {
	han, total := 0, 0
	for _, r := range string(content) {
		total++
		if unicode.Is(unicode.Han, r) {
			han++
		}
	}
	if han*10 > total {
		return i18n.ZhCN
	}
	return i18n.EN
}
