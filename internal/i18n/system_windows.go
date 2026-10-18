package i18n

import (
	"strings"

	"golang.org/x/sys/windows"
)

// osLanguages 读 Windows 的界面语言，返回逗号分隔的语言代码。
func osLanguages() string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return ""
	}
	return strings.Join(langs, ",")
}
