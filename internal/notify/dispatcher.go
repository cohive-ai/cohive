package notify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sender 是一个通知通道（目前只有 Telegram）。
type Sender interface {
	Send(ctx context.Context, target, text string) error
}

// MaxAttempts 是一条通知最多尝试发送的次数。
const MaxAttempts = 5

// backoff 是第 N 次失败后的等待时间。
var backoff = []time.Duration{10 * time.Second, time.Minute, 5 * time.Minute, 30 * time.Minute}

// Dispatcher 把待发通知投递到各工作空间的群里。
type Dispatcher struct {
	Pool    *pgxpool.Pool
	Senders map[string]Sender // 按通道名
	Logf    func(format string, args ...any)
}

func (d *Dispatcher) logf(format string, args ...any) {
	if d.Logf != nil {
		d.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// Run 每隔 interval 投递一次，直到 ctx 结束。
func (d *Dispatcher) Run(ctx context.Context, interval time.Duration) {
	for ctx.Err() == nil {
		if _, err := d.RunOnce(ctx); err != nil && ctx.Err() == nil {
			d.logf("投递群通知出错：%v", err)
		}
		sleep(ctx, interval)
	}
}

// RunOnce 处理当前所有到期的待发通知（单次最多 50 条），返回处理条数。
func (d *Dispatcher) RunOnce(ctx context.Context) (int, error) {
	n := 0
	for ; n < 50; n++ {
		done, err := d.one(ctx)
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
	if err := d.deliver(ctx, tx, n); err != nil {
		return false, err
	}
	return false, tx.Commit(ctx)
}

func (d *Dispatcher) deliver(ctx context.Context, tx pgx.Tx, n store.Notification) error {
	ws, err := store.GetWorkspace(ctx, tx, n.WorkspaceID)
	if err != nil {
		return err
	}
	if ws.Notify.Channel == "" {
		return store.MarkNotificationFailed(ctx, tx, n.ID, "工作空间已关闭群通知")
	}
	sender, ok := d.Senders[ws.Notify.Channel]
	if !ok {
		return store.MarkNotificationFailed(ctx, tx, n.ID, fmt.Sprintf("通道 %s 不可用", ws.Notify.Channel))
	}
	is, err := store.GetIssue(ctx, tx, n.IssueID)
	if err != nil {
		return err
	}
	list, err := store.ListMembers(ctx, tx, n.WorkspaceID)
	if err != nil {
		return err
	}
	members := make(map[int64]store.Member, len(list))
	for _, m := range list {
		members[m.ID] = m
	}

	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	sendErr := sender.Send(sendCtx, ws.Notify.Target, Render(ws, is, n, members))
	if sendErr == nil {
		return store.MarkNotificationSent(ctx, tx, n.ID)
	}

	attempt := n.Attempts + 1
	var ra *RetryAfterError
	switch {
	case IsPermanent(sendErr):
		d.logf("群通知 #%d 发送失败，不再重试：%v", n.ID, sendErr)
		return store.MarkNotificationFailed(ctx, tx, n.ID, sendErr.Error())
	case attempt >= MaxAttempts:
		d.logf("群通知 #%d 已失败 %d 次，放弃：%v", n.ID, attempt, sendErr)
		return store.MarkNotificationFailed(ctx, tx, n.ID, sendErr.Error())
	case errors.As(sendErr, &ra):
		return store.MarkNotificationRetry(ctx, tx, n.ID, time.Now().Add(time.Duration(ra.Seconds)*time.Second), sendErr.Error())
	default:
		return store.MarkNotificationRetry(ctx, tx, n.ID, time.Now().Add(backoff[attempt-1]), sendErr.Error())
	}
}
