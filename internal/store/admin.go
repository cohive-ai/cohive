package store

import "context"

// MemberSummary 是管理页成员列表的一行。
type MemberSummary struct {
	Member
	HasPassword   bool
	Devices       int // 账号未吊销的设备数（设备属于人，作用于他全部工作空间）
	PendingIssues int // 他作为负责人或发起人、尚未结束的问题单数（不含草稿）
	OwnedServices int
}

// ListMemberSummaries 列出工作空间的成员资格；status 为空时列在册的，为 removed 时列已移出的。
func ListMemberSummaries(ctx context.Context, q DBTX, wsID int64, status string) ([]MemberSummary, error) {
	if status == "" {
		status = MemberActive
	}
	rows, err := q.Query(ctx, `SELECT `+memberColumns+`, a.password_hash <> '',
		(SELECT count(*) FROM devices d WHERE d.account_id = a.id AND d.revoked_at IS NULL),
		(SELECT count(*) FROM issues i WHERE i.workspace_id = m.workspace_id
			AND i.status NOT IN ('DRAFT', 'CLOSED', 'DECLINED') AND (i.owner_id = m.id OR i.source_member_id = m.id)),
		(SELECT count(*) FROM services s WHERE s.owner_id = m.id)`+memberFrom+`
		WHERE m.workspace_id = $1 AND m.status = $2 ORDER BY m.id`, wsID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MemberSummary
	for rows.Next() {
		var s MemberSummary
		if err := rows.Scan(&s.ID, &s.WorkspaceID, &s.Name, &s.DisplayName, &s.TelegramUsername, &s.TelegramUserID, &s.Role,
			&s.AccountID, &s.Status, &s.AccountDisabled, &s.HasPassword, &s.Devices, &s.PendingIssues, &s.OwnedServices); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// CountAdmins 返回工作空间在册的管理员数。
func CountAdmins(ctx context.Context, q DBTX, wsID int64) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM members WHERE workspace_id = $1 AND role = 'admin' AND status = 'active'`, wsID).Scan(&n)
	return n, err
}

// LockAdmins 在事务内锁住工作空间的全部在册管理员行（按编号升序；锁的强度同 LockMember）。
func LockAdmins(ctx context.Context, q DBTX, wsID int64) error {
	rows, err := q.Query(ctx, `SELECT id FROM members WHERE workspace_id = $1 AND role = 'admin' AND status = 'active'
		ORDER BY id FOR NO KEY UPDATE`, wsID)
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}
