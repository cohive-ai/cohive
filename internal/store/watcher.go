package store

import "context"

// 问题单关注者（抄送、自行关注）。有效关注者 = 成员在册且账号未停用：移出成员、停用账号时不必删行，读的时候过滤。

const validWatcher = ` m.status = 'active' AND a.disabled_at IS NULL `

// InsertWatcher 把成员加为关注者；已经在册的忽略。返回这次是不是新加的。
func InsertWatcher(ctx context.Context, q DBTX, issueID, memberID, addedBy int64) (bool, error) {
	tag, err := q.Exec(ctx, `INSERT INTO issue_watchers (issue_id, member_id, added_by) VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING`, issueID, memberID, addedBy)
	return tag.RowsAffected() > 0, err
}

// DeleteWatcher 取消关注；本来没有关注的静默成功。
func DeleteWatcher(ctx context.Context, q DBTX, issueID, memberID int64) error {
	_, err := q.Exec(ctx, `DELETE FROM issue_watchers WHERE issue_id = $1 AND member_id = $2`, issueID, memberID)
	return err
}

// ListWatchers 返回问题单的有效关注者，按关注时间排序。
func ListWatchers(ctx context.Context, q DBTX, issueID int64) ([]Member, error) {
	return collectMembers(ctx, q, `SELECT `+memberColumns+memberFrom+`
		JOIN issue_watchers w ON w.member_id = m.id
		WHERE w.issue_id = $1 AND`+validWatcher+`
		ORDER BY w.created_at, m.id`, issueID)
}

// WatcherIDs 返回问题单有效关注者的成员编号，按关注时间排序。
func WatcherIDs(ctx context.Context, q DBTX, issueID int64) ([]int64, error) {
	rows, err := q.Query(ctx, `SELECT w.member_id FROM issue_watchers w
		JOIN members m ON m.id = w.member_id JOIN accounts a ON a.id = m.account_id
		WHERE w.issue_id = $1 AND`+validWatcher+`
		ORDER BY w.created_at, w.member_id`, issueID)
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

// IsWatcher 判断成员是不是问题单的有效关注者。
func IsWatcher(ctx context.Context, q DBTX, issueID, memberID int64) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM issue_watchers w
		JOIN members m ON m.id = w.member_id JOIN accounts a ON a.id = m.account_id
		WHERE w.issue_id = $1 AND w.member_id = $2 AND`+validWatcher+`)`, issueID, memberID).Scan(&ok)
	return ok, err
}
