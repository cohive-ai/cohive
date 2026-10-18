package i18n

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// osLanguages 读 macOS 的系统首选语言，返回逗号分隔的语言代码。
func osLanguages() string {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return ""
	}
	// 输出形如 ( "zh-Hans-CN", "en-CN" )
	var langs []string
	for _, f := range strings.FieldsFunc(string(out), func(r rune) bool { return strings.ContainsRune("(),\"\n\t ", r) }) {
		langs = append(langs, f)
	}
	return strings.Join(langs, ",")
}
