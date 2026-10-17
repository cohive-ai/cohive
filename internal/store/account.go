package store

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// Account 是一个人：登录名、显示名、密码、Telegram 连接全局一份；角色按工作空间挂在成员资格上。
type Account struct {
	ID               int64
	Name             string // 登录名，全局唯一，不可改
	DisplayName      string
	TelegramUsername string
	TelegramUserID   *int64
	ServerAdmin      bool
	DisabledAt       *time.Time
	CreatedAt        time.Time
}

// Disabled 判断账号是否已停用。
func (a Account) Disabled() bool { return a.DisabledAt != nil }

// Telegram 用户名与编号取自通讯账号表（第十一号迁移前在账号表上）。
const accountColumns = `a.id, a.name, a.display_name, coalesce((SELECT ci.handle FROM chat_identities ci WHERE ci.account_id = a.id AND ci.provider = 'telegram'), ''),
	(SELECT nullif(ci.user_id, '')::bigint FROM chat_identities ci WHERE ci.account_id = a.id AND ci.provider = 'telegram'),
	a.is_server_admin, a.disabled_at, a.created_at`

func scanAccount(row interface{ Scan(...any) error }) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Name, &a.DisplayName, &a.TelegramUsername, &a.TelegramUserID, &a.ServerAdmin, &a.DisabledAt, &a.CreatedAt)
	return a, notFound(err)
}

func collectAccounts(rows pgx.Rows, err error) ([]Account, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CreateAccount 新建账号（没有密码）。
func CreateAccount(ctx context.Context, q DBTX, name, display string) (Account, error) {
	return scanAccount(q.QueryRow(ctx, `INSERT INTO accounts AS a (name, display_name) VALUES ($1, $2) RETURNING `+accountColumns,
		name, display))
}

// AccountByID 返回账号。
func AccountByID(ctx context.Context, q DBTX, id int64) (Account, error) {
	return scanAccount(q.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts a WHERE a.id = $1`, id))
}

// AccountByName 按登录名返回账号。
func AccountByName(ctx context.Context, q DBTX, name string) (Account, error) {
	return scanAccount(q.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts a WHERE a.name = $1`, name))
}

// AccountByTelegramUserID 返回连接到该 Telegram 账号的 Cohive 账号。
func AccountByTelegramUserID(ctx context.Context, q DBTX, tgUserID int64) (Account, error) {
	return scanAccount(q.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts a
		WHERE a.id = (SELECT account_id FROM chat_identities WHERE provider = 'telegram' AND tenant = '' AND user_id = $1)`, strconv.FormatInt(tgUserID, 10)))
}

// LockAccount 在事务内锁住账号行并返回最新内容，让同一账号的并发操作排队执行。
func LockAccount(ctx context.Context, q DBTX, id int64) (Account, error) {
	return scanAccount(q.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts a WHERE a.id = $1 FOR UPDATE`, id))
}

// ListAccounts 列出账号，prefix 非空时按登录名前缀筛选。
func ListAccounts(ctx context.Context, q DBTX, prefix string) ([]Account, error) {
	return collectAccounts(q.Query(ctx, `SELECT `+accountColumns+` FROM accounts a
		WHERE $1 = '' OR a.name LIKE replace(replace(replace($1, '\', '\\'), '%', '\%'), '_', '\_') || '%'
		ORDER BY a.name LIMIT 500`, prefix))
}

// PasswordInfo 是账号的网页密码状态。
type PasswordInfo struct {
	Hash       string // bcrypt 哈希；为空表示没有设置密码
	MustChange bool
}

// AccountPassword 读取账号的密码哈希。
func AccountPassword(ctx context.Context, q DBTX, accountID int64) (PasswordInfo, error) {
	var p PasswordInfo
	err := q.QueryRow(ctx, `SELECT password_hash, must_change_password FROM accounts WHERE id = $1`, accountID).
		Scan(&p.Hash, &p.MustChange)
	return p, notFound(err)
}

// SetAccountPassword 设置账号的密码哈希。
func SetAccountPassword(ctx context.Context, q DBTX, accountID int64, hash string, mustChange bool) error {
	return execOne(ctx, q, `UPDATE accounts SET password_hash = $2, must_change_password = $3 WHERE id = $1`,
		accountID, hash, mustChange)
}

// SetAccountTelegram 连接（userID 为 nil 且 username 为空时断开）账号的 Telegram 账号。
func SetAccountTelegram(ctx context.Context, q DBTX, accountID int64, userID *int64, username string) error {
	return setTelegram(ctx, q, accountID, func(string, string) (string, string) { return int64Text(userID), username })
}

// SetAccountTelegramUsername 只改 Telegram 用户名（命令行兜底：只用于 @）。
func SetAccountTelegramUsername(ctx context.Context, q DBTX, accountID int64, username string) error {
	return setTelegram(ctx, q, accountID, func(id, _ string) (string, string) { return id, username })
}

// SetAccountTelegramUserID 只改 Telegram 用户编号（命令行兜底）。
func SetAccountTelegramUserID(ctx context.Context, q DBTX, accountID int64, userID *int64) error {
	return setTelegram(ctx, q, accountID, func(_, handle string) (string, string) { return int64Text(userID), handle })
}

func int64Text(p *int64) string {
	if p == nil {
		return ""
	}
	return strconv.FormatInt(*p, 10)
}

// setTelegram 按 update 算出新的 Telegram 用户编号与用户名写回通讯账号表；两者都为空时删掉这一行。
// 账号不存在时返回 ErrNotFound；同一 Telegram 账号已连接别的账号时返回唯一约束错误。
func setTelegram(ctx context.Context, q DBTX, accountID int64, update func(userID, handle string) (string, string)) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE id = $1)`, accountID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return ErrNotFound
	}
	cur, err := ChatIdentityOf(ctx, q, accountID, ProviderTelegram)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	userID, handle := update(cur.UserID, cur.Handle)
	if userID == "" && handle == "" {
		return DeleteChatIdentity(ctx, q, accountID, ProviderTelegram)
	}
	return UpsertChatIdentity(ctx, q, ChatIdentity{AccountID: accountID, Provider: ProviderTelegram, UserID: userID, Handle: handle})
}

// SetAccountDisplayName 修改显示名。
func SetAccountDisplayName(ctx context.Context, q DBTX, accountID int64, display string) error {
	return execOne(ctx, q, `UPDATE accounts SET display_name = $2 WHERE id = $1`, accountID, display)
}

// SetServerAdmin 授予或撤销服务器管理员。
func SetServerAdmin(ctx context.Context, q DBTX, accountID int64, on bool) error {
	return execOne(ctx, q, `UPDATE accounts SET is_server_admin = $2 WHERE id = $1`, accountID, on)
}

// SetAccountDisabled 停用（by 为操作者，命令行时为空）或启用（disabled 为假）账号。
func SetAccountDisabled(ctx context.Context, q DBTX, accountID int64, disabled bool, by *int64) error {
	if disabled {
		return execOne(ctx, q, `UPDATE accounts SET disabled_at = now(), disabled_by = $2 WHERE id = $1`, accountID, by)
	}
	return execOne(ctx, q, `UPDATE accounts SET disabled_at = NULL, disabled_by = NULL WHERE id = $1`, accountID)
}

// AnyServerAdmin 返回是否有未停用的服务器管理员。
func AnyServerAdmin(ctx context.Context, q DBTX) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM accounts WHERE is_server_admin AND disabled_at IS NULL)`).Scan(&ok)
	return ok, err
}

// LockServerAdmins 在事务内锁住全部服务器管理员行，返回其中未停用的人数。
// 两名服务器管理员同时互相撤销或停用时排队执行，后一个会看到前一个的结果。
func LockServerAdmins(ctx context.Context, q DBTX) (int, error) {
	rows, err := q.Query(ctx, `SELECT disabled_at IS NULL FROM accounts WHERE is_server_admin ORDER BY id FOR UPDATE`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var active bool
		if err := rows.Scan(&active); err != nil {
			return 0, err
		}
		if active {
			n++
		}
	}
	return n, rows.Err()
}

// CountActiveServerAdmins 返回未停用的服务器管理员数。
func CountActiveServerAdmins(ctx context.Context, q DBTX) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM accounts WHERE is_server_admin AND disabled_at IS NULL`).Scan(&n)
	return n, err
}

// execOne 执行只影响一行的更新；没有命中时返回 ErrNotFound。
func execOne(ctx context.Context, q DBTX, sql string, args ...any) error {
	tag, err := q.Exec(ctx, sql, args...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ErrAccountDisabled 表示账号已停用。
var ErrAccountDisabled = errors.New("账号已停用，请联系服务器管理员")
