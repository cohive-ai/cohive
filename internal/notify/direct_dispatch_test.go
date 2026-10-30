package notify_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

// ddChannel 同时实现 Sender 与 DirectSender：群消息与私聊分开记录，私聊可按次数注入错误。
type ddChannel struct {
	mu       sync.Mutex
	provider string
	groups   []notify.Rendered
	directs  []ddSent
	errs     []error // 第 N 次私聊返回 errs[N]；超出范围成功
	calls    int
}

type ddSent struct {
	to store.ChatIdentity
	m  notify.Rendered
}

func (c *ddChannel) Format() notify.Formatter {
	if c.provider == store.ProviderDingTalk {
		return notify.DingTalkFormat{}
	}
	return notify.TelegramFormat{}
}

func (c *ddChannel) Send(_ context.Context, _ string, m notify.Rendered) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.groups = append(c.groups, m)
	return nil
}

func (c *ddChannel) SendDirect(_ context.Context, to store.ChatIdentity, m notify.Rendered) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	i := c.calls
	c.calls++
	if i < len(c.errs) && c.errs[i] != nil {
		return c.errs[i]
	}
	c.directs = append(c.directs, ddSent{to, m})
	return nil
}

type ddEnv struct {
	dispatchEnv
	w  store.Member
	tg *ddChannel
}

func newDDEnv(t *testing.T, linkTelegram bool) ddEnv {
	t.Helper()
	ctx := context.Background()
	e := newDispatchEnv(t)
	ws, _ := store.DefaultWorkspace(ctx, e.pool)
	w, err := store.CreateMember(ctx, e.pool, ws, "lee-watcher", "")
	if err != nil {
		t.Fatal(err)
	}
	if linkTelegram {
		if err := store.UpsertChatIdentity(ctx, e.pool, store.ChatIdentity{AccountID: w.AccountID, Provider: store.ProviderTelegram, UserID: "9001", Handle: "watcher"}); err != nil {
			t.Fatal(err)
		}
	}
	tg := &ddChannel{provider: store.ProviderTelegram}
	e.d.Senders = map[string]notify.Sender{store.ProviderTelegram: tg}
	return ddEnv{dispatchEnv: e, w: w, tg: tg}
}

// submitCC 发出一张给 lee-codex、抄送 lee-watcher 的单。
func (e ddEnv) submitCC(t *testing.T) store.Issue {
	t.Helper()
	ctx := context.Background()
	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "下单接口返回 500", Brief: "空购物车下单报错", Owner: "lee-codex", Actual: "SECRET", CC: []string{"lee-watcher"}})
	if err != nil {
		t.Fatal(err)
	}
	is, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version})
	if err != nil {
		t.Fatal(err)
	}
	return is
}

func (e ddEnv) direct(t *testing.T, issueID int64) store.Notification {
	t.Helper()
	list, err := store.ListNotificationsForIssue(context.Background(), e.pool, issueID)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Notification
	for _, n := range list {
		if n.DirectMemberID != nil {
			out = append(out, n)
		}
	}
	if len(out) != 1 {
		t.Fatalf("应有 1 条私聊通知：%+v", out)
	}
	return out[0]
}

// 私聊发到关注者的连接上：群消息不再提关注者；幂等键是 n<编号>；默认语言用服务器缺省语言。
func TestDirectSentToConnection(t *testing.T) {
	e := newDDEnv(t, true)
	ctx := context.Background()
	is := e.submitCC(t)
	if n, err := e.d.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("应处理群通知与私聊共 2 条：%d %v", n, err)
	}
	if len(e.tg.groups) != 1 || strings.Contains(e.tg.groups[0].Text, "关注") || strings.Contains(e.tg.groups[0].Text, "lee-watcher") {
		t.Fatalf("群消息不再提关注者：%+v", e.tg.groups)
	}
	if len(e.tg.directs) != 1 {
		t.Fatalf("应私聊 1 条：%+v", e.tg.directs)
	}
	got := e.tg.directs[0]
	d := e.direct(t, is.ID)
	if got.to.UserID != "9001" || got.m.Key != "n"+strconv.FormatInt(d.ID, 10) || d.Status != "sent" {
		t.Fatalf("私聊目标 / 幂等键 / 状态：%+v %+v", got, d)
	}
	if first := strings.SplitN(got.m.Text, "\n", 2)[0]; !strings.HasPrefix(first, "你关注的问题单 #"+strconv.FormatInt(is.ID, 10)+" 有新动态：") {
		t.Fatalf("原因行：%q", first)
	}
	if len(got.m.Mentions) != 0 || strings.Contains(got.m.Text, "SECRET") {
		t.Fatalf("私聊不 @、不带正文：%+v", got.m)
	}
}

// 收件人账号设了英文：私聊是英文；群消息仍按工作空间语言。
func TestDirectUsesRecipientLanguage(t *testing.T) {
	e := newDDEnv(t, true)
	ctx := context.Background()
	if err := store.SetAccountLanguage(ctx, e.pool, e.w.AccountID, "en"); err != nil {
		t.Fatal(err)
	}
	e.submitCC(t)
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.tg.directs) != 1 || !strings.HasPrefix(e.tg.directs[0].m.Text, "Update on issue #") || e.tg.directs[0].m.Lang != "en" {
		t.Fatalf("私聊应是英文：%+v", e.tg.directs)
	}
	if len(e.tg.groups) != 1 || strings.Contains(e.tg.groups[0].Text, "Update on issue") {
		t.Fatalf("群消息不受收件人语言影响：%+v", e.tg.groups)
	}
}

// 没设账号语言：用服务器缺省语言。
func TestDirectDefaultsToServerLanguage(t *testing.T) {
	e := newDDEnv(t, true)
	e.submitCC(t)
	if _, err := e.d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(e.tg.directs) != 1 || !strings.HasPrefix(e.tg.directs[0].m.Text, "你关注的问题单 #") {
		t.Fatalf("应按服务器缺省语言（简体中文）：%+v", e.tg.directs)
	}
}

// 工作空间没绑群：私聊照发，也没有群通知。
func TestDirectWithoutGroup(t *testing.T) {
	e := newDDEnv(t, true)
	ctx := context.Background()
	ws, _ := store.DefaultWorkspace(ctx, e.pool)
	if err := store.SetWorkspaceNotify(ctx, e.pool, ws, store.NotifyConfig{}); err != nil {
		t.Fatal(err)
	}
	e.submitCC(t)
	if n, err := e.d.RunOnce(ctx); err != nil || n != 1 {
		t.Fatalf("只有 1 条私聊：%d %v", n, err)
	}
	if len(e.tg.groups) != 0 || len(e.tg.directs) != 1 {
		t.Fatalf("没绑群也私聊：群 %d 私聊 %d", len(e.tg.groups), len(e.tg.directs))
	}
}

// 收件人没连接通讯账号：skipped，原因写明，会话提醒仍在。
func TestDirectSkippedWithoutConnection(t *testing.T) {
	e := newDDEnv(t, false)
	ctx := context.Background()
	is := e.submitCC(t)
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	d := e.direct(t, is.ID)
	if d.Status != "skipped" || d.LastError != "收件人没有连接可私聊的通讯账号" || len(e.tg.directs) != 0 {
		t.Fatalf("应跳过：%+v", d)
	}
	var n int
	if err := e.pool.QueryRow(ctx, `SELECT count(*) FROM member_alerts WHERE member_id = $1 AND issue_id = $2 AND kind = 'watching'`, e.w.ID, is.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("会话提醒仍在：%d %v", n, err)
	}
}

// 只登记了用户名、没有用户编号的连接发不了私聊：skipped。
func TestDirectSkippedWithoutUserID(t *testing.T) {
	e := newDDEnv(t, false)
	ctx := context.Background()
	if err := store.UpsertChatIdentity(ctx, e.pool, store.ChatIdentity{AccountID: e.w.AccountID, Provider: store.ProviderTelegram, Handle: "watcher"}); err != nil {
		t.Fatal(err)
	}
	is := e.submitCC(t)
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if d := e.direct(t, is.ID); d.Status != "skipped" || len(e.tg.directs) != 0 {
		t.Fatalf("没有用户编号应跳过：%+v", d)
	}
}

// 通道不是 DirectSender：skipped。
func TestDirectSkippedWhenSenderCannotDirect(t *testing.T) {
	e := newDDEnv(t, true)
	e.d.Senders = map[string]notify.Sender{store.ProviderTelegram: e.sender}
	is := e.submitCC(t)
	if _, err := e.d.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if d := e.direct(t, is.ID); d.Status != "skipped" {
		t.Fatalf("通道不支持私聊应跳过：%+v", d)
	}
}

// 收件人已被移出：skipped。
func TestDirectSkippedForRemovedMember(t *testing.T) {
	e := newDDEnv(t, true)
	ctx := context.Background()
	is := e.submitCC(t)
	if _, err := e.pool.Exec(ctx, `UPDATE members SET status = 'removed' WHERE id = $1`, e.w.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if d := e.direct(t, is.ID); d.Status != "skipped" || len(e.tg.directs) != 0 {
		t.Fatalf("已移出应跳过：%+v", d)
	}
}

// 永久性错误：只调用一次，failed。
func TestDirectPermanentErrorFails(t *testing.T) {
	e := newDDEnv(t, true)
	ctx := context.Background()
	e.tg.errs = []error{&notify.PermanentError{Msg: "blocked by user"}}
	is := e.submitCC(t)
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	d := e.direct(t, is.ID)
	if d.Status != "failed" || e.tg.calls != 1 || !strings.Contains(d.LastError, "blocked") {
		t.Fatalf("永久性错误应 failed 且只试一次：%+v calls=%d", d, e.tg.calls)
	}
}

// 普通错误：最多试 3 次，用尽 failed。
func TestDirectPlainErrorRetriesThenFails(t *testing.T) {
	e := newDDEnv(t, true)
	ctx := context.Background()
	boom := errors.New("boom")
	e.tg.errs = []error{boom, boom, boom, boom}
	is := e.submitCC(t)
	for i := 1; i <= 3; i++ {
		if _, err := e.d.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		d := e.direct(t, is.ID)
		if i < 3 && (d.Status != "pending" || d.Attempts != i || e.tg.calls != i) {
			t.Fatalf("第 %d 次失败后应待重试：%+v calls=%d", i, d, e.tg.calls)
		}
		e.makeDue(t)
	}
	d := e.direct(t, is.ID)
	if d.Status != "failed" || e.tg.calls != notify.DirectMaxAttempts {
		t.Fatalf("3 次后 failed：%+v calls=%d", d, e.tg.calls)
	}
	e.makeDue(t)
	if _, err := e.d.RunOnce(ctx); err != nil || e.tg.calls != 3 {
		t.Fatalf("failed 后不再试：calls=%d", e.tg.calls)
	}
}

// 限流：延后、不计失败；通道整体不可用计入次数（防止积压的私聊每分钟重试一遍）；都不卡住别的通知。
func TestDirectChannelDownAndRateLimitDelay(t *testing.T) {
	e := newDDEnv(t, true)
	ctx := context.Background()
	e.tg.errs = []error{&notify.RetryAfterError{Seconds: 30}, &notify.ChannelDownError{Msg: "token invalid"}}
	is := e.submitCC(t)
	if n, err := e.d.RunOnce(ctx); err != nil || n != 2 {
		t.Fatalf("私聊延后不应让整轮停下，群通知照发：%d %v", n, err)
	}
	if len(e.tg.groups) != 1 {
		t.Fatalf("群通知应已发出：%d", len(e.tg.groups))
	}
	d := e.direct(t, is.ID)
	if d.Status != "pending" || d.Attempts != 0 {
		t.Fatalf("限流：延后且不计失败：%+v", d)
	}
	e.makeDue(t)
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if d = e.direct(t, is.ID); d.Status != "pending" || d.Attempts != 1 || e.tg.calls != 2 {
		t.Fatalf("通道不可用：计一次失败后重试：%+v calls=%d", d, e.tg.calls)
	}
	e.makeDue(t)
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if d = e.direct(t, is.ID); d.Status != "sent" {
		t.Fatalf("恢复后应送出：%+v", d)
	}
}

// 通道一直不可用：试满 DirectMaxAttempts 次就放弃，不会每分钟重试到过期。
func TestDirectChannelDownGivesUp(t *testing.T) {
	e := newDDEnv(t, true)
	ctx := context.Background()
	down := &notify.ChannelDownError{Msg: "token invalid"}
	e.tg.errs = []error{down, down, down, down, down}
	is := e.submitCC(t)
	for i := 0; i < 5; i++ {
		if _, err := e.d.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		e.makeDue(t)
	}
	if d := e.direct(t, is.ID); d.Status != "failed" || e.tg.calls != notify.DirectMaxAttempts {
		t.Fatalf("通道不可用 3 次后 failed：%+v calls=%d", d, e.tg.calls)
	}
}

// 优先用工作空间绑定的通讯工具；那边没连接再退到别的。
func TestDirectPrefersWorkspaceProvider(t *testing.T) {
	e := newDDEnv(t, true)
	ctx := context.Background()
	ws, _ := store.DefaultWorkspace(ctx, e.pool)
	if err := store.SetWorkspaceNotify(ctx, e.pool, ws, store.NotifyConfig{Channel: store.ProviderDingTalk, Target: "cid-1", ShowTitle: true}); err != nil {
		t.Fatal(err)
	}
	dt := &ddChannel{provider: store.ProviderDingTalk}
	e.d.Senders[store.ProviderDingTalk] = dt
	if err := store.UpsertChatIdentity(ctx, e.pool, store.ChatIdentity{AccountID: e.w.AccountID, Provider: store.ProviderDingTalk, Tenant: "corp", UserID: "u-watch"}); err != nil {
		t.Fatal(err)
	}
	e.submitCC(t)
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(dt.directs) != 1 || dt.directs[0].to.UserID != "u-watch" || dt.directs[0].to.Tenant != "corp" || len(e.tg.directs) != 0 {
		t.Fatalf("应优先走工作空间绑定的钉钉：dt %+v tg %+v", dt.directs, e.tg.directs)
	}
	if len(dt.directs[0].m.Mentions) != 0 {
		t.Fatalf("私聊不 @：%+v", dt.directs[0].m)
	}

	// 钉钉那边没连接：退到 Telegram
	e2 := newDDEnv(t, true)
	if err := store.SetWorkspaceNotify(ctx, e2.pool, ws, store.NotifyConfig{Channel: store.ProviderDingTalk, Target: "cid-1"}); err != nil {
		t.Fatal(err)
	}
	dt2 := &ddChannel{provider: store.ProviderDingTalk}
	e2.d.Senders[store.ProviderDingTalk] = dt2
	e2.submitCC(t)
	if _, err := e2.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(dt2.directs) != 0 || len(e2.tg.directs) != 1 {
		t.Fatalf("绑定的通讯工具上没连接时退到别的：dt %+v tg %+v", dt2.directs, e2.tg.directs)
	}
}

// 留言里被 @ 的人：原因行是“提到了你”。
func TestDirectMentionedReason(t *testing.T) {
	e := newDDEnv(t, true)
	ctx := context.Background()
	is := e.submitCC(t)
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	e.tg.directs = nil
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActComment, Note: "@lee-watcher 看一下"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.d.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.tg.directs) != 1 || !strings.Contains(e.tg.directs[0].m.Text, "lee-claude 在问题单 #") || !strings.Contains(e.tg.directs[0].m.Text, "提到了你") {
		t.Fatalf("被 @ 的原因行：%+v", e.tg.directs)
	}
}
