package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// BoundMember 是绑定到某个 Telegram 用户编号的成员及其工作空间。
type BoundMember struct {
	Member
	WorkspaceName string
}

// MembersByTelegramUserID 返回绑定到该 Telegram 用户编号的所有成员（可能跨多个工作空间）。
func MembersByTelegramUserID(ctx context.Context, q DBTX, userID int64) ([]BoundMember, error) {
	rows, err := q.Query(ctx, `
		SELECT m.id, m.workspace_id, m.name, m.display_name, m.telegram_username, m.telegram_user_id, w.name
		FROM members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.telegram_user_id = $1 ORDER BY w.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BoundMember
	for rows.Next() {
		var b BoundMember
		if err := rows.Scan(&b.ID, &b.WorkspaceID, &b.Name, &b.DisplayName, &b.TelegramUsername, &b.TelegramUserID, &b.WorkspaceName); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// CreateLoginCode 保存登录码的哈希。
func CreateLoginCode(ctx context.Context, q DBTX, memberID int64, codeHash []byte, expiresAt time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO login_codes (member_id, code_hash, expires_at) VALUES ($1, $2, $3)`,
		memberID, codeHash, expiresAt)
	return err
}

// ConsumeLoginCode 原子地使用一个未过期、未使用的登录码，返回所属成员；不可用时返回 ErrNotFound。
func ConsumeLoginCode(ctx context.Context, q DBTX, codeHash []byte) (int64, error) {
	var memberID int64
	err := q.QueryRow(ctx, `UPDATE login_codes SET used_at = now()
		WHERE code_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING member_id`, codeHash).Scan(&memberID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return memberID, err
}

// MemberByID 返回成员。
func MemberByID(ctx context.Context, q DBTX, id int64) (Member, error) {
	m := Member{ID: id}
	err := q.QueryRow(ctx, `SELECT workspace_id, name, display_name, telegram_username, telegram_user_id
		FROM members WHERE id = $1`, id).Scan(&m.WorkspaceID, &m.Name, &m.DisplayName, &m.TelegramUsername, &m.TelegramUserID)
	return m, notFound(err)
}
