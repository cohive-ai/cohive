package notify_test

import (
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

var (
	claude = store.Member{ID: 1, Name: "lee-claude", TelegramUsername: "lee_claude"}
	codex  = store.Member{ID: 2, Name: "lee-codex"} // 没登记 Telegram 用户名
)

func members() map[int64]store.Member {
	return map[int64]store.Member{1: claude, 2: codex}
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
