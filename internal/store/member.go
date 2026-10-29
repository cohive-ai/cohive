package store

import (
	"context"
	"errors"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/jackc/pgx/v5/pgconn"
)

// 成员资格状态
const (
	MemberActive  = "active"  // 在册
	MemberRemoved = "removed" // 已移出；历史问题单仍指向它，再次加入时复用同一行
)

// memberSelect 是读取成员资格的公共列与来源：名字、显示名、Telegram 来自账号。
const memberColumns = `m.id, m.workspace_id, a.name, a.display_name, coalesce((SELECT ci.handle FROM chat_identities ci WHERE ci.account_id = a.id AND ci.provider = 'telegram'), ''),
	(SELECT nullif(ci.user_id, '')::bigint FROM chat_identities ci WHERE ci.account_id = a.id AND ci.provider = 'telegram'), m.role,
	m.account_id, m.status, a.disabled_at IS NOT NULL`

const memberFrom = ` FROM members m JOIN accounts a ON a.id = m.account_id `

func scanMember(row interface{ Scan(...any) error }) (Member, error) {
	var m Member
	err := row.Scan(&m.ID, &m.WorkspaceID, &m.Name, &m.DisplayName, &m.TelegramUsername, &m.TelegramUserID, &m.Role,
		&m.AccountID, &m.Status, &m.AccountDisabled)
	return m, notFound(err)
}

func collectMembers(ctx context.Context, q DBTX, sql string, args ...any) ([]Member, error) {
	rows, err := q.Query(ctx, sql, args...)
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

// MemberByID 返回成员资格（任何状态）。
func MemberByID(ctx context.Context, q DBTX, id int64) (Member, error) {
	return scanMember(q.QueryRow(ctx, `SELECT `+memberColumns+memberFrom+`WHERE m.id = $1`, id))
}

// MemberByName 返回工作空间里该登录名的在册成员。
func MemberByName(ctx context.Context, q DBTX, wsID int64, name string) (Member, error) {
	return scanMember(q.QueryRow(ctx, `SELECT `+memberColumns+memberFrom+`
		WHERE m.workspace_id = $1 AND a.name = $2 AND m.status = 'active'`, wsID, name))
}

// MemberMatch 是按名字检索到的一个成员。
type MemberMatch struct {
	Member
	Exact    bool     // 登录名或昵称与检索词完全相同（不区分大小写）
	Services []string // 登记为负责人的服务名
}

// FindMembers 在工作空间的在册、账号未停用的成员里按登录名、昵称检索（不区分大小写）：
// 完全相同的排在前面，其次是包含检索词的；检索词为空时列出全部。最多返回 limit 条，
// 第二个返回值表示是否还有更多没返回。
func FindMembers(ctx context.Context, q DBTX, wsID int64, query string, limit int) ([]MemberMatch, bool, error) {
	// 用 strpos 而不是 LIKE：检索词里的 % 和 _ 按原样匹配
	rows, err := q.Query(ctx, `SELECT `+memberColumns+`,
		($2 <> '' AND (lower(a.name) = lower($2) OR lower(a.display_name) = lower($2))) AS exact,
		coalesce(array(SELECT s.name FROM services s WHERE s.owner_id = m.id ORDER BY s.name), '{}')`+memberFrom+`
		WHERE m.workspace_id = $1 AND m.status = 'active' AND a.disabled_at IS NULL
		  AND ($2 = '' OR strpos(lower(a.name), lower($2)) > 0 OR strpos(lower(a.display_name), lower($2)) > 0)
		ORDER BY exact DESC, a.name LIMIT $3`, wsID, query, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []MemberMatch
	for rows.Next() {
		var mm MemberMatch
		m := &mm.Member
		if err := rows.Scan(&m.ID, &m.WorkspaceID, &m.Name, &m.DisplayName, &m.TelegramUsername, &m.TelegramUserID, &m.Role,
			&m.AccountID, &m.Status, &m.AccountDisabled, &mm.Exact, &mm.Services); err != nil {
			return nil, false, err
		}
		out = append(out, mm)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// MembersNamedLike 返回工作空间里登录名或昵称与 name 相同（不区分大小写）的在册、账号未停用的成员，
// 指派前用来发现“传的是昵称”或“登录名与别人的昵称撞了”。
func MembersNamedLike(ctx context.Context, q DBTX, wsID int64, name string) ([]Member, error) {
	return collectMembers(ctx, q, `SELECT `+memberColumns+memberFrom+`
		WHERE m.workspace_id = $1 AND m.status = 'active' AND a.disabled_at IS NULL
		  AND (lower(a.name) = lower($2) OR lower(a.display_name) = lower($2))
		ORDER BY a.name`, wsID, name)
}

// MemberOf 返回账号在工作空间里的成员资格（任何状态）。
func MemberOf(ctx context.Context, q DBTX, accountID, wsID int64) (Member, error) {
	return scanMember(q.QueryRow(ctx, `SELECT `+memberColumns+memberFrom+`
		WHERE m.account_id = $1 AND m.workspace_id = $2`, accountID, wsID))
}

// LockMember 在事务内锁住成员资格行并返回最新内容。
// 用 FOR NO KEY UPDATE 而不是 FOR UPDATE：成员行从不删除、主键不变，这把锁只为让管理操作互相排队；
// FOR UPDATE 会挡住其他事务按外键引用这一行（写问题单事件、通知等要的 FOR KEY SHARE），
// 而那些事务往往已持有问题单行锁，两边互等就是死锁。加锁顺序见 core 包的说明。
func LockMember(ctx context.Context, q DBTX, memberID int64) (Member, error) {
	return scanMember(q.QueryRow(ctx, `SELECT `+memberColumns+memberFrom+`WHERE m.id = $1 FOR NO KEY UPDATE OF m`, memberID))
}

// ErrAlreadyMember 表示账号已经是该工作空间的在册成员。
var ErrAlreadyMember = i18n.Errorf("store.err.already_member")

// AddMembership 把账号加入工作空间：没有成员资格时新建，已移出时复用原成员资格改回在册（角色按本次）。
// 已是在册成员时返回 ErrAlreadyMember。
func AddMembership(ctx context.Context, q DBTX, accountID, wsID int64, role string, addedBy *int64) (Member, error) {
	var id int64
	var status string
	err := q.QueryRow(ctx, `SELECT id, status FROM members WHERE account_id = $1 AND workspace_id = $2 FOR UPDATE`,
		accountID, wsID).Scan(&id, &status)
	switch {
	case errors.Is(notFound(err), ErrNotFound):
		if err := q.QueryRow(ctx, `INSERT INTO members (workspace_id, account_id, role, added_by)
			VALUES ($1, $2, $3, $4) RETURNING id`, wsID, accountID, role, addedBy).Scan(&id); err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23505" {
				return Member{}, ErrAlreadyMember // 同时加入同一个人：唯一约束兜底
			}
			return Member{}, err
		}
	case err != nil:
		return Member{}, err
	case status == MemberActive:
		return Member{}, ErrAlreadyMember
	default:
		if _, err := q.Exec(ctx, `UPDATE members SET status = 'active', role = $2, added_by = $3,
			removed_at = NULL, removed_by = NULL WHERE id = $1`, id, role, addedBy); err != nil {
			return Member{}, err
		}
	}
	return MemberByID(ctx, q, id)
}

// CreateMember 为登录名 name 在工作空间里建成员资格：账号不存在时新建（没有密码），
// 存在时直接加入（命令行 member add 与测试夹具用）。
func CreateMember(ctx context.Context, q DBTX, wsID int64, name, display string) (Member, error) {
	acc, err := AccountByName(ctx, q, name)
	if errors.Is(err, ErrNotFound) {
		acc, err = CreateAccount(ctx, q, name, display)
	}
	if err != nil {
		return Member{}, err
	}
	return AddMembership(ctx, q, acc.ID, wsID, RoleMember, nil)
}

// CountActiveMemberTokens 返回成员资格未吊销的令牌数。
func CountActiveMemberTokens(ctx context.Context, q DBTX, memberID int64) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM tokens WHERE member_id = $1 AND revoked_at IS NULL`, memberID).Scan(&n)
	return n, err
}

// RemoveMembership 把成员资格标为已移出，并吊销它的全部令牌。
func RemoveMembership(ctx context.Context, q DBTX, memberID int64, by *int64) error {
	if err := execOne(ctx, q, `UPDATE members SET status = 'removed', removed_at = now(), removed_by = $2
		WHERE id = $1 AND status = 'active'`, memberID, by); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `UPDATE tokens SET revoked_at = now() WHERE member_id = $1 AND revoked_at IS NULL`, memberID)
	return err
}

// ListMembers 列出工作空间的在册成员（含已停用账号的）。
func ListMembers(ctx context.Context, q DBTX, wsID int64) ([]Member, error) {
	return collectMembers(ctx, q, `SELECT `+memberColumns+memberFrom+`
		WHERE m.workspace_id = $1 AND m.status = 'active' ORDER BY m.id`, wsID)
}

// ListAllMembers 列出工作空间的全部成员资格（含已移出的），群通知渲染历史操作人时用。
func ListAllMembers(ctx context.Context, q DBTX, wsID int64) ([]Member, error) {
	return collectMembers(ctx, q, `SELECT `+memberColumns+memberFrom+`WHERE m.workspace_id = $1 ORDER BY m.id`, wsID)
}

// Membership 是账号所在的一个工作空间。
type Membership struct {
	MemberID    int64
	WorkspaceID int64
	Workspace   string // 路径
	Label       string // 显示名，没设时为路径
	Role        string
	Archived    bool // 工作空间已归档（只读）
}

// AccountMemberships 列出账号在册的全部工作空间。
func AccountMemberships(ctx context.Context, q DBTX, accountID int64) ([]Membership, error) {
	rows, err := q.Query(ctx, `SELECT m.id, w.id, w.name, coalesce(nullif(w.display_name, ''), w.name), m.role, w.archived_at IS NOT NULL
		FROM members m JOIN workspaces w ON w.id = m.workspace_id
		WHERE m.account_id = $1 AND m.status = 'active' ORDER BY w.name`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Membership
	for rows.Next() {
		var ms Membership
		if err := rows.Scan(&ms.MemberID, &ms.WorkspaceID, &ms.Workspace, &ms.Label, &ms.Role, &ms.Archived); err != nil {
			return nil, err
		}
		out = append(out, ms)
	}
	return out, rows.Err()
}

// IsWorkspaceAdmin 返回账号是否为该工作空间在册的管理员。
func IsWorkspaceAdmin(ctx context.Context, q DBTX, accountID, wsID int64) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE account_id = $1 AND workspace_id = $2
		AND status = 'active' AND role = 'admin')`, accountID, wsID).Scan(&ok)
	return ok, err
}

// AdministersMemberOf 返回是否存在某个工作空间：target 在册，且 actor 是它在册的管理员。
func AdministersMemberOf(ctx context.Context, q DBTX, actorID, targetID int64) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members t JOIN members a ON a.workspace_id = t.workspace_id
		WHERE t.account_id = $2 AND t.status = 'active'
		  AND a.account_id = $1 AND a.status = 'active' AND a.role = 'admin')`, actorID, targetID).Scan(&ok)
	return ok, err
}

// IsActiveMemberOf 返回账号是否为工作空间的在册成员。
func IsActiveMemberOf(ctx context.Context, q DBTX, accountID, wsID int64) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE account_id = $1 AND workspace_id = $2
		AND status = 'active')`, accountID, wsID).Scan(&ok)
	return ok, err
}

// 昵称撞名的类别
const (
	ClashName        = "name"         // 与别人的登录名相同
	ClashDisplayName = "display_name" // 与别人的昵称相同
)

// DisplayNameClash 检查 display 是否与账号所在任一工作空间里其他在册、账号未停用成员的登录名或昵称相同
// （不区分大小写、忽略首尾空白）。返回撞上的类别（ClashName 或 ClashDisplayName），没撞返回空串；两样都撞时报登录名。
func DisplayNameClash(ctx context.Context, q DBTX, accountID int64, display string) (string, error) {
	var byName, byDisplay bool
	err := q.QueryRow(ctx, `SELECT coalesce(bool_or(lower(a.name) = lower(btrim($2, E' \t\r\n\v\f'))), false),
		coalesce(bool_or(a.display_name <> '' AND lower(btrim(a.display_name, E' \t\r\n\v\f')) = lower(btrim($2, E' \t\r\n\v\f'))), false)
		FROM members me JOIN members m ON m.workspace_id = me.workspace_id JOIN accounts a ON a.id = m.account_id
		WHERE me.account_id = $1 AND me.status = 'active' AND m.status = 'active'
		  AND a.id <> $1 AND a.disabled_at IS NULL`, accountID, display).Scan(&byName, &byDisplay)
	switch {
	case err != nil:
		return "", err
	case byName:
		return ClashName, nil
	case byDisplay:
		return ClashDisplayName, nil
	}
	return "", nil
}

// LockDisplayNames 在事务内取一把全局的事务级咨询锁，让本人改昵称的操作互相排队，
// 避免两人同时改成同一个昵称时都通过撞名检查。只在事务开头、锁任何行之前调用。
func LockDisplayNames(ctx context.Context, q DBTX) error {
	_, err := q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('cohive.display_name'))`)
	return err
}
