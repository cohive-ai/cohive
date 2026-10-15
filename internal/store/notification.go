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
	Status        string
	Attempts      int
	NextAttemptAt time.Time
	LastError     string
	CreatedAt     time.Time
	SentAt        *time.Time
}

const notificationColumns = `id, workspace_id, issue_id, event, actor_member_id, actor_tool, recipients,
	status, attempts, next_attempt_at, last_error, created_at, sent_at`

func scanNotification(row interface{ Scan(...any) error }) (Notification, error) {
	var n Notification
	var recipients []byte
	err := row.Scan(&n.ID, &n.WorkspaceID, &n.IssueID, &n.Event, &n.ActorMemberID, &n.ActorTool, &recipients,
		&n.Status, &n.Attempts, &n.NextAttemptAt, &n.LastError, &n.CreatedAt, &n.SentAt)
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
	_, err = q.Exec(ctx, `INSERT INTO notifications (workspace_id, issue_id, event, actor_member_id, actor_tool, recipients)
		VALUES ($1, $2, $3, $4, $5, $6)`, n.WorkspaceID, n.IssueID, n.Event, n.ActorMemberID, n.ActorTool, recipients)
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

// MarkNotificationRetry 记录一次失败，并安排在 retryAt 重试。
func MarkNotificationRetry(ctx context.Context, q DBTX, id int64, retryAt time.Time, errText string) error {
	_, err := q.Exec(ctx, `UPDATE notifications SET attempts = attempts + 1, next_attempt_at = $2, last_error = $3
		WHERE id = $1`, id, retryAt, errText)
	return err
}

// MarkNotificationFailed 放弃投递。
func MarkNotificationFailed(ctx context.Context, q DBTX, id int64, errText string) error {
	_, err := q.Exec(ctx, `UPDATE notifications SET status = 'failed', attempts = attempts + 1, last_error = $2
		WHERE id = $1`, id, errText)
	return err
}
