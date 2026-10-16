package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// PasswordInfo 是成员的网页密码状态。
type PasswordInfo struct {
	Hash       string // bcrypt 哈希；为空表示没有设置密码
	MustChange bool
}

// MemberPassword 读取成员的密码哈希。
func MemberPassword(ctx context.Context, q DBTX, memberID int64) (PasswordInfo, error) {
	var p PasswordInfo
	err := q.QueryRow(ctx, `SELECT password_hash, must_change_password FROM members WHERE id = $1`, memberID).
		Scan(&p.Hash, &p.MustChange)
	return p, notFound(err)
}

// SetMemberPassword 设置成员的密码哈希。
func SetMemberPassword(ctx context.Context, q DBTX, memberID int64, hash string, mustChange bool) error {
	tag, err := q.Exec(ctx, `UPDATE members SET password_hash = $2, must_change_password = $3 WHERE id = $1`,
		memberID, hash, mustChange)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// MembersByName 返回所有工作空间里叫这个名字的成员（网页登录时按成员名查找）。
func MembersByName(ctx context.Context, q DBTX, name string) ([]Member, error) {
	rows, err := q.Query(ctx, `SELECT `+memberColumns+` FROM members WHERE name = $1 ORDER BY id`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// AnyAdmin 返回是否已经有任何管理员。
func AnyAdmin(ctx context.Context, q DBTX) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE role = 'admin')`).Scan(&ok)
	return ok, err
}

// CreateSession 保存网页会话（只存会话编号的哈希）。
// 网页会话的来源
const (
	SessionViaPassword = "password" // 账号密码登录（或注册后直接登录）
	SessionViaDevice   = "device"   // 已配对电脑上 cohive web 免密登录
)

func CreateSession(ctx context.Context, q DBTX, idHash []byte, memberID int64, via string, expiresAt time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO web_sessions (id_hash, member_id, via, expires_at) VALUES ($1, $2, $3, $4)`,
		idHash, memberID, via, expiresAt)
	return err
}

// SessionMember 返回有效会话所属的成员与会话来源；会话不存在或过期时返回 ErrUnauthorized。
func SessionMember(ctx context.Context, q DBTX, idHash []byte) (Member, string, error) {
	var m Member
	var via string
	err := q.QueryRow(ctx, `SELECT `+prefixed("m.", memberColumns)+`, s.via FROM web_sessions s
		JOIN members m ON m.id = s.member_id WHERE s.id_hash = $1 AND s.expires_at > now()`, idHash).
		Scan(&m.ID, &m.WorkspaceID, &m.Name, &m.DisplayName, &m.TelegramUsername, &m.TelegramUserID, &m.Role, &via)
	if errors.Is(notFound(err), ErrNotFound) {
		return Member{}, "", ErrUnauthorized
	}
	return m, via, err
}

// DeleteSession 删除会话（退出登录）。
func DeleteSession(ctx context.Context, q DBTX, idHash []byte) error {
	_, err := q.Exec(ctx, `DELETE FROM web_sessions WHERE id_hash = $1`, idHash)
	return err
}

// DeleteMemberSessions 删除成员的全部会话（改密码、重置密码后让其他登录失效）；keep 为要保留的会话。
func DeleteMemberSessions(ctx context.Context, q DBTX, memberID int64, keep []byte) error {
	_, err := q.Exec(ctx, `DELETE FROM web_sessions WHERE member_id = $1 AND ($2::bytea IS NULL OR id_hash <> $2)`, memberID, keep)
	return err
}

// CreateWebLoginCode 保存一次性网页登录码的哈希。
func CreateWebLoginCode(ctx context.Context, q DBTX, codeHash []byte, memberID int64, expiresAt time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO web_login_codes (code_hash, member_id, expires_at) VALUES ($1, $2, $3)`,
		codeHash, memberID, expiresAt)
	return err
}

// UseWebLoginCode 原子地使用一次性网页登录码，返回成员编号；不可用时返回 ErrNotFound。
func UseWebLoginCode(ctx context.Context, q DBTX, codeHash []byte) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `UPDATE web_login_codes SET used_at = now()
		WHERE code_hash = $1 AND used_at IS NULL AND expires_at > now() RETURNING member_id`, codeHash).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// prefixed 给逗号分隔的列名加表前缀。
func prefixed(p, cols string) string {
	return p + strings.ReplaceAll(cols, ", ", ", "+p)
}
