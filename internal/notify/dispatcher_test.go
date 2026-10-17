package notify_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
	"github.com/jackc/pgx/v5/pgxpool"
)

type fakeSender struct {
	mu    sync.Mutex
	sent  []string
	fail  error
	calls int
}

func (f *fakeSender) Format() notify.Formatter { return notify.TelegramFormat{} }

func (f *fakeSender) Send(ctx context.Context, target string, m notify.Rendered) error {
	return f.send(ctx, target, m.Text)
}

func (f *fakeSender) send(_ context.Context, target, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail != nil {
		return f.fail
	}
	f.sent = append(f.sent, target+"|"+text)
	return nil
}

type dispatchEnv struct {
	pool   *pgxpool.Pool
	svc    *core.Service
	a, b   store.Identity
	sender *fakeSender
	d      *notify.Dispatcher
}

func newDispatchEnv(t *testing.T) dispatchEnv {
	t.Helper()
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	ws, err := store.DefaultWorkspace(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspaceNotify(ctx, pool, ws, store.NotifyConfig{Channel: "telegram", Target: "-100", ShowTitle: true, ShowBrief: true}); err != nil {
		t.Fatal(err)
	}
	ident := func(name, tool string) store.Identity {
		m, err := store.CreateMember(ctx, pool, ws, name, "")
		if err != nil {
			t.Fatal(err)
		}
		plain, _, err := store.IssueToken(ctx, pool, m.ID, tool)
		if err != nil {
			t.Fatal(err)
		}
		id, err := store.LookupToken(ctx, pool, plain)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	e := dispatchEnv{pool: pool, svc: &core.Service{Pool: pool}, a: ident("lee-claude", "claude-code"), b: ident("lee-codex", "codex"),
		sender: &fakeSender{}}
	e.d = &notify.Dispatcher{Pool: pool, Senders: map[string]notify.Sender{"telegram": e.sender}, Logf: t.Logf}
	return e
}

func (e dispatchEnv) submitOne(t *testing.T) store.Issue {
	t.Helper()
	ctx := context.Background()
	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "下单接口返回 500", Brief: "空购物车下单报错", Owner: "lee-codex", Actual: "SECRET"})
	if err != nil {
		t.Fatal(err)
	}
	is, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version})
	if err != nil {
		t.Fatal(err)
	}
	return is
}

func (e dispatchEnv) notification(t *testing.T, issueID int64) store.Notification {
	t.Helper()
	list, err := store.ListNotificationsForIssue(context.Background(), e.pool, issueID)
	if err != nil || len(list) != 1 {
		t.Fatalf("应有 1 条通知：%v %v", list, err)
	}
	return list[0]
}

func (e dispatchEnv) makeDue(t *testing.T) {
	t.Helper()
	if _, err := e.pool.Exec(context.Background(), `UPDATE notifications SET next_attempt_at = now() - interval '1 second' WHERE status = 'pending'`); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchSends(t *testing.T) {
	e := newDispatchEnv(t)
	is := e.submitOne(t)
	n, err := e.d.RunOnce(context.Background())
	if err != nil || n != 1 {
		t.Fatalf("应处理 1 条，得到 %d %v", n, err)
	}
	if len(e.sender.sent) != 1 || !strings.HasPrefix(e.sender.sent[0], "-100|") || !strings.Contains(e.sender.sent[0], "空购物车下单报错") || strings.Contains(e.sender.sent[0], "SECRET") {
		t.Fatalf("发送内容不符：%v", e.sender.sent)
	}
	if got := e.notification(t, is.ID); got.Status != "sent" || got.SentAt == nil {
		t.Fatalf("应标记为已发送：%+v", got)
	}
	if n, _ := e.d.RunOnce(context.Background()); n != 0 {
		t.Fatalf("已发送的不应再发，得到 %d", n)
	}
}

// 从库里读出的成员带着 Telegram 用户编号，群通知按编号提到负责人。
func TestDispatchMentionsByTelegramUserID(t *testing.T) {
	e := newDispatchEnv(t)
	ctx := context.Background()
	id := int64(5551)
	if err := store.SetAccountTelegramUserID(ctx, e.pool, e.b.AccountID, &id); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccountTelegramUsername(ctx, e.pool, e.b.AccountID, "old_codex"); err != nil {
		t.Fatal(err)
	}
	e.submitOne(t)
	if n, err := e.d.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("应处理 1 条，得到 %d %v", n, err)
	}
	if len(e.sender.sent) != 1 || !strings.Contains(e.sender.sent[0], `新单交给 <a href="tg://user?id=5551">lee-codex</a>`) ||
		strings.Contains(e.sender.sent[0], "@old_codex") {
		t.Fatalf("应按用户编号提到负责人：%v", e.sender.sent)
	}
}

// 工作空间绑的是另一种通讯工具的群：经那个通道发出，用它的格式，按负责人在那个通讯工具上的连接 @。
func TestDispatchUsesWorkspaceChannel(t *testing.T) {
	e := newDispatchEnv(t)
	ctx := context.Background()
	ws, _ := store.DefaultWorkspace(ctx, e.pool)
	if err := store.SetWorkspaceNotify(ctx, e.pool, ws, store.NotifyConfig{Channel: store.ProviderDingTalk, Target: "cid-1", ShowTitle: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertChatIdentity(ctx, e.pool, store.ChatIdentity{AccountID: e.b.AccountID, Provider: store.ProviderDingTalk,
		Tenant: "corp", UserID: "u-codex"}); err != nil {
		t.Fatal(err)
	}
	dt := &fakeChannel{provider: store.ProviderDingTalk}
	e.d.Senders[store.ProviderDingTalk] = dt
	e.submitOne(t)
	if n, err := e.d.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("应处理 1 条，得到 %d %v", n, err)
	}
	if len(e.sender.sent) != 0 || len(dt.sent) != 1 {
		t.Fatalf("应经钉钉通道发出：tg %v / dt %+v", e.sender.sent, dt.sent)
	}
	m := dt.sent[0]
	if !strings.Contains(m.Text, "[@u-codex]lee-codex") || len(m.Mentions) != 1 || m.Mentions[0].Tenant != "corp" || strings.Contains(m.Text, "SECRET") {
		t.Fatalf("应按钉钉连接 @ 负责人、不带正文：%+v", m)
	}
}

func TestDispatchRetriesThenFails(t *testing.T) {
	e := newDispatchEnv(t)
	is := e.submitOne(t)
	e.sender.fail = errors.New("网络抖动")

	before := time.Now()
	if _, err := e.d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := e.notification(t, is.ID)
	if got.Status != "pending" || got.Attempts != 1 || !strings.Contains(got.LastError, "网络抖动") {
		t.Fatalf("第一次失败后应保持待发并记录错误：%+v", got)
	}
	if wait := got.NextAttemptAt.Sub(before); wait < 9*time.Second || wait > 15*time.Second {
		t.Fatalf("第一次失败后约 10 秒重试，实际 %v", wait)
	}
	if n, _ := e.d.RunOnce(context.Background()); n != 0 {
		t.Fatal("未到重试时间不应再发")
	}

	for i := 2; i <= notify.MaxAttempts; i++ {
		e.makeDue(t)
		if _, err := e.d.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	got = e.notification(t, is.ID)
	if got.Status != "failed" || got.Attempts != notify.MaxAttempts {
		t.Fatalf("失败 %d 次后应标记为失败：%+v", notify.MaxAttempts, got)
	}
}

func TestDispatchPermanentAndRateLimit(t *testing.T) {
	e := newDispatchEnv(t)
	is := e.submitOne(t)

	e.sender.fail = &notify.RetryAfterError{Seconds: 120}
	before := time.Now()
	if _, err := e.d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := e.notification(t, is.ID)
	if wait := got.NextAttemptAt.Sub(before); got.Status != "pending" || wait < 119*time.Second || wait > 125*time.Second {
		t.Fatalf("限流时应按 retry_after 等待：%+v（%v）", got, wait)
	}

	e.makeDue(t)
	tgPermanent := &notify.PermanentError{Msg: "Forbidden: bot was kicked"}
	e.sender.fail = tgPermanent
	if _, err := e.d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := e.notification(t, is.ID); got.Status != "failed" || !strings.Contains(got.LastError, "kicked") {
		t.Fatalf("不可重试的错误应直接标记失败：%+v", got)
	}
}

func TestDispatchSkipsWhenGroupTurnedOff(t *testing.T) {
	e := newDispatchEnv(t)
	is := e.submitOne(t)
	if err := store.SetWorkspaceNotify(context.Background(), e.pool, is.WorkspaceID, store.NotifyConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if e.sender.calls != 0 {
		t.Fatal("群通知关闭后不应再发送")
	}
	if got := e.notification(t, is.ID); got.Status != "failed" || !strings.Contains(got.LastError, "关闭") {
		t.Fatalf("应标记为失败并说明已关闭：%+v", got)
	}
}

// 投递时链接取服务器级对外地址：没设时不带链接；BaseURL 为空时读数据库里的设置。
func TestDispatchLinkFromServerSettings(t *testing.T) {
	ctx := context.Background()
	e := newDispatchEnv(t)
	e.submitOne(t)
	if _, err := e.d.RunOnce(ctx); err != nil || len(e.sender.sent) != 1 || strings.Contains(e.sender.sent[0], "http") {
		t.Fatalf("没设对外地址时不带链接：%v %v", e.sender.sent, err)
	}
	if err := store.SetExternalURL(ctx, e.d.Pool, "https://c.example.com", nil); err != nil {
		t.Fatal(err)
	}
	is := e.submitOne(t)
	if _, err := e.d.RunOnce(ctx); err != nil || len(e.sender.sent) != 2 ||
		!strings.Contains(e.sender.sent[1], fmt.Sprintf("https://c.example.com/w/default/issues/%d", is.ID)) {
		t.Fatalf("应带对外地址拼的链接：%v %v", e.sender.sent, err)
	}
	e.d.BaseURL = func(context.Context) string { return "https://env.example.com" }
	is = e.submitOne(t)
	if _, err := e.d.RunOnce(ctx); err != nil || !strings.Contains(e.sender.sent[2], "https://env.example.com/w/") {
		t.Fatalf("服务端传入的对外地址优先：%v %v", e.sender.sent, err)
	}
}
