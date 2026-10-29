package notify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sender 是投递问题单通知用到的通道能力，由 Channel 实现。
type Sender interface {
	Format() Formatter
	Send(ctx context.Context, chat string, m Rendered) error
}

// MaxAttempts 是一条通知最多尝试发送的次数（通道整体不可用和限流不计入）。
const MaxAttempts = 7

// backoff 是第 N 次失败后的等待时间，合计约 8.5 小时。
var backoff = []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute, 30 * time.Minute, 2 * time.Hour, 6 * time.Hour}

// MaxAge 是通知的有效期，超过仍未送出的不再发送，避免积压后在群里刷出大量过时消息。
const MaxAge = 24 * time.Hour

// errStopBatch 表示通道暂时整体不可用（令牌错误、限流），本轮不再继续投递。
var errStopBatch = errors.New("pause delivery for this round")

// Dispatcher 把待发通知投递到各工作空间的群里。
type Dispatcher struct {
	Pool    *pgxpool.Pool
	Senders map[string]Sender // 按通讯工具
	Logf    func(format string, args ...any)
	// BaseURL 返回服务器级对外地址，用于拼问题单链接（服务端传入 core.Service 的，环境变量优先）；
	// 为空时直接读数据库里的设置。
	BaseURL func(ctx context.Context) string
	// Reminders 写到点的超时催办（core.Service.QueueReminders）；为空时不催办。
	Reminders func(ctx context.Context) (int, error)

	downMsg string // 上一次记录的通道不可用原因，只在变化时打日志
}

func (d *Dispatcher) logf(format string, args ...any) {
	if d.Logf != nil {
		d.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

func (d *Dispatcher) baseURL(ctx context.Context) string {
	if d.BaseURL != nil {
		return d.BaseURL(ctx)
	}
	st, err := store.GetServerSettings(ctx, d.Pool, false)
	if err != nil {
		return ""
	}
	return st.ExternalURL
}

// Run 每隔 interval 投递一次，直到 ctx 结束。
func (d *Dispatcher) Run(ctx context.Context, interval time.Duration) {
	var lastRemind time.Time
	for ctx.Err() == nil {
		// 超时催办搭投递循环的车：大约每分钟扫一次到点的单子，写好的催办随即由下面的投递发出
		if d.Reminders != nil && time.Since(lastRemind) >= RemindEvery {
			lastRemind = time.Now()
			if _, err := d.Reminders(ctx); err != nil && ctx.Err() == nil {
				d.logf("failed to queue overdue reminders: %v", err)
			}
		}
		if _, err := d.RunOnce(ctx); err != nil && ctx.Err() == nil {
			d.logf("failed to deliver group notifications: %v", err)
		}
		sleep(ctx, interval)
	}
}

// RemindEvery 是扫描超时催办的间隔。催办时长以小时计，一分钟的误差无关紧要。
const RemindEvery = time.Minute

// RunOnce 处理当前所有到期的待发通知（单次最多 50 条），返回处理条数。
func (d *Dispatcher) RunOnce(ctx context.Context) (int, error) {
	n := 0
	for ; n < 50; n++ {
		done, err := d.one(ctx)
		if errors.Is(err, errStopBatch) {
			return n, nil
		}
		if err != nil || done {
			return n, err
		}
	}
	return n, nil
}

// one 在一个事务里锁定一条到期通知并处理；没有到期通知时返回 done=true。
func (d *Dispatcher) one(ctx context.Context) (done bool, err error) {
	tx, err := d.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	n, err := store.ClaimDueNotification(ctx, tx)
	if errors.Is(err, store.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	stop := false
	if err := d.deliver(ctx, tx, n); errors.Is(err, errStopBatch) {
		stop = true
	} else if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	if stop {
		return false, errStopBatch
	}
	return false, nil
}

// deliver 投递一条通知。下面写进数据库的原因（失败、跳过的说明）是存下来给管理员看的文字，用服务器缺省语言；
// 通知本身存的是事件，到发送这一刻才按工作空间的语言渲染成文字。
func (d *Dispatcher) deliver(ctx context.Context, tx pgx.Tx, n store.Notification) error {
	ws, err := store.GetWorkspace(ctx, tx, n.WorkspaceID)
	if err != nil {
		return err
	}
	if ws.Archived() { // 归档时已把待发的跳过，这里兜底：归档与派发并发时漏网的也不发
		return store.MarkNotificationSkipped(ctx, tx, n.ID, i18n.T("notify.dispatch.archived"))
	}
	if ws.Notify.Channel == "" {
		return store.MarkNotificationFailed(ctx, tx, n.ID, i18n.T("notify.dispatch.disabled"))
	}
	sender, ok := d.Senders[ws.Notify.Channel]
	if !ok {
		return store.MarkNotificationFailed(ctx, tx, n.ID, i18n.T("notify.dispatch.channel_unavailable", i18n.D{"Channel": ws.Notify.Channel}))
	}
	is, err := store.GetIssue(ctx, tx, n.IssueID)
	if err != nil {
		return err
	}
	if expired, err := store.NotificationExpired(ctx, tx, n.ID, MaxAge); err != nil {
		return err
	} else if expired {
		return store.MarkNotificationSkipped(ctx, tx, n.ID, i18n.T("notify.dispatch.expired"))
	}
	if stale(n, is) {
		return store.MarkNotificationSkipped(ctx, tx, n.ID, i18n.T("notify.dispatch.stale"))
	}
	list, err := store.ListAllMembers(ctx, tx, n.WorkspaceID)
	if err != nil {
		return err
	}
	accounts := make([]int64, 0, len(list))
	for _, m := range list {
		accounts = append(accounts, m.AccountID)
	}
	chats, err := store.ChatIdentitiesOf(ctx, tx, accounts)
	if err != nil {
		return err
	}
	people := make(map[int64]Person, len(list))
	for _, m := range list {
		people[m.ID] = personOf(m, chats[m.AccountID])
	}

	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	msg := RenderFor(sender.Format(), ws.Notify.Channel, d.baseURL(ctx), ws, is, n, people)
	msg.Key = fmt.Sprintf("n%d", n.ID)
	sendErr := sender.Send(sendCtx, ws.Notify.Target, msg)
	if sendErr == nil {
		if d.downMsg != "" {
			d.logf("group notifications resumed")
			d.downMsg = ""
		}
		return store.MarkNotificationSent(ctx, tx, n.ID)
	}

	attempt := n.Attempts + 1
	var ra *RetryAfterError
	var down *ChannelDownError
	var mig *ChatMigratedError
	switch {
	case errors.As(sendErr, &down):
		if d.downMsg != down.Msg {
			d.logf("group notifications paused, pending ones are kept: %v", sendErr)
			d.downMsg = down.Msg
		}
		if err := store.DelayNotification(ctx, tx, n.ID, time.Minute, sendErr.Error()); err != nil {
			return err
		}
		return errStopBatch
	case errors.As(sendErr, &ra):
		if err := store.DelayNotification(ctx, tx, n.ID, time.Duration(ra.Seconds)*time.Second, sendErr.Error()); err != nil {
			return err
		}
		return errStopBatch
	case errors.As(sendErr, &mig):
		d.logf("group of workspace %s was upgraded to a supergroup, chat ID changed from %s to %s", ws.Name, ws.Notify.Target, mig.NewChatID)
		if err := store.SetWorkspaceNotifyTarget(ctx, tx, ws.ID, mig.NewChatID); err != nil {
			return err
		}
		return store.DelayNotification(ctx, tx, n.ID, 0, sendErr.Error())
	case IsPermanent(sendErr):
		d.logf("group notification #%d failed, not retrying: %v", n.ID, sendErr)
		return store.MarkNotificationFailed(ctx, tx, n.ID, sendErr.Error())
	case attempt >= MaxAttempts:
		d.logf("group notification #%d failed %d times, giving up: %v", n.ID, attempt, sendErr)
		return store.MarkNotificationFailed(ctx, tx, n.ID, sendErr.Error())
	default:
		return store.MarkNotificationRetry(ctx, tx, n.ID, backoff[attempt-1], sendErr.Error())
	}
}

// stale 判断通知是否已过时：问题单的状态或负责人在写入通知之后发生了变化。
// 例如转交给别人之后，旧的“交给 A”就不应再发出。
// 接单、关单是告知型通知：说的是已经发生的事，之后单子再往前走（开始修复、请求验证）也照样发，否则群里的信息链会断。
func stale(n store.Notification, is store.Issue) bool {
	if n.IssueStatus == "" || core.InfoEvent(n.Event) { // 旧数据没有记录、告知型事件，不判断
		return false
	}
	if string(is.Status) != n.IssueStatus {
		return true
	}
	switch {
	case n.IssueOwnerID == nil && is.OwnerID == nil:
		return false
	case n.IssueOwnerID == nil || is.OwnerID == nil:
		return true
	}
	return *n.IssueOwnerID != *is.OwnerID
}
