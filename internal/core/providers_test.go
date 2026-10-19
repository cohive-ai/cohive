package core

import (
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// 用码绑定的通讯工具：文案键都按通讯工具命名、在代码里拼出来，字面量检查管不到，这里逐个确认中英文都齐全。
// 接新的通讯工具时，这个测试会列出要补的键。
func TestProviderKeysComplete(t *testing.T) {
	ps := CodeProviders()
	if len(ps) == 0 {
		t.Fatal("应至少有钉钉、Slack 两种用码绑定的通讯工具")
	}
	for _, p := range ps {
		for _, key := range []string{
			"core.provider." + p, "audit.provider." + p, "audit.credential." + p, "audit.text." + p, "audit.via." + p,
			"audit.verb.account_" + p + "_link", "audit.verb.account_" + p + "_unlink", "audit.verb.account_" + p + "_unlink_self",
			"notify.provider." + p, "api." + p + "_disabled", "server.flag." + p + "_chat", "server.workspace.notify_" + p,
		} {
			for _, l := range []i18n.Lang{i18n.EN, i18n.ZhCN} {
				if !l.Has(key) {
					t.Errorf("语言包 %s 缺键 %s", l, key)
				}
			}
		}
	}
}

// 分类：Telegram 用带参数的链接，其余已接入的都用码；不认识的通讯工具既不是 Telegram 也不用码。
func TestUsesCode(t *testing.T) {
	for p, want := range map[string]bool{store.ProviderTelegram: false, store.ProviderDingTalk: true, store.ProviderSlack: true, "wechat": false, "": false} {
		if UsesCode(p) != want {
			t.Errorf("UsesCode(%q) 应为 %v", p, want)
		}
	}
	if got := CodeProviders(); len(got) != 2 || got[0] != store.ProviderDingTalk || got[1] != store.ProviderSlack {
		t.Errorf("CodeProviders 按显示顺序：%v", got)
	}
	if ActLinkOf(store.ProviderDingTalk) != ActDingTalkLink || ActUnlinkOf(store.ProviderDingTalk) != ActDingTalkUnlink {
		t.Error("钉钉的连接、断开动作名要与历史记录一致")
	}
	if ChatActor(store.Account{ID: 1}, store.ProviderSlack).Via != "slack" || ChatActor(store.Account{ID: 1}, store.ProviderTelegram).Via != ViaTelegram {
		t.Error("群里操作的入口：用码绑定的是通讯工具名，Telegram 是 telegram")
	}
}
