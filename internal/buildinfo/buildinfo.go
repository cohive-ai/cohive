// Package buildinfo 保存构建时注入的版本号与提交号，服务端与命令行共用（见 Makefile 的 LDFLAGS）。
package buildinfo

// Version 与 Commit 由构建时 -ldflags -X 注入；直接 go build、go run 时为开发构建。
var (
	Version = "dev"
	Commit  = ""
)

// IsDev 判断是否为没有注入版本号的开发构建；开发构建之间不做版本一致性提醒。
func IsDev() bool { return Version == "" || Version == "dev" }

// String 返回“版本（提交 xxx）”形式的说明。
func String() string {
	if Commit == "" {
		return Version
	}
	return Version + "（提交 " + Commit + "）"
}
