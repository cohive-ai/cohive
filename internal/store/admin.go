package store

import "context"

// MemberSummary 是管理页成员列表的一行。
type MemberSummary struct {
	Member
	HasPassword bool
	Devices     int
}

// ListMemberSummaries 列出工作空间成员及其设备数、是否设了密码。
func ListMemberSummaries(ctx context.Context, q DBTX, wsID int64) ([]MemberSummary, error) {
	rows, err := q.Query(ctx, `SELECT `+prefixed("m.", memberColumns)+`, m.password_hash <> '',
		(SELECT count(*) FROM devices d WHERE d.member_id = m.id AND d.revoked_at IS NULL)
		FROM members m WHERE m.workspace_id = $1 ORDER BY m.id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemberSummary
	for rows.Next() {
		var s MemberSummary
		if err := rows.Scan(&s.ID, &s.WorkspaceID, &s.Name, &s.DisplayName, &s.TelegramUsername, &s.TelegramUserID, &s.Role,
			&s.HasPassword, &s.Devices); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountAdmins 返回工作空间的管理员数。
func CountAdmins(ctx context.Context, q DBTX, wsID int64) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM members WHERE workspace_id = $1 AND role = 'admin'`, wsID).Scan(&n)
	return n, err
}

// LockAdmins 在事务内锁住工作空间的全部管理员行。
func LockAdmins(ctx context.Context, q DBTX, wsID int64) error {
	rows, err := q.Query(ctx, `SELECT id FROM members WHERE workspace_id = $1 AND role = 'admin' FOR UPDATE`, wsID)
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}
