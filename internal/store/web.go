package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// 网页会话的来源
const (
	SessionViaPassword = "password" // 账号密码登录（或注册后直接登录）
	SessionViaDevice   = "device"   // 已配对电脑上 cohive web 免密登录
)

// CreateSession 保存网页会话（只存会话编号的哈希）。会话属于账号，不属于某个工作空间。
func CreateSession(ctx context.Context, q DBTX, idHash []byte, accountID int64, via string, expiresAt time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO web_sessions (id_hash, account_id, via, expires_at) VALUES ($1, $2, $3, $4)`,
		idHash, accountID, via, expiresAt)
	return err
}

// SessionAccount 返回有效会话所属的账号与会话来源；会话不存在、过期或账号已停用时返回 ErrUnauthorized。
func SessionAccount(ctx context.Context, q DBTX, idHash []byte) (Account, string, error) {
	var via string
	var a Account
	err := q.QueryRow(ctx, `SELECT `+accountColumns+`, s.via FROM web_sessions s
		JOIN accounts a ON a.id = s.account_id
		WHERE s.id_hash = $1 AND s.expires_at > now() AND a.disabled_at IS NULL`, idHash).
		Scan(&a.ID, &a.Name, &a.DisplayName, &a.TelegramUsername, &a.TelegramUserID, &a.ServerAdmin, &a.DisabledAt, &a.CreatedAt, &via)
	if errors.Is(notFound(err), ErrNotFound) {
		return Account{}, "", ErrUnauthorized
	}
	return a, via, err
}

// DeleteSession 删除会话（退出登录）。
func DeleteSession(ctx context.Context, q DBTX, idHash []byte) error {
	_, err := q.Exec(ctx, `DELETE FROM web_sessions WHERE id_hash = $1`, idHash)
	return err
}

// DeleteAccountSessions 删除账号的全部会话（改密码、重置密码、停用后让其他登录失效）；keep 为要保留的会话。
func DeleteAccountSessions(ctx context.Context, q DBTX, accountID int64, keep []byte) error {
	_, err := q.Exec(ctx, `DELETE FROM web_sessions WHERE account_id = $1 AND ($2::bytea IS NULL OR id_hash <> $2)`, accountID, keep)
	return err
}

// CreateWebLoginCode 保存一次性网页登录码的哈希。
func CreateWebLoginCode(ctx context.Context, q DBTX, codeHash []byte, accountID int64, expiresAt time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO web_login_codes (code_hash, account_id, expires_at) VALUES ($1, $2, $3)`,
		codeHash, accountID, expiresAt)
	return err
}

// UseWebLoginCode 原子地使用一次性网页登录码，返回账号编号；不可用时返回 ErrNotFound。
func UseWebLoginCode(ctx context.Context, q DBTX, codeHash []byte) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `UPDATE web_login_codes SET used_at = now()
		WHERE code_hash = $1 AND used_at IS NULL AND expires_at > now() RETURNING account_id`, codeHash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// DeleteAccountWebLoginCodes 作废账号未用的一次性网页登录码（停用时）。
func DeleteAccountWebLoginCodes(ctx context.Context, q DBTX, accountID int64) error {
	_, err := q.Exec(ctx, `DELETE FROM web_login_codes WHERE account_id = $1 AND used_at IS NULL`, accountID)
	return err
}
