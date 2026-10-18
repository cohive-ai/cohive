package store

import (
	"context"

	"github.com/cohive-ai/cohive/internal/issue"
)

// DueReminder 是一张到了催办时间的问题单：Level 是已催到的档位，Due 是按已等待时长应到的档位（1 或 2）。
type DueReminder struct {
	IssueID        int64
	WorkspaceID    int64
	Status         issue.Status
	OwnerID        *int64
	SourceMemberID int64
	Level          int
	Due            int
}

// DueReminders 找出到了催办时间的问题单并锁住（跳过别人正锁着的）：只看待接单、待验证，
// 工作空间对应的时长为 0 时不催；按数据库时间算已等待时长，一次跳到应到的档位（停机很久后不会连发两条）。
func DueReminders(ctx context.Context, q DBTX, limit int) ([]DueReminder, error) {
	rows, err := q.Query(ctx, `
		SELECT i.id, i.workspace_id, i.status, i.owner_id, i.source_member_id, i.remind_level,
			LEAST(2, floor(extract(epoch FROM now() - i.status_since) / (x.h * 3600)))::int
		FROM issues i
		JOIN workspaces w ON w.id = i.workspace_id
		CROSS JOIN LATERAL (SELECT CASE i.status WHEN 'OPEN' THEN w.remind_open_hours ELSE w.remind_verify_hours END AS h) x
		WHERE i.status IN ('OPEN', 'READY_FOR_VERIFICATION') AND i.remind_level < 2 AND x.h > 0
			AND now() >= i.status_since + make_interval(hours => x.h * (i.remind_level + 1))
		ORDER BY i.status_since, i.id
		LIMIT $1
		FOR UPDATE OF i SKIP LOCKED`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DueReminder
	for rows.Next() {
		var d DueReminder
		var st string
		if err := rows.Scan(&d.IssueID, &d.WorkspaceID, &st, &d.OwnerID, &d.SourceMemberID, &d.Level, &d.Due); err != nil {
			return nil, err
		}
		d.Status = issue.Status(st)
		out = append(out, d)
	}
	return out, rows.Err()
}

// SetRemindLevel 记下问题单本轮已催到的档位（不改版本号与更新时间）。
func SetRemindLevel(ctx context.Context, q DBTX, issueID int64, level int) error {
	return execOne(ctx, q, `UPDATE issues SET remind_level = $2 WHERE id = $1`, issueID, level)
}

// WorkspaceAdminMembers 返回工作空间在册、账号未停用的管理员成员资格编号。
func WorkspaceAdminMembers(ctx context.Context, q DBTX, wsID int64) ([]int64, error) {
	rows, err := q.Query(ctx, `SELECT m.id FROM members m JOIN accounts a ON a.id = m.account_id
		WHERE m.workspace_id = $1 AND m.status = 'active' AND m.role = 'admin' AND a.disabled_at IS NULL ORDER BY m.id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
