package core

import "github.com/cohive-ai/cohive/internal/store"

// 通讯工具的两类做法：
//   - Telegram 用带参数的机器人链接连接账号，在网页填群编号绑群；
//   - 其余通讯工具都“用码绑定”：账号连接是私聊把网页上的码发给机器人、再回网页确认，群绑定是在群里 @机器人
//     发网页上生成的群绑定码。
//
// 接新的通讯工具（飞书、Lark……）时，在 store 里加常量并放进 store.Providers，在 UsesCode 里加一行，再补齐语言包里
// 按通讯工具命名的文案（TestProviderKeysComplete 会逐个检查），业务层其余地方不用改。

const ProviderSlack = store.ProviderSlack

// UsesCode 判断通讯工具是否用码绑定。
func UsesCode(provider string) bool {
	switch provider {
	case ProviderDingTalk, ProviderSlack:
		return true
	}
	return false
}

// CodeProviders 按显示顺序列出用码绑定的通讯工具。
func CodeProviders() []string {
	var out []string
	for _, p := range store.Providers {
		if UsesCode(p) {
			out = append(out, p)
		}
	}
	return out
}

// ActLinkOf、ActUnlinkOf 是用码绑定的通讯工具“连接、断开账号”的管理操作（account.<通讯工具>_link / _unlink）。
func ActLinkOf(provider string) string   { return "account." + provider + "_link" }
func ActUnlinkOf(provider string) string { return "account." + provider + "_unlink" }
