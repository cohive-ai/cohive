package client

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cohive-ai/cohive/internal/i18n"
)

// 命令行的语言（方案 docs/superpowers/plans/2026-10-18-i18n-community.md 第 3.2、3.3 节）：
// COHIVE_LANG > 账号语言 > 系统语言 > 英文。账号语言由服务器在响应头 Content-Language 回告（账号没设时是服务器缺省语言），
// 缓存在配置目录的 language 文件里（不碰凭据文件），下次启动生效；网页个人中心改了语言，命令行下一次访问服务器后跟上。

// explicitLang 记下用户是否用 COHIVE_LANG 指定了语言：指定了就总带语言头，服务器以它为准，也不再缓存账号语言。
var explicitLang bool

// ResolveLanguage 决定本次运行的语言，由 cohive 入口调用一次。
func ResolveLanguage() i18n.Lang {
	if l, ok := i18n.Match(os.Getenv("COHIVE_LANG")); ok {
		explicitLang = true
		return l
	}
	explicitLang = false
	if l, ok := i18n.Match(cachedLanguage()); ok {
		return l
	}
	return i18n.Local()
}

// setLang 决定请求带不带 Accept-Language：带凭据的请求不带（服务器按账号语言回报错），除非用户指定了 COHIVE_LANG；
// 还不知道是谁的请求（配对、查版本）带命令行当前的语言。
// 只用于发给协作服务 REST 接口的请求；转发 MCP 的请求（proxy.go）不带，MCP 的语言由服务器按账号决定。
func setLang(req *http.Request) {
	if explicitLang || req.Header.Get("Authorization") == "" {
		req.Header.Set("Accept-Language", string(i18n.Default()))
	}
}

// languagePath 是缓存账号语言的文件。
func languagePath() string {
	dir, err := ConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "language")
}

func cachedLanguage() string {
	b, err := os.ReadFile(languagePath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// rememberLang 记下服务器按账号回告的语言：只在带凭据、没带语言头、成功的请求之后，有变化才写。
func rememberLang(req *http.Request, resp *http.Response) {
	if req.Header.Get("Accept-Language") != "" || req.Header.Get("Authorization") == "" || resp.StatusCode/100 != 2 {
		return
	}
	l, ok := i18n.Match(resp.Header.Get("Content-Language"))
	if !ok || cachedLanguage() == string(l) {
		return
	}
	p := languagePath()
	if p == "" {
		return
	}
	// 配置目录不存在时不新建：还没登录过就没有要缓存的账号
	_ = os.WriteFile(p, []byte(string(l)+"\n"), 0o600)
}
