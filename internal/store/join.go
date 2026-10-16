package store

import (
	"context"
	"time"
)

// JoinRequest 是一次加入工作空间的申请。
type JoinRequest struct {
	ID               int64
	WorkspaceID      int64
	TelegramUserID   int64
	TelegramUsername string
	DisplayName      string
	Status           string // pending、approved、rejected
	DecidedBy        *int64
	CreatedAt        time.Time
	DecidedAt        *time.Time
}

const joinColumns = `id, workspace_id, telegram_user_id, telegram_username, display_name, status, decided_by, created_at, decided_at`

func scanJoin(row interface{ Scan(...any) error }) (JoinRequest, error) {
	var j JoinRequest
	err := row.Scan(&j.ID, &j.WorkspaceID, &j.TelegramUserID, &j.TelegramUsername, &j.DisplayName,
		&j.Status, &j.DecidedBy, &j.CreatedAt, &j.DecidedAt)
	return j, notFound(err)
}

// PendingJoinRequest 返回该账号在工作空间里待审的申请。
func PendingJoinRequest(ctx context.Context, q DBTX, wsID, tgUserID int64) (JoinRequest, error) {
	return scanJoin(q.QueryRow(ctx, `SELECT `+joinColumns+` FROM join_requests
		WHERE workspace_id = $1 AND telegram_user_id = $2 AND status = 'pending'`, wsID, tgUserID))
}

func CreateJoinRequest(ctx context.Context, q DBTX, wsID, tgUserID int64, username, display string) (JoinRequest, error) {
	return scanJoin(q.QueryRow(ctx, `INSERT INTO join_requests (workspace_id, telegram_user_id, telegram_username, display_name)
		VALUES ($1, $2, $3, $4) RETURNING `+joinColumns, wsID, tgUserID, username, display))
}

// GetJoinRequestForUpdate 在事务内读取并锁定申请。
func GetJoinRequestForUpdate(ctx context.Context, q DBTX, id int64) (JoinRequest, error) {
	return scanJoin(q.QueryRow(ctx, `SELECT `+joinColumns+` FROM join_requests WHERE id = $1 FOR UPDATE`, id))
}

func DecideJoinRequest(ctx context.Context, q DBTX, id int64, status string, decidedBy *int64) error {
	_, err := q.Exec(ctx, `UPDATE join_requests SET status = $2, decided_by = $3, decided_at = now() WHERE id = $1`,
		id, status, decidedBy)
	return err
}

// ListJoinRequests 列出工作空间的申请；status 为空时列出全部。
func ListJoinRequests(ctx context.Context, q DBTX, wsID int64, status string) ([]JoinRequest, error) {
	rows, err := q.Query(ctx, `SELECT `+joinColumns+` FROM join_requests
		WHERE workspace_id = $1 AND ($2 = '' OR status = $2) ORDER BY id DESC LIMIT 100`, wsID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []JoinRequest
	for rows.Next() {
		j, err := scanJoin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// MemberByTelegramUserID 返回工作空间里绑定到该 Telegram 编号的成员。
func MemberByTelegramUserID(ctx context.Context, q DBTX, wsID, tgUserID int64) (Member, error) {
	m := Member{WorkspaceID: wsID}
	err := q.QueryRow(ctx, `SELECT id, name, display_name, telegram_username, telegram_user_id, role
		FROM members WHERE workspace_id = $1 AND telegram_user_id = $2`, wsID, tgUserID).
		Scan(&m.ID, &m.Name, &m.DisplayName, &m.TelegramUsername, &m.TelegramUserID, &m.Role)
	return m, notFound(err)
}
