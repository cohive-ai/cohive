package i18n

import "os"

// Local 判断本机命令行用的语言：环境变量 COHIVE_LANG > 系统语言（LC_ALL、LC_MESSAGES、LANG，
// 再看操作系统的界面语言）> 英文。
func Local() Lang {
	if l, ok := Match(os.Getenv("COHIVE_LANG")); ok {
		return l
	}
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(k); v != "" {
			if l, ok := Match(v); ok {
				return l
			}
			// 设置了但匹配不到（例如 ja_JP）：尊重用户的区域设置，回退英文，不再看系统界面语言
			if normalize(v) != "" {
				return EN
			}
		}
	}
	if l, ok := Match(osLanguages()); ok {
		return l
	}
	return EN
}
