package notify_test

import (
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

func renderN(w tws, event string, n store.Notification) string {
	n.Event = event
	return notify.Render(w.base, w.ws, sampleIssue(), n, members())
}

// 关注者与被 @ 的人改为私聊：群消息即使收到带这两个名单的旧数据，首行也不再列出、不再 @ 他们。
func TestGroupMessageIgnoresWatchersAndMentions(t *testing.T) {
	w := workspace(true, true, "")
	text := renderN(w, "accepted", store.Notification{ActorMemberID: codex.ID, ActorTool: "codex", Recipients: []int64{claude.ID}, Watchers: []int64{linked.ID}, Mentions: []int64{linked.ID}})
	head := strings.SplitN(text, "\n", 2)[0]
	if head != "#12 已被 lee-codex 接单，告知发起人 @lee_claude" || strings.Contains(text, "tg://user?id=777") {
		t.Fatalf("首行：\n%s", text)
	}
	w.ws.Language = "en"
	text = renderN(w, "commented", store.Notification{ActorMemberID: codex.ID, Recipients: []int64{claude.ID}, Mentions: []int64{linked.ID}, Watchers: []int64{claude.ID}})
	if strings.Contains(text, "mentioned") || strings.Contains(text, "watching") || strings.Contains(text, "tg://user?id=777") {
		t.Fatalf("英文首行：\n%s", text)
	}
}

// 超时催办第 2 档：群消息只 @ 当事人与管理员，不带关注者。
func TestOverdueGroupWithoutWatchers(t *testing.T) {
	w := workspace(true, false, "")
	w.ws.Remind = store.RemindConfig{OpenHours: 4}
	n := store.Notification{Event: "overdue_admin", IssueStatus: "OPEN", Recipients: []int64{codex.ID, claude.ID}, Watchers: []int64{linked.ID}}
	text := notify.Render(w.base, w.ws, sampleIssue(), n, members())
	head := strings.SplitN(text, "\n", 2)[0]
	if !strings.Contains(head, "请管理员 @lee_claude") || strings.Contains(head, "关注") || strings.Contains(text, "tg://user?id=777") {
		t.Fatalf("催办第 2 档：\n%s", head)
	}
}

func directPeople() map[int64]notify.Person {
	return map[int64]notify.Person{
		claude.ID: {Name: claude.Name, Chats: []store.ChatIdentity{{Provider: store.ProviderTelegram, UserID: "111", Handle: "lee_claude"}}},
		codex.ID:  {Name: codex.Name},
		linked.ID: {Name: linked.Name, Chats: []store.ChatIdentity{{Provider: store.ProviderTelegram, UserID: "777"}}},
	}
}

func renderDirect(w tws, n store.Notification, l i18n.Lang) notify.Rendered {
	return notify.RenderDirect(notify.TelegramFormat{}, store.ProviderTelegram, w.base, w.ws, sampleIssue(), n, directPeople(), l)
}

// 私聊（关注）：第一行是原因，名字不带链接、不 @，动作行是查看，末尾有问题单链接。
func TestRenderDirectWatching(t *testing.T) {
	w := workspace(true, true, "https://cohive.example.com")
	n := store.Notification{Event: "started", ActorMemberID: linked.ID, ActorTool: "codex", DirectMemberID: ptr(claude.ID), Watchers: []int64{claude.ID}}
	m := renderDirect(w, n, i18n.ZhCN)
	lines := strings.Split(m.Text, "\n")
	if lines[0] != `你关注的问题单 #12 有新动态：开始修复（a&lt;b&gt;&amp;&#34;c 操作）` {
		t.Fatalf("原因行：\n%s", m.Text)
	}
	for _, want := range []string{"服务 order-api，发起人 lee-claude，负责人 lee-codex", "标题：下单接口返回 500", "简述：空购物车下单返回 500", "/cohive 12", "https://cohive.example.com/w/payments/issues/12"} {
		if !strings.Contains(m.Text, want) {
			t.Fatalf("缺少 %q：\n%s", want, m.Text)
		}
	}
	if strings.Contains(m.Text, "tg://") || strings.Contains(m.Text, "@") || len(m.Mentions) != 0 || strings.Contains(m.Text, "SECRET") {
		t.Fatalf("私聊不 @、不带链接、不带正文：%+v", m)
	}
	if m.Title != "#12 开始修复" || m.Lang != i18n.ZhCN {
		t.Fatalf("Title 与语言：%+v", m)
	}
	// 处理型事件的私聊也是查看指引，不是“请处理”
	n.Event = "assigned"
	if m = renderDirect(w, n, i18n.ZhCN); strings.Contains(m.Text, "接单、退回") || !strings.Contains(m.Text, "/cohive 12") {
		t.Fatalf("动作行应为查看：\n%s", m.Text)
	}
}

// 私聊（被 @）：原因行写出操作者；名字转义；英文版。ShowTitle / ShowBrief 关时不带。
func TestRenderDirectMentionedAndEnglish(t *testing.T) {
	w := workspace(false, false, "")
	n := store.Notification{Event: "commented", ActorMemberID: linked.ID, DirectMemberID: ptr(claude.ID), Mentions: []int64{claude.ID}}
	m := renderDirect(w, n, i18n.ZhCN)
	if first := strings.SplitN(m.Text, "\n", 2)[0]; first != `a&lt;b&gt;&amp;&#34;c 在问题单 #12 的留言里提到了你` {
		t.Fatalf("被 @ 的原因行（名字已转义）：%q", first)
	}
	if strings.Contains(m.Text, "标题：") || strings.Contains(m.Text, "简述：") || len(m.Mentions) != 0 || strings.Contains(m.Text, "tg://") {
		t.Fatalf("关掉标题与简述后不带：\n%s", m.Text)
	}
	en := renderDirect(w, n, i18n.EN)
	if first := strings.SplitN(en.Text, "\n", 2)[0]; !strings.HasSuffix(first, " mentioned you in a comment on issue #12") || en.Lang != i18n.EN {
		t.Fatalf("英文被 @：%+v", en)
	}
	n = store.Notification{Event: "started", ActorMemberID: codex.ID, DirectMemberID: ptr(claude.ID), Watchers: []int64{claude.ID}}
	en = renderDirect(w, n, i18n.EN)
	if first := strings.SplitN(en.Text, "\n", 2)[0]; first != "Update on issue #12 you're watching: Fixing started (by lee-codex)" || en.Title != "#12 Fixing started" {
		t.Fatalf("英文关注：%+v", en)
	}
	if !strings.Contains(en.Text, "/cohive 12") || !strings.Contains(en.Text, "Reporter lee-claude, owner lee-codex") {
		t.Fatalf("英文动作行是查看指引、带发起人与负责人：\n%s", en.Text)
	}
	// 超时催办是系统发起的：原因行不写操作者
	n = store.Notification{Event: "overdue_admin", ActorMemberID: codex.ID, DirectMemberID: ptr(claude.ID), Watchers: []int64{claude.ID}}
	if first := strings.SplitN(renderDirect(w, n, i18n.ZhCN).Text, "\n", 2)[0]; first != "你关注的问题单 #12 有新动态：催办（已请管理员协调）" {
		t.Fatalf("催办原因行：%q", first)
	}
}

// 钉钉私聊同样不 @ 人。
func TestRenderDirectDingTalkNoMentions(t *testing.T) {
	w := workspace(true, true, "")
	people := map[int64]notify.Person{
		claude.ID: {Name: claude.Name, Chats: []store.ChatIdentity{{Provider: store.ProviderDingTalk, UserID: "u-claude"}}},
		codex.ID:  {Name: codex.Name, Chats: []store.ChatIdentity{{Provider: store.ProviderDingTalk, UserID: "u-codex"}}},
	}
	n := store.Notification{Event: "commented", ActorMemberID: codex.ID, DirectMemberID: ptr(claude.ID), Mentions: []int64{claude.ID}}
	m := notify.RenderDirect(notify.DingTalkFormat{}, store.ProviderDingTalk, "", w.ws, sampleIssue(), n, people, i18n.ZhCN)
	if len(m.Mentions) != 0 || strings.Contains(m.Text, "atId") || !strings.Contains(m.Text, "lee-codex") {
		t.Fatalf("钉钉私聊不 @：%+v", m)
	}
}
