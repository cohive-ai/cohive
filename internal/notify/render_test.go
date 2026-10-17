package notify_test

import (
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

var (
	claude = store.Member{ID: 1, Name: "lee-claude", TelegramUsername: "lee_claude"} // 只有用户名的旧数据
	codex  = store.Member{ID: 2, Name: "lee-codex"}                                   // 没连接 Telegram
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
	for _, event := range []string{"assigned", "declined", "verify", "reopened", "escalated"} {
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
	for _, event := range []string{"assigned", "declined", "verify", "reopened", "escalated"} {
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
