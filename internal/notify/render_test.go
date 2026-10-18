package notify_test

import (
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

var (
	claude = store.Member{ID: 1, Name: "lee-claude", TelegramUsername: "lee_claude"} // 只有用户名的旧数据
	codex  = store.Member{ID: 2, Name: "lee-codex"}                                  // 没连接 Telegram
	linked = store.Member{ID: 3, Name: `a<b>&"c`, TelegramUsername: "old_name", TelegramUserID: ptr(int64(777))}
)

func ptr[T any](v T) *T { return &v }

func members() map[int64]store.Member {
	return map[int64]store.Member{1: claude, 2: codex, 3: linked}
}

func sampleIssue() store.Issue {
	owner := codex.ID
	return store.Issue{
		ID: 12, SourceMemberID: claude.ID, SourceName: claude.Name, OwnerID: &owner, OwnerName: codex.Name,
		TargetService: "order-api", Title: "下单接口返回 500", Brief: "空购物车下单返回 500",
		Summary: "SECRET-SUMMARY", Expected: "SECRET-EXPECTED", Actual: "SECRET-ACTUAL",
		ReproScript: "SECRET-REPRO", ReproEnv: "SECRET-ENV", Evidence: "SECRET-EVIDENCE",
	}
}

// tws 是测试用的工作空间与服务器级对外地址。
type tws struct {
	ws   store.Workspace
	base string
}

func workspace(showTitle, showBrief bool, url string) tws {
	return tws{ws: store.Workspace{Name: "payments", DisplayName: "支付组",
		Notify: store.NotifyConfig{Channel: "telegram", Target: "-100", ShowTitle: showTitle, ShowBrief: showBrief}}, base: url}
}

func render(t *testing.T, w tws, is store.Issue, event string, actor int64, tool string, recipients ...int64) string {
	t.Helper()
	return notify.Render(w.base, w.ws, is, store.Notification{Event: event, ActorMemberID: actor, ActorTool: tool, Recipients: recipients}, members())
}

func TestAssignedMessage(t *testing.T) {
	text := render(t, workspace(true, true, "https://cohive.example.com"), sampleIssue(), "assigned", claude.ID, "claude-code", codex.ID)
	for _, want := range []string{"#12", "新单交给 lee-codex", "order-api", "lee-claude", "Claude Code", "发出",
		"下单接口返回 500", "空购物车下单返回 500", "/cohive 12", "$cohive 12", "https://cohive.example.com/w/payments/issues/12"} {
		if !strings.Contains(text, want) {
			t.Errorf("消息应包含 %q：\n%s", want, text)
		}
	}
}

// 链接用服务器级对外地址 + 路径，不用显示名。
func TestLinkUsesExternalURLAndPath(t *testing.T) {
	text := render(t, workspace(true, true, "https://cohive.example.com"), sampleIssue(), "assigned", claude.ID, "claude-code", codex.ID)
	if !strings.Contains(text, "https://cohive.example.com/w/payments/issues/12") || strings.Contains(text, "支付组") {
		t.Fatalf("链接应为对外地址 + 路径：\n%s", text)
	}
}

func TestNeverLeaksIssueBody(t *testing.T) {
	for _, event := range []string{"assigned", "declined", "verify", "reopened", "escalated", "accepted", "closed"} {
		text := render(t, workspace(true, true, ""), sampleIssue(), event, codex.ID, "codex", claude.ID, codex.ID)
		if strings.Contains(text, "SECRET") {
			t.Fatalf("%s 消息泄露了问题单正文：\n%s", event, text)
		}
	}
}

func TestTitleAndBriefSwitches(t *testing.T) {
	text := render(t, workspace(false, false, ""), sampleIssue(), "assigned", claude.ID, "claude-code", codex.ID)
	if strings.Contains(text, "下单接口返回 500") || strings.Contains(text, "空购物车") {
		t.Fatalf("关闭开关后不应显示标题和简述：\n%s", text)
	}
	if strings.Contains(text, "http") {
		t.Fatalf("没配置看板地址时不应有链接：\n%s", text)
	}
}

func TestMentions(t *testing.T) {
	is := sampleIssue()
	text := render(t, workspace(true, true, ""), is, "verify", codex.ID, "codex", claude.ID)
	if !strings.Contains(text, "@lee_claude") {
		t.Fatalf("登记了 Telegram 用户名的成员应被 @：\n%s", text)
	}
	text = render(t, workspace(true, true, ""), is, "escalated", claude.ID, "claude-code", claude.ID, codex.ID)
	if !strings.Contains(text, "@lee_claude") || !strings.Contains(text, "lee-codex") || strings.Contains(text, "@lee-codex") {
		t.Fatalf("没登记的成员只显示成员名、不加 @：\n%s", text)
	}
}

// 连接了 Telegram 的成员按用户编号提到，不用可能为空或已改的用户名；链接文字严格转义。
func TestMentionByUserID(t *testing.T) {
	for _, event := range []string{"assigned", "declined", "verify", "reopened", "escalated", "accepted", "closed"} {
		text := render(t, workspace(true, true, ""), sampleIssue(), event, codex.ID, "codex", linked.ID, codex.ID)
		want := `<a href="tg://user?id=777">a&lt;b&gt;&amp;&#34;c</a>`
		if !strings.Contains(text, want) || strings.Contains(text, "@old_name") {
			t.Fatalf("%s：应按用户编号提到、不用用户名，期望含 %s：\n%s", event, want, text)
		}
		// 去掉这一个链接后不应再有任何标签，说明链接文字没有把消息结构弄乱
		if rest := strings.Replace(text, want, "", 1); strings.ContainsAny(rest, "<>") {
			t.Fatalf("%s：链接之外出现了未转义的尖括号：\n%s", event, text)
		}
	}
}

func TestTransferWording(t *testing.T) {
	text := render(t, workspace(true, true, ""), sampleIssue(), "assigned", codex.ID, "codex", claude.ID)
	if !strings.Contains(text, "转交") || !strings.Contains(text, "@lee_claude") {
		t.Fatalf("由非发起人交来的单应写成转交：\n%s", text)
	}
}

func TestHTMLEscaping(t *testing.T) {
	is := sampleIssue()
	is.Title = `<b>x</b> & "y"`
	is.TargetService = "<svc>"
	text := render(t, workspace(true, true, ""), is, "assigned", claude.ID, "claude-code", codex.ID)
	if strings.Contains(text, "<b>x</b>") || strings.Contains(text, "<svc>") {
		t.Fatalf("用户输入必须转义：\n%s", text)
	}
	if !strings.Contains(text, "&lt;b&gt;x&lt;/b&gt; &amp;") {
		t.Fatalf("转义结果不符：\n%s", text)
	}
}

// 接单告知发起人、验证通过关单告知负责人：写明谁做了什么、@ 对方；指引是“查看”而不是“处理”；
// 标题、简述照工作空间开关；正文从不发。
func TestAcceptedAndClosedMessages(t *testing.T) {
	is := sampleIssue()
	text := render(t, workspace(true, true, "https://cohive.example.com"), is, "accepted", codex.ID, "codex", claude.ID)
	for _, want := range []string{"#12 已被 lee-codex 接单", "告知发起人 @lee_claude", "order-api", "用 Codex 接单",
		"下单接口返回 500", "/cohive 12", "查看", "https://cohive.example.com/w/payments/issues/12"} {
		if !strings.Contains(text, want) {
			t.Errorf("接单消息应包含 %q：\n%s", want, text)
		}
	}
	text = render(t, workspace(false, false, ""), is, "closed", claude.ID, "claude-code", codex.ID)
	for _, want := range []string{"#12 已由 lee-claude 验证通过并关闭", "告知负责人 lee-codex", "用 Claude Code 关闭", "查看"} {
		if !strings.Contains(text, want) {
			t.Errorf("关单消息应包含 %q：\n%s", want, text)
		}
	}
	if strings.Contains(text, "下单接口返回 500") || strings.Contains(text, "空购物车") || strings.Contains(text, "处理") {
		t.Fatalf("关掉开关后不显示标题简述；告知型指引不写“处理”：\n%s", text)
	}
}

// 钉钉格式：标题（卡片标题与降级 Markdown 的标题）、按钉钉连接 @ 负责人。
func TestAcceptedAndClosedDingTalk(t *testing.T) {
	is := sampleIssue()
	people := map[int64]notify.Person{
		claude.ID: {Name: claude.Name},
		codex.ID:  {Name: codex.Name, Chats: []store.ChatIdentity{{Provider: store.ProviderDingTalk, Tenant: "corp", UserID: "u-codex"}}},
	}
	ws := store.Workspace{Name: "payments", Notify: store.NotifyConfig{Channel: store.ProviderDingTalk, Target: "cid", ShowTitle: true}}
	m := notify.RenderFor(notify.DingTalkFormat{}, store.ProviderDingTalk, "", ws, is,
		store.Notification{Event: "closed", ActorMemberID: claude.ID, ActorTool: "claude-code", Recipients: []int64{codex.ID}}, people)
	if m.Title != "#12 已关闭" || !strings.Contains(m.Text, "告知负责人 @lee-codex") || len(m.Mentions) != 1 || m.Mentions[0].UserID != "u-codex" {
		t.Fatalf("钉钉关单消息：%+v", m)
	}
	m = notify.RenderFor(notify.DingTalkFormat{}, store.ProviderDingTalk, "", ws, is,
		store.Notification{Event: "accepted", ActorMemberID: codex.ID, ActorTool: "codex", Recipients: []int64{claude.ID}}, people)
	if m.Title != "#12 已接单" || !strings.Contains(m.Text, "告知发起人 lee-claude") || len(m.Mentions) != 0 {
		t.Fatalf("钉钉接单消息（发起人没连钉钉，只写名字）：%+v", m)
	}
}

// 超时催办：待接单催负责人、待验证催发起人，写出工作空间设置的时长；第 2 档同时请管理员协调；钉钉标题。
func TestOverdueMessages(t *testing.T) {
	is := sampleIssue()
	w := workspace(true, false, "")
	w.ws.Remind = store.RemindConfig{OpenHours: 4, VerifyHours: 24}
	n := store.Notification{Event: "overdue", IssueStatus: "OPEN", ActorMemberID: codex.ID, ActorTool: "cohive", Recipients: []int64{codex.ID}}
	text := notify.Render(w.base, w.ws, is, n, members())
	for _, want := range []string{"#12 已等待接单超过 4 小时，请 lee-codex 接单、退回或转交", "下单接口返回 500", "处理"} {
		if !strings.Contains(text, want) {
			t.Errorf("待接单催办应包含 %q：\n%s", want, text)
		}
	}
	n = store.Notification{Event: "overdue_admin", IssueStatus: "READY_FOR_VERIFICATION", ActorMemberID: claude.ID, ActorTool: "cohive",
		Recipients: []int64{claude.ID, linked.ID}}
	text = notify.Render(w.base, w.ws, is, n, members())
	if !strings.Contains(text, "#12 已等待验证超过 48 小时，仍没有动作：请 @lee_claude 尽快验证；请管理员 <a href=\"tg://user?id=777\">") {
		t.Fatalf("第 2 档：\n%s", text)
	}
	n.Recipients = []int64{claude.ID}
	if text = notify.Render(w.base, w.ws, is, n, members()); !strings.Contains(text, "仍没有动作，请 @lee_claude 尽快验证") || strings.Contains(text, "管理员") {
		t.Fatalf("没有别的管理员时只催当事人：\n%s", text)
	}
	w.ws.Remind.OpenHours = 0 // 投递前关掉了：不写具体小时数
	n = store.Notification{Event: "overdue", IssueStatus: "OPEN", Recipients: []int64{codex.ID}}
	if text = notify.Render(w.base, w.ws, is, n, members()); !strings.Contains(text, "已等待接单较长时间") {
		t.Fatalf("时长已关时：\n%s", text)
	}
	m := notify.RenderFor(notify.DingTalkFormat{}, store.ProviderDingTalk, "", w.ws, is,
		store.Notification{Event: "overdue_admin", IssueStatus: "OPEN", Recipients: []int64{codex.ID}},
		map[int64]notify.Person{codex.ID: {Name: codex.Name, Chats: []store.ChatIdentity{{Provider: store.ProviderDingTalk, UserID: "u-codex"}}}})
	if m.Title != "#12 催办（已请管理员协调）" || len(m.Mentions) != 1 || !strings.Contains(m.Text, "@lee-codex") {
		t.Fatalf("钉钉催办：%+v", m)
	}
	if text := render(t, workspace(true, true, ""), is, "overdue", codex.ID, "cohive", codex.ID); strings.Contains(text, "SECRET") {
		t.Fatalf("催办不带正文：\n%s", text)
	}
}
