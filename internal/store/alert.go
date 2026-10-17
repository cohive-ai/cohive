package store

import (
	"context"
	"time"
)

// Alert 是发给某位成员的一条会话内提醒：只有单号、动作、谁，没有标题与正文。
type Alert struct {
	ID            int64
	WorkspaceID   int64
	MemberID      int64 // 收件人
	IssueID       int64
	Kind          string
	ActorMemberID int64
	ActorName     string // 只读：操作者登录名
	CreatedAt     time.Time
}

func InsertAlert(ctx context.Context, q DBTX, a Alert) error {
	_, err := q.Exec(ctx, `INSERT INTO member_alerts (workspace_id, member_id, issue_id, kind, actor_member_id)
		VALUES ($1, $2, $3, $4, $5)`, a.WorkspaceID, a.MemberID, a.IssueID, a.Kind, a.ActorMemberID)
	return err
}

// LastAlertID 返回成员最新一条提醒的编号，没有时为 0。
func LastAlertID(ctx context.Context, q DBTX, memberID int64) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM member_alerts WHERE member_id = $1`, memberID).Scan(&id)
	return id, err
}

// AlertsAfter 返回成员编号大于 after 的提醒，另加 grace 之内写入的（编号在插入时分配、提交时才可见，
// 编号较小的可能后提交，只按编号会漏掉；调用方按编号去重）。按编号升序，最多 limit 条。
func AlertsAfter(ctx context.Context, q DBTX, memberID, after int64, grace time.Duration, limit int) ([]Alert, error) {
	rows, err := q.Query(ctx, `
		SELECT x.id, x.workspace_id, x.member_id, x.issue_id, x.kind, x.actor_member_id, a.name, x.created_at
		FROM member_alerts x JOIN members m ON m.id = x.actor_member_id JOIN accounts a ON a.id = m.account_id
		WHERE x.member_id = $1 AND (x.id > $2 OR x.created_at > now() - make_interval(secs => $3))
		ORDER BY x.id LIMIT $4`, memberID, after, grace.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Alert
	for rows.Next() {
		var x Alert
		if err := rows.Scan(&x.ID, &x.WorkspaceID, &x.MemberID, &x.IssueID, &x.Kind, &x.ActorMemberID, &x.ActorName, &x.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
