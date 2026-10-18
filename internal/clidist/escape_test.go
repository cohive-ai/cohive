package clidist

import (
	"os/exec"
	"strings"
	"testing"
)

// 译文里的引号、美元符号、反引号、反斜杠进了 sh 双引号字符串后，必须原样还原，不能被 shell 解释。
func TestEscapeSh(t *testing.T) {
	for _, in := range []string{
		`plain`, `don't`, `say "hi"`, `cost $5 and ${HOME} and $(id)`, "back`tick`", `a\b\\c`, `“授权” 和 ‘引号’`, "多行\n第二行",
		`100% done %s`, `!bang`,
	} {
		script := `printf '%s' "` + escapeSh(in) + `"`
		out, err := exec.Command("sh", "-c", script).Output()
		if err != nil || string(out) != in {
			t.Errorf("escapeSh(%q)：sh 得到 %q，%v（脚本 %s）", in, out, err, script)
		}
	}
}

// PowerShell 双引号字符串里的转义：反引号开头的转义序列覆盖 ` " $ 与三种弯双引号。没有 PowerShell 可跑，只检查转义规则。
func TestEscapePs(t *testing.T) {
	cases := map[string]string{
		`plain`:       `plain`,
		`don't`:       `don't`,
		`say "hi"`:    "say `\"hi`\"",
		`cost $5`:     "cost `$5",
		"tick`":       "tick``",
		"“授权”":        "`“授权`”",
		"„x“":         "`„x`“",
		`C:\Users\me`: `C:\Users\me`,
	}
	for in, want := range cases {
		if got := escapePs(in); got != want {
			t.Errorf("escapePs(%q) = %q，期望 %q", in, got, want)
		}
	}
}

// renderMessage：占位符换成表达式，表达式本身不被转义，译文被转义。
func TestRenderMessage(t *testing.T) {
	got := renderMessage("en", "install.checksum_mismatch", []string{"Want", "${want}", "Got", "${got}"}, escapeSh)
	if !strings.Contains(got, "expected ${want}, got ${got})") || strings.Contains(got, `\$`) {
		t.Fatalf("表达式应原样保留：%s", got)
	}
}
