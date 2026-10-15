// Package web 嵌入看板的构建产物（web/dist），由 make web 生成。
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
