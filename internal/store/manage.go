package store

import (
	"context"
	"time"

	"github.com/cohive-ai/cohive/internal/issue"
)

// ---- 移出与转交 ----

// HandoverIssue 是成员名下一张尚未结束的问题单；As 为 owner（他是负责人）或 source（他是发起人）。
type HandoverIssue struct {
	IssueID int64
	Title   string
	Status  issue.Status
	As      string
	Version int
}

// 转交时“他名下”的角色
const (
	AsOwner  = "owner"
	AsSource = "source"
)

// PendingIssuesOf 列出成员名下尚未结束（不含草稿、已关闭、已退回）的问题单；
// 他既是负责人又是发起人的单子出现两行。
func PendingIssuesOf(ctx context.Context, q DBTX, memberID int64) ([]HandoverIssue, error) {
	rows, err := q.Query(ctx, `
		SELECT id, title, status, 'owner', version FROM issues
		WHERE owner_id = $1 AND status NOT IN ('DRAFT', 'CLOSED', 'DECLINED')
		UNION ALL
		SELECT id, title, status, 'source', version FROM issues
		WHERE source_member_id = $1 AND status NOT IN ('DRAFT', 'CLOSED', 'DECLINED')
		ORDER BY 1, 4`, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HandoverIssue
	for rows.Next() {
		var h HandoverIssue
		var st string
		if err := rows.Scan(&h.IssueID, &h.Title, &st, &h.As, &h.Version); err != nil {
			return nil, err
		}
		h.Status = issue.Status(st)
		out = append(out, h)
	}
	return out, rows.Err()
}

// ServicesOwnedBy 列出成员登记为负责人的服务名。
func ServicesOwnedBy(ctx context.Context, q DBTX, memberID int64) ([]string, error) {
	rows, err := q.Query(ctx, `SELECT name FROM services WHERE owner_id = $1 ORDER BY name`, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// SetServiceOwner 改服务负责人；服务不存在时返回 ErrNotFound。
func SetServiceOwner(ctx context.Context, q DBTX, wsID int64, name string, ownerID int64) error {
	return execOne(ctx, q, `UPDATE services SET owner_id = $3 WHERE workspace_id = $1 AND name = $2`, wsID, name, ownerID)
}

// SetIssueSource 改问题单发起人（管理员改派用；版本号由随后的 UpdateIssue 加一）。
// 发起人换了人，超时催办重新计时（待验证时催的是发起人）。
func SetIssueSource(ctx context.Context, q DBTX, issueID, sourceID int64) error {
	return execOne(ctx, q, `UPDATE issues SET
		status_since = CASE WHEN source_member_id IS DISTINCT FROM $2 THEN now() ELSE status_since END,
		remind_level = CASE WHEN source_member_id IS DISTINCT FROM $2 THEN 0 ELSE remind_level END,
		source_member_id = $2 WHERE id = $1`, issueID, sourceID)
}

// ---- 服务器管理用列表 ----

// AccountSummary 是服务器管理页账号列表的一行。
type AccountSummary struct {
	Account
	Devices     int
	Memberships []AccountMembership
}

// AccountMembership 是账号的一条成员资格（含已移出的）。
type AccountMembership struct {
	Workspace string
	Role      string
	Status    string
}

// ListAccountSummaries 列出账号及其成员资格、未吊销设备数；prefix 非空时按登录名前缀筛选。
func ListAccountSummaries(ctx context.Context, q DBTX, prefix string) ([]AccountSummary, error) {
	accs, err := ListAccounts(ctx, q, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]AccountSummary, 0, len(accs))
	for _, a := range accs {
		s := AccountSummary{Account: a}
		if err := q.QueryRow(ctx, `SELECT count(*) FROM devices WHERE account_id = $1 AND revoked_at IS NULL`, a.ID).
			Scan(&s.Devices); err != nil {
			return nil, err
		}
		rows, err := q.Query(ctx, `SELECT w.name, m.role, m.status FROM members m JOIN workspaces w ON w.id = m.workspace_id
			WHERE m.account_id = $1 ORDER BY w.name`, a.ID)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var ms AccountMembership
			if err := rows.Scan(&ms.Workspace, &ms.Role, &ms.Status); err != nil {
				rows.Close()
				return nil, err
			}
			s.Memberships = append(s.Memberships, ms)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// WorkspaceSummary 是服务器管理页工作空间列表的一行。
type WorkspaceSummary struct {
	Workspace
	CreatedAt     time.Time
	Members       int
	Admins        []string
	OpenIssues    int
	TelegramBound bool
}

// ListWorkspaceSummaries 列出全部工作空间及其在册成员数、管理员、未结束问题单数。
func ListWorkspaceSummaries(ctx context.Context, q DBTX) ([]WorkspaceSummary, error) {
	rows, err := q.Query(ctx, `SELECT `+workspaceColumns+`, created_at,
		(SELECT count(*) FROM members m WHERE m.workspace_id = w.id AND m.status = 'active'),
		coalesce((SELECT array_agg(a.name ORDER BY a.name) FROM members m JOIN accounts a ON a.id = m.account_id
			WHERE m.workspace_id = w.id AND m.status = 'active' AND m.role = 'admin'), '{}'),
		(SELECT count(*) FROM issues i WHERE i.workspace_id = w.id AND i.status NOT IN ('DRAFT', 'CLOSED', 'DECLINED'))
		FROM workspaces w ORDER BY w.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkspaceSummary
	for rows.Next() {
		var s WorkspaceSummary
		if err := rows.Scan(&s.ID, &s.Name, &s.DisplayName, &s.Notify.Channel, &s.Notify.Target, &s.Notify.ShowTitle,
			&s.Notify.ShowBrief, &s.Remind.OpenHours, &s.Remind.VerifyHours, &s.CreatedAt, &s.Members, &s.Admins, &s.OpenIssues); err != nil {
			return nil, err
		}
		s.TelegramBound = s.Notify.Target != "" // 绑了群（任一种通讯工具）；接口字段名沿用 telegram_bound
		out = append(out, s)
	}
	return out, rows.Err()
}

// LockWorkspace 在事务内锁住工作空间行。
func LockWorkspace(ctx context.Context, q DBTX, wsID int64) (Workspace, error) {
	return scanWorkspace(q.QueryRow(ctx, `SELECT `+workspaceColumns+` FROM workspaces w WHERE w.id = $1 FOR UPDATE OF w`, wsID))
}

// CountIssues 返回工作空间的问题单数（含草稿）。
func CountIssues(ctx context.Context, q DBTX, wsID int64) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM issues WHERE workspace_id = $1`, wsID).Scan(&n)
	return n, err
}

// DeleteEmptyWorkspace 删除没有问题单的工作空间及其邀请、申请、服务、成员资格（及其令牌）、群审批消息。
// 调用方须先确认没有问题单。
func DeleteEmptyWorkspace(ctx context.Context, q DBTX, wsID int64) error {
	for _, sql := range []string{
		`DELETE FROM bot_posts WHERE workspace_id = $1`,
		`DELETE FROM workspace_chats WHERE workspace_id = $1`,
		`DELETE FROM chat_bind_requests WHERE workspace_id = $1`,
		`DELETE FROM signups WHERE workspace_id = $1`,
		`DELETE FROM invites WHERE workspace_id = $1`,
		`DELETE FROM services WHERE workspace_id = $1`,
		`DELETE FROM idempotency WHERE token_id IN (SELECT t.id FROM tokens t JOIN members m ON m.id = t.member_id WHERE m.workspace_id = $1)`,
		`DELETE FROM tokens WHERE member_id IN (SELECT id FROM members WHERE workspace_id = $1)`,
		`DELETE FROM login_codes WHERE member_id IN (SELECT id FROM members WHERE workspace_id = $1)`,
		`DELETE FROM members WHERE workspace_id = $1`,
	} {
		if _, err := q.Exec(ctx, sql, wsID); err != nil {
			return err
		}
	}
	return execOne(ctx, q, `DELETE FROM workspaces WHERE id = $1`, wsID)
}
