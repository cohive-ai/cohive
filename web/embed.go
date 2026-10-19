// Package web 嵌入看板的构建产物（web/dist，由 make web 生成）以及默认主题与主题规则。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Dist 返回看板构建产物；尚未构建时只含占位文件，服务端会返回构建提示。
func Dist() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

// 默认主题与主题规则：服务端检查、加载自定义主题时读的是和网页同一份
//
//go:embed theme-spec.json
var themeSpec []byte

//go:embed src/theme.css
var defaultTheme []byte

// ThemeSpec 返回主题变量清单与对比度规则（web/theme-spec.json）。
func ThemeSpec() []byte { return themeSpec }

// DefaultTheme 返回默认主题（web/src/theme.css）。
func DefaultTheme() []byte { return defaultTheme }
