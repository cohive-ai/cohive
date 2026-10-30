package notify_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

func renderN(w tws, event string, n store.Notification) string {
	n.Event = event
	return notify.Render(w.base, w.ws, sampleIssue(), n, members())
}

// 关注者与原收件人在同一条消息里：首行前半是谁该动手，“关注（仅告知）”后面是关注者（会 @）。
func TestWatchersAppendedToHead(t *testing.T) {
	w := workspace(true, true, "")
	text := renderN(w, "accepted", store.Notification{ActorMemberID: codex.ID, ActorTool: "codex", Recipients: []int64{claude.ID}, Watchers: []int64{linked.ID}})
	head := strings.SplitN(text, "\n", 2)[0]
	want := `#12 已被 lee-codex 接单，告知发起人 @lee_claude；关注（仅告知）<a href="tg://user?id=777">`
	if !strings.HasPrefix(head, want) {
		t.Fatalf("首行：\n%s", head)
	}
	// 留言里被 @ 的人在关注者之前
	text = renderN(w, "commented", store.Notification{ActorMemberID: codex.ID, Recipients: []int64{claude.ID}, Mentions: []int64{linked.ID}, Watchers: []int64{claude.ID}})
	head = strings.SplitN(text, "\n", 2)[0]
	if !strings.Contains(head, "；留言提到 <a href=\"tg://user?id=777\">") || !strings.Contains(head, "；关注（仅告知）@lee_claude") ||
		strings.Index(head, "留言提到") > strings.Index(head, "关注（仅告知）") {
		t.Fatalf("留言提到 + 关注者：\n%s", head)
	}
}

// 操作者本人不被 @，只写名字。
func TestWatcherActorNotMentioned(t *testing.T) {
	text := renderN(workspace(true, true, ""), "accepted",
		store.Notification{ActorMemberID: claude.ID, Recipients: []int64{codex.ID}, Watchers: []int64{claude.ID}})
	head := strings.SplitN(text, "\n", 2)[0]
	if !strings.HasSuffix(head, "；关注（仅告知）lee-claude") || strings.Contains(head[strings.Index(head, "关注"):], "@") {
		t.Fatalf("操作者不应被 @：\n%s", head)
	}
}

// 没有原收件人、只有关注者或被 @ 的人：首行写事件名与操作者。
func TestWatchersOnlyHead(t *testing.T) {
	w := workspace(true, true, "")
	text := renderN(w, "started", store.Notification{ActorMemberID: codex.ID, ActorTool: "codex", Watchers: []int64{linked.ID, claude.ID}})
	head := strings.SplitN(text, "\n", 2)[0]
	if !strings.HasPrefix(head, "#12 开始修复，由 lee-codex 操作；关注（仅告知）") || strings.Contains(head, "告知发起人") {
		t.Fatalf("只有关注者的首行：\n%s", head)
	}
	text = renderN(w, "commented", store.Notification{ActorMemberID: codex.ID, Mentions: []int64{claude.ID}})
	if head = strings.SplitN(text, "\n", 2)[0]; head != "#12 新留言，由 lee-codex 操作；留言提到 @lee_claude" {
		t.Fatalf("只有被 @ 的人：\n%s", head)
	}
}

// 超时催办第 2 档带关注者；钉钉的 @ 名单里也有关注者和被 @ 的人。
func TestOverdueWithWatchersAndDingTalkMentions(t *testing.T) {
	w := workspace(true, false, "")
	w.ws.Remind = store.RemindConfig{OpenHours: 4}
	n := store.Notification{Event: "overdue_admin", IssueStatus: "OPEN", Recipients: []int64{codex.ID, claude.ID}, Watchers: []int64{linked.ID}}
	text := notify.Render(w.base, w.ws, sampleIssue(), n, members())
	head := strings.SplitN(text, "\n", 2)[0]
	if !strings.Contains(head, "请管理员 @lee_claude") || !strings.Contains(head, "；关注（仅告知）<a href=\"tg://user?id=777\">") {
		t.Fatalf("催办第 2 档：\n%s", head)
	}
	people := map[int64]notify.Person{}
	for _, m := range []store.Member{claude, codex, linked} {
		people[m.ID] = notify.Person{Name: m.Name, Chats: []store.ChatIdentity{{Provider: store.ProviderDingTalk, UserID: "u-" + m.Name}}}
	}
	m := notify.RenderFor(notify.DingTalkFormat{}, store.ProviderDingTalk, "", w.ws, sampleIssue(),
		store.Notification{Event: "commented", ActorMemberID: codex.ID, Recipients: []int64{claude.ID}, Mentions: []int64{linked.ID}, Watchers: []int64{codex.ID, linked.ID}}, people)
	ids := map[string]bool{}
	for _, c := range m.Mentions {
		ids[c.UserID] = true
	}
	if len(m.Mentions) != 2 || !ids["u-lee-claude"] || !ids[`u-`+linked.Name] || !strings.Contains(m.Text, "关注（仅告知）lee-codex") {
		t.Fatalf("钉钉 @ 名单（操作者不在内、不重复）：%+v", m)
	}
}

func TestWatchersEnglish(t *testing.T) {
	w := workspace(true, true, "")
	w.ws.Language = "en"
	text := renderN(w, "accepted", store.Notification{ActorMemberID: codex.ID, Recipients: []int64{claude.ID}, Mentions: []int64{claude.ID}, Watchers: []int64{linked.ID}})
	if head := strings.SplitN(text, "\n", 2)[0]; !strings.Contains(head, "; mentioned: @lee_claude; watching (FYI): <a href=\"tg://user?id=777\">") {
		t.Fatalf("英文首行：\n%s", head)
	}
	text = renderN(w, "started", store.Notification{ActorMemberID: codex.ID, Watchers: []int64{claude.ID}})
	if head := strings.SplitN(text, "\n", 2)[0]; head != "#12 Fixing started by lee-codex; watching (FYI): @lee_claude" {
		t.Fatalf("英文只有关注者：\n%s", head)
	}
}

// 派发时关注者与被 @ 的人也从库里取成员，渲染成真名而不是“未知成员”，且不影响过时判断。
func TestDispatchLoadsWatchers(t *testing.T) {
	e := newDispatchEnv(t)
	ctx := context.Background()
	ws, err := store.DefaultWorkspace(ctx, e.pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMember(ctx, e.pool, ws, "lee-watcher", ""); err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "下单接口返回 500", Brief: "空购物车下单报错", Owner: "lee-codex", Actual: "x", CC: []string{"lee-watcher"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version}); err != nil {
		t.Fatal(err)
	}
	if n, err := e.d.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("应处理 1 条：%d %v", n, err)
	}
	if len(e.sender.sent) != 1 || !strings.Contains(e.sender.sent[0], "新单交给 lee-codex，请接单、退回或转交；关注（仅告知）lee-watcher") ||
		strings.Contains(e.sender.sent[0], "未知成员") {
		t.Fatalf("应带上关注者：%v", e.sender.sent)
	}
}
