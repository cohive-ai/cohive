package skill

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
)

func TestForReturnsEachLanguage(t *testing.T) {
	zh, en := For(i18n.ZhCN), For(i18n.EN)
	if len(zh) == 0 || len(en) == 0 || string(zh) == string(en) {
		t.Fatal("中英文技能文件都应存在且内容不同")
	}
	if !strings.Contains(string(zh), "问题单") || strings.Contains(string(en), "问题单") {
		t.Error("中文版写“问题单”，英文版不应出现")
	}
	if got := For(i18n.Lang("ja")); string(got) != string(en) {
		t.Error("没有某种语言的技能文件时应回退英文")
	}
}

// 两种语言的技能文件讲的是同一件事：frontmatter 的名字一致，提到的工具名与命令完全一致，
// 都认中英两种“以 Cohive 开头的提醒”。
func TestLanguagesAgree(t *testing.T) {
	zh, en := string(For(i18n.ZhCN)), string(For(i18n.EN))
	for name, s := range map[string]string{"zh-CN": zh, "en": en} {
		if !strings.HasPrefix(s, "---\nname: cohive\ndescription: ") {
			t.Errorf("%s：frontmatter 应以 name: cohive 与 description 开头", name)
		}
		if !strings.Contains(s, "Cohive reminder:") || !strings.Contains(s, "Cohive 提醒：") {
			t.Errorf("%s：应同时写明英文与中文的提醒开头", name)
		}
	}
	token := regexp.MustCompile("`(cohive_[a-z_]+|/cohive[^`]*|\\$cohive[^`]*|start_fixing|note|accept|decline|transfer|pass|reopen|expected_version|needs_me|your_actions|brief|version)`")
	set := func(s string) []string {
		seen := map[string]bool{}
		for _, m := range token.FindAllString(s, -1) {
			seen[m] = true
		}
		var out []string
		for k := range seen {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	if a, b := set(zh), set(en); strings.Join(a, " ") != strings.Join(b, " ") {
		t.Errorf("两种语言提到的工具名与参数不一致：\nzh-CN %v\nen    %v", a, b)
	}
}

func TestDetect(t *testing.T) {
	if Detect(For(i18n.ZhCN)) != i18n.ZhCN {
		t.Error("中文版应识别为中文")
	}
	if Detect(For(i18n.EN)) != i18n.EN {
		t.Error("英文版（含少量中文提醒开头）应识别为英文")
	}
	if Detect(nil) != i18n.EN {
		t.Error("空内容按英文")
	}
}

// 技能目录的名字必须是语言包里有的语言。
func TestDirsAreLocales(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, l := range i18n.Supported() {
		have[string(l)] = true
	}
	for _, e := range entries {
		if e.IsDir() && !have[e.Name()] {
			t.Errorf("目录 %s 不是 internal/i18n/locales 里的语言", e.Name())
		}
	}
}
