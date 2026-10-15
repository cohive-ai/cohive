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

func statuses(t *testing.T, e dispatchEnv, issueID int64) []string {
	t.Helper()
	list, err := store.ListNotificationsForIssue(context.Background(), e.pool, issueID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, n := range list {
		out = append(out, n.Status)
	}
	return out
}

// 转交后，旧的“交给 B”还没发出，就不应再发，以免 @ 到已不负责的人。
func TestStaleNotificationSkipped(t *testing.T) {
	ctx := context.Background()
	e := newDispatchEnv(t)
	is := e.submitOne(t) // 交给 lee-codex
	if _, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActTransfer,
		ExpectedVersion: is.Version, Assignee: "lee-claude", Note: "前端的问题"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.sender.sent) != 1 || !strings.Contains(e.sender.sent[0], "lee-claude") || strings.Contains(e.sender.sent[0], "交给 lee-codex") {
		t.Fatalf("只应发出最新的“交给 lee-claude”：%v", e.sender.sent)
	}
	if got := statuses(t, e, is.ID); len(got) != 2 || got[0] != "skipped" || got[1] != "sent" {
		t.Fatalf("过时的通知应标记为跳过：%v", got)
	}
}

func TestExpiredNotificationSkipped(t *testing.T) {
	ctx := context.Background()
	e := newDispatchEnv(t)
	is := e.submitOne(t)
	if _, err := e.pool.Exec(ctx, `UPDATE notifications SET created_at = now() - interval '25 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if e.sender.calls != 0 {
		t.Fatal("超过 24 小时的通知不应再发")
	}
	if got := statuses(t, e, is.ID); got[0] != "skipped" {
		t.Fatalf("应标记为跳过：%v", got)
	}
}

// 令牌错误是全局配置问题：暂停本批，通知保留、不计失败次数。
func TestChannelDownKeepsPending(t *testing.T) {
	ctx := context.Background()
	e := newDispatchEnv(t)
	first := e.submitOne(t)
	second := e.submitOne(t)
	e.sender.fail = &notify.ChannelDownError{Msg: "Unauthorized"}
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if e.sender.calls != 1 {
		t.Fatalf("通道不可用时应停止本批，只尝试 1 次，实际 %d 次", e.sender.calls)
	}
	for _, id := range []int64{first.ID, second.ID} {
		n := e.notification(t, id)
		if n.Status != "pending" || n.Attempts != 0 {
			t.Fatalf("通知应保持待发且不计次数：%+v", n)
		}
	}
}

func TestRateLimitNotCounted(t *testing.T) {
	ctx := context.Background()
	e := newDispatchEnv(t)
	is := e.submitOne(t)
	e.submitOne(t)
	e.sender.fail = &notify.RetryAfterError{Seconds: 30}
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if e.sender.calls != 1 {
		t.Fatalf("限流后应停止本批，实际尝试 %d 次", e.sender.calls)
	}
	if n := e.notification(t, is.ID); n.Attempts != 0 || n.Status != "pending" {
		t.Fatalf("限流不计失败次数：%+v", n)
	}
}

type migratingSender struct{ fakeSender }

func (m *migratingSender) Send(ctx context.Context, target, text string) error {
	if target == "-100" {
		m.calls++
		return &notify.ChatMigratedError{NewChatID: "-1009999"}
	}
	return m.fakeSender.Send(ctx, target, text)
}

// 群升级为超级群后，自动改用新编号并补发。
func TestChatMigratedUpdatesTarget(t *testing.T) {
	ctx := context.Background()
	e := newDispatchEnv(t)
	is := e.submitOne(t)
	ms := &migratingSender{}
	e.d.Senders["telegram"] = ms
	for i := 0; i < 2; i++ {
		if _, err := e.d.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := store.GetWorkspace(ctx, e.pool, is.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if ws.Notify.Target != "-1009999" {
		t.Fatalf("应把群编号更新为新编号，得到 %s", ws.Notify.Target)
	}
	if len(ms.sent) != 1 || !strings.HasPrefix(ms.sent[0], "-1009999|") {
		t.Fatalf("应改发到新群：%v", ms.sent)
	}
}

func TestRequeueFailed(t *testing.T) {
	ctx := context.Background()
	e := newDispatchEnv(t)
	is := e.submitOne(t)
	e.sender.fail = &notify.PermanentError{Msg: "bad"}
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	pending, failed, err := store.CountNotifications(ctx, e.pool, is.WorkspaceID)
	if err != nil || pending != 0 || failed != 1 {
		t.Fatalf("应有 1 条失败：%d %d %v", pending, failed, err)
	}
	n, err := store.RequeueFailedNotifications(ctx, e.pool, is.WorkspaceID)
	if err != nil || n != 1 {
		t.Fatalf("应重新排队 1 条：%d %v", n, err)
	}
	e.sender.fail = nil
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.notification(t, is.ID); got.Status != "sent" {
		t.Fatalf("重新排队后应发送成功：%+v", got)
	}
}
