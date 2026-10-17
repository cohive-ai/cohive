package store

import (
	"context"
	"encoding/json"
	"time"
)

// Notification 是一条待发（或已发）的群通知，与触发它的状态变更在同一事务写入。
type Notification struct {
	ID            int64
	WorkspaceID   int64
	IssueID       int64
	Event         string
	ActorMemberID int64
	ActorTool     string
	Recipients    []int64
	IssueStatus   string // 写入时问题单的状态，投递前核对
	IssueOwnerID  *int64 // 写入时问题单的负责人，投递前核对
	Status        string
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
	SentAt        *time.Time
}

const notificationColumns = `id, workspace_id, issue_id, event, actor_member_id, actor_tool, recipients,
	issue_status, issue_owner_id, status, attempts, next_attempt_at, last_error, created_at, sent_at`

func scanNotification(row interface{ Scan(...any) error }) (Notification, error) {
	var n Notification
	var recipients []byte
	err := row.Scan(&n.ID, &n.WorkspaceID, &n.IssueID, &n.Event, &n.ActorMemberID, &n.ActorTool, &recipients,
		&n.IssueStatus, &n.IssueOwnerID, &n.Status, &n.Attempts, &n.NextAttemptAt, &n.LastError, &n.CreatedAt, &n.SentAt)
	if err != nil {
		return Notification{}, notFound(err)
	}
	return n, json.Unmarshal(recipients, &n.Recipients)
}

func InsertNotification(ctx context.Context, q DBTX, n Notification) error {
	recipients, err := json.Marshal(n.Recipients)
	if err != nil {
		return err
	}
	_, err = q.Exec(ctx, `INSERT INTO notifications (workspace_id, issue_id, event, actor_member_id, actor_tool, recipients,
		issue_status, issue_owner_id) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		n.WorkspaceID, n.IssueID, n.Event, n.ActorMemberID, n.ActorTool, recipients, n.IssueStatus, n.IssueOwnerID)
	return err
}

func ListNotificationsForIssue(ctx context.Context, q DBTX, issueID int64) ([]Notification, error) {
	rows, err := q.Query(ctx, `SELECT `+notificationColumns+` FROM notifications WHERE issue_id = $1 ORDER BY id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// ClaimDueNotification 在事务内锁定一条到期的待发通知；没有时返回 ErrNotFound。
// 多个投递进程并发时互不重复（SKIP LOCKED）。
func ClaimDueNotification(ctx context.Context, q DBTX) (Notification, error) {
	return scanNotification(q.QueryRow(ctx, `SELECT `+notificationColumns+` FROM notifications
		WHERE status = 'pending' AND next_attempt_at <= now()
		ORDER BY next_attempt_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`))
}

func MarkNotificationSent(ctx context.Context, q DBTX, id int64) error {
	_, err := q.Exec(ctx, `UPDATE notifications SET status = 'sent', attempts = attempts + 1, sent_at = now(),
		last_error = '' WHERE id = $1`, id)
	return err
}

// 通知的排期与到期一律用数据库时间（now()）计算：判断到期的 ClaimDueNotification 用的是数据库时钟，
// 若排期用投递进程的时钟，两边哪怕差几毫秒，“立即重发”的通知也会被当成未到期。

// MarkNotificationRetry 记录一次失败，并安排在 after 之后（按数据库时间）重试。
func MarkNotificationRetry(ctx context.Context, q DBTX, id int64, after time.Duration, errText string) error {
	_, err := q.Exec(ctx, `UPDATE notifications SET attempts = attempts + 1,
		next_attempt_at = now() + $2 * interval '1 microsecond', last_error = $3
		WHERE id = $1`, id, after.Microseconds(), errText)
	return err
}

// NotificationExpired 判断通知写入至今（按数据库时间）是否已超过 maxAge。
func NotificationExpired(ctx context.Context, q DBTX, id int64, maxAge time.Duration) (bool, error) {
	var expired bool
	err := q.QueryRow(ctx, `SELECT created_at < now() - $2 * interval '1 microsecond' FROM notifications WHERE id = $1`,
		id, maxAge.Microseconds()).Scan(&expired)
	return expired, notFound(err)
}

// MarkNotificationFailed 放弃投递。
func MarkNotificationFailed(ctx context.Context, q DBTX, id int64, errText string) error {
	_, err := q.Exec(ctx, `UPDATE notifications SET status = 'failed', attempts = attempts + 1, last_error = $2
		WHERE id = $1`, id, errText)
	return err
}

// MarkNotificationSkipped 不再发送（已过时或已过期）。
func MarkNotificationSkipped(ctx context.Context, q DBTX, id int64, reason string) error {
	_, err := q.Exec(ctx, `UPDATE notifications SET status = 'skipped', last_error = $2 WHERE id = $1`, id, reason)
	return err
}

// DelayNotification 推迟 after（按数据库时间；0 表示立即可重发）发送，但不计失败次数
// （通道暂不可用、限流、群编号已更新）。
func DelayNotification(ctx context.Context, q DBTX, id int64, after time.Duration, reason string) error {
	_, err := q.Exec(ctx, `UPDATE notifications SET next_attempt_at = now() + $2 * interval '1 microsecond', last_error = $3
		WHERE id = $1`, id, after.Microseconds(), reason)
	return err
}

// CountNotifications 返回工作空间待发与失败的通知条数。
func CountNotifications(ctx context.Context, q DBTX, wsID int64) (pending, failed int, err error) {
	err = q.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status = 'pending'), count(*) FILTER (WHERE status = 'failed')
		FROM notifications WHERE workspace_id = $1`, wsID).Scan(&pending, &failed)
	return pending, failed, err
}

// RequeueFailedNotifications 把工作空间里失败的通知重新排队，返回条数。
func RequeueFailedNotifications(ctx context.Context, q DBTX, wsID int64) (int64, error) {
	tag, err := q.Exec(ctx, `UPDATE notifications SET status = 'pending', attempts = 0, next_attempt_at = now(),
		created_at = now(), last_error = '' WHERE workspace_id = $1 AND status = 'failed'`, wsID)
	return tag.RowsAffected(), err
}
