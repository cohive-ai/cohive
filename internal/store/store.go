// Package store 是 Cohive 的 Postgres 读写层。所有函数接收 DBTX，
// 业务层可以把多个调用组合在同一个事务里。
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var (
	ErrNotFound        = i18n.Errorf("store.err.not_found")
	ErrUnauthorized    = i18n.Errorf("store.err.unauthorized")
	ErrVersionConflict = i18n.Errorf("store.err.version_conflict")
)

// DBTX 同时由 *pgxpool.Pool 和 pgx.Tx 实现。
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Member 是成员资格：某个账号在某个工作空间里的身份。名字、显示名、Telegram 取自账号。
type Member struct {
	ID               int64
	WorkspaceID      int64
	Name             string // 账号的登录名
	DisplayName      string
	TelegramUsername string
	TelegramUserID   *int64 // 账号连接的 Telegram 账号编号，用于识别群里点审批按钮的人
	Role             string // member 或 admin；管理员可以生成邀请、审批配对申请
	AccountID        int64
	Status           string // active 或 removed
	AccountDisabled  bool
}

// Active 判断成员资格在册且账号未停用。
func (m Member) Active() bool { return m.Status == MemberActive && !m.AccountDisabled }

// 成员角色
const (
	RoleMember = "member"
	RoleAdmin  = "admin"
)

// IsAdmin 判断成员是否为管理员。
func (m Member) IsAdmin() bool { return m.Role == RoleAdmin }

// Identity 是一次调用的操作者：哪个人的哪个工具。
type Identity struct {
	TokenID     int64
	AccountID   int64
	MemberID    int64
	WorkspaceID int64
	MemberName  string
	Tool        string
	DeviceID    *int64 // 令牌所属设备；管理员手工签发的令牌为空
	Language    string // 账号语言代码；空表示跟随服务器缺省语言（MCP 按它选工具说明的语言）
}

type Issue struct {
	ID               int64
	WorkspaceID      int64
	SourceMemberID   int64
	SourceName       string // 只读，查询时关联得到
	SourceTool       string
	OwnerID          *int64
	OwnerName        string // 只读，查询时关联得到
	TargetService    string
	Severity         string
	Title            string
	Brief            string // 群通知简述，会发到工作空间的群里
	Summary          string
	Expected         string
	Actual           string
	ReproScript      string
	ReproEnv         string
	ReproExitCode    *int
	Evidence         string
	Repo             string
	Branch           string
	CommitSHA        string
	Status           issue.Status
	Version          int
	ReopenCount      int
	EscalatedAt      *time.Time
	EscalationReason string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Event struct {
	ID            int64
	IssueID       int64
	ActorMemberID int64
	ActorName     string // 只读
	ActorTool     string
	Action        issue.Action
	FromStatus    issue.Status
	ToStatus      issue.Status
	Note          string
	Payload       map[string]any
	CreatedAt     time.Time
}

func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// ---- 工作空间与成员 ----

// DefaultWorkspace 返回名为 default 的工作空间，不存在则创建。
func DefaultWorkspace(ctx context.Context, q DBTX) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO workspaces (name) VALUES ('default')
		ON CONFLICT (name) DO UPDATE SET name = EXCLUDED.name
		RETURNING id`).Scan(&id)
	return id, err
}

// ---- 令牌 ----

func hashToken(plain string) []byte {
	h := sha256.Sum256([]byte(plain))
	return h[:]
}

// IssueToken 为成员的某个工具签发令牌。明文只在此返回一次，库里只存哈希。
func IssueToken(ctx context.Context, q DBTX, memberID int64, tool string) (plain string, tokenID int64, err error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", 0, err
	}
	plain = "cohive_" + base64.RawURLEncoding.EncodeToString(buf)
	err = q.QueryRow(ctx,
		`INSERT INTO tokens (member_id, tool, token_hash) VALUES ($1, $2, $3) RETURNING id`,
		memberID, tool, hashToken(plain)).Scan(&tokenID)
	return plain, tokenID, err
}

// LookupToken 按令牌找身份。查询末尾再查一次成员资格与账号状态是第二道防线：移出、停用时令牌已吊销，这里再查一次。
func LookupToken(ctx context.Context, q DBTX, plain string) (Identity, error) {
	var id Identity
	err := q.QueryRow(ctx, `
		SELECT t.id, a.id, m.id, m.workspace_id, a.name, t.tool, t.device_id, coalesce(a.language, '')
		FROM tokens t JOIN members m ON m.id = t.member_id
		JOIN accounts a ON a.id = m.account_id
		LEFT JOIN devices d ON d.id = t.device_id
		WHERE t.token_hash = $1 AND t.revoked_at IS NULL
		AND (t.device_id IS NULL OR d.revoked_at IS NULL)
		AND m.status = 'active' AND a.disabled_at IS NULL`,
		hashToken(plain)).Scan(&id.TokenID, &id.AccountID, &id.MemberID, &id.WorkspaceID, &id.MemberName, &id.Tool, &id.DeviceID, &id.Language)
	if errors.Is(err, pgx.ErrNoRows) {
		return Identity{}, ErrUnauthorized
	}
	return id, err
}

func RevokeToken(ctx context.Context, q DBTX, tokenID int64) error {
	tag, err := q.Exec(ctx, `UPDATE tokens SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, tokenID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeTokenInWorkspace 只吊销属于指定工作空间成员的令牌。
func RevokeTokenInWorkspace(ctx context.Context, q DBTX, wsID, tokenID int64) error {
	tag, err := q.Exec(ctx, `UPDATE tokens t SET revoked_at = now() FROM members m
		WHERE t.id = $2 AND t.member_id = m.id AND m.workspace_id = $1 AND t.revoked_at IS NULL`, wsID, tokenID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- 服务登记 ----

func UpsertService(ctx context.Context, q DBTX, wsID int64, name string, ownerID int64) error {
	_, err := q.Exec(ctx, `
		INSERT INTO services (workspace_id, name, owner_id) VALUES ($1, $2, $3)
		ON CONFLICT (workspace_id, name) DO UPDATE SET owner_id = EXCLUDED.owner_id`,
		wsID, name, ownerID)
	return err
}

// ServiceOwner 返回服务的负责人；负责人已移出时视为未登记。
func ServiceOwner(ctx context.Context, q DBTX, wsID int64, service string) (Member, error) {
	return scanMember(q.QueryRow(ctx, `SELECT `+memberColumns+`
		FROM services s JOIN members m ON m.id = s.owner_id JOIN accounts a ON a.id = m.account_id
		WHERE s.workspace_id = $1 AND s.name = $2 AND m.status = 'active'`, wsID, service))
}

// ---- 问题单 ----

const issueColumns = `
	i.id, i.workspace_id, i.source_member_id, sa.name, i.source_tool, i.owner_id, COALESCE(oa.name, ''),
	i.target_service, i.severity, i.title, i.brief, i.summary, i.expected, i.actual,
	i.repro_script, i.repro_env, i.repro_exit_code, i.evidence, i.repo, i.branch, i.commit_sha,
	i.status, i.version, i.reopen_count, i.escalated_at, i.escalation_reason, i.created_at, i.updated_at`

const issueFrom = `
	FROM issues i
	JOIN members sm ON sm.id = i.source_member_id
	JOIN accounts sa ON sa.id = sm.account_id
	LEFT JOIN members om ON om.id = i.owner_id
	LEFT JOIN accounts oa ON oa.id = om.account_id`

func scanIssue(row pgx.Row) (Issue, error) {
	var is Issue
	var status string
	err := row.Scan(&is.ID, &is.WorkspaceID, &is.SourceMemberID, &is.SourceName, &is.SourceTool,
		&is.OwnerID, &is.OwnerName, &is.TargetService, &is.Severity, &is.Title, &is.Brief, &is.Summary,
		&is.Expected, &is.Actual, &is.ReproScript, &is.ReproEnv, &is.ReproExitCode, &is.Evidence,
		&is.Repo, &is.Branch, &is.CommitSHA, &status, &is.Version, &is.ReopenCount,
		&is.EscalatedAt, &is.EscalationReason, &is.CreatedAt, &is.UpdatedAt)
	if err != nil {
		return Issue{}, notFound(err)
	}
	is.Status, err = issue.ParseStatus(status)
	return is, err
}

func collectIssues(rows pgx.Rows) ([]Issue, error) {
	defer rows.Close()
	var out []Issue
	for rows.Next() {
		is, err := scanIssue(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, is)
	}
	return out, rows.Err()
}

// InsertIssue 写入新问题单，回填 ID、版本与时间。
func InsertIssue(ctx context.Context, q DBTX, is *Issue) error {
	return q.QueryRow(ctx, `
		INSERT INTO issues (workspace_id, source_member_id, source_tool, owner_id, target_service,
			severity, title, summary, expected, actual, repro_script, repro_env, repro_exit_code,
			evidence, repo, branch, commit_sha, status, brief)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
		RETURNING id, version, created_at, updated_at`,
		is.WorkspaceID, is.SourceMemberID, is.SourceTool, is.OwnerID, is.TargetService,
		is.Severity, is.Title, is.Summary, is.Expected, is.Actual, is.ReproScript, is.ReproEnv,
		is.ReproExitCode, is.Evidence, is.Repo, is.Branch, is.CommitSHA, string(is.Status), is.Brief,
	).Scan(&is.ID, &is.Version, &is.CreatedAt, &is.UpdatedAt)
}

func GetIssue(ctx context.Context, q DBTX, id int64) (Issue, error) {
	return scanIssue(q.QueryRow(ctx, `SELECT `+issueColumns+issueFrom+` WHERE i.id = $1`, id))
}

// LockIssues 在事务内按编号升序锁住工作空间里的这些问题单（不存在或不属于该空间的编号忽略）。
// 一次锁多张单时必须走这里，按固定顺序加锁，免得两个事务各锁一半互等。
func LockIssues(ctx context.Context, q DBTX, wsID int64, ids []int64) error {
	rows, err := q.Query(ctx, `SELECT id FROM issues WHERE workspace_id = $1 AND id = ANY($2) ORDER BY id FOR UPDATE`, wsID, ids)
	if err != nil {
		return err
	}
	rows.Close()
	return rows.Err()
}

// GetIssueForUpdate 在事务内读取并锁定问题单。
func GetIssueForUpdate(ctx context.Context, q DBTX, id int64) (Issue, error) {
	return scanIssue(q.QueryRow(ctx, `SELECT `+issueColumns+issueFrom+` WHERE i.id = $1 FOR UPDATE OF i`, id))
}

// UpdateIssue 按 is.Version 做乐观并发更新，成功后版本加一。
// 版本号是“状态、归属与正文”的版本，评论不经这里（见 TouchIssue）。
// 超时催办：状态或负责人一变就重新计时、档位归零（SQL 里 CASE 右边引用的是更新前的值）。
func UpdateIssue(ctx context.Context, q DBTX, is *Issue) error {
	err := q.QueryRow(ctx, `
		UPDATE issues SET owner_id=$3, target_service=$4, severity=$5, title=$6, summary=$7,
			expected=$8, actual=$9, repro_script=$10, repro_env=$11, repro_exit_code=$12,
			evidence=$13, repo=$14, branch=$15, commit_sha=$16, status=$17, reopen_count=$18,
			escalated_at=$19, escalation_reason=$20, brief=$21, version=version+1, updated_at=now(),
			status_since = CASE WHEN status IS DISTINCT FROM $17 OR owner_id IS DISTINCT FROM $3 THEN now() ELSE status_since END,
			remind_level = CASE WHEN status IS DISTINCT FROM $17 OR owner_id IS DISTINCT FROM $3 THEN 0 ELSE remind_level END
		WHERE id=$1 AND version=$2
		RETURNING version, updated_at`,
		is.ID, is.Version, is.OwnerID, is.TargetService, is.Severity, is.Title, is.Summary,
		is.Expected, is.Actual, is.ReproScript, is.ReproEnv, is.ReproExitCode, is.Evidence,
		is.Repo, is.Branch, is.CommitSHA, string(is.Status), is.ReopenCount,
		is.EscalatedAt, is.EscalationReason, is.Brief,
	).Scan(&is.Version, &is.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrVersionConflict
	}
	return err
}

// TouchIssue 只刷新更新时间，不改版本号：评论等不改变状态、归属与正文的写入用它，
// 收件箱排序与“7 天内被退回”的窗口照旧随之更新。
func TouchIssue(ctx context.Context, q DBTX, id int64) error {
	tag, err := q.Exec(ctx, `UPDATE issues SET updated_at = now() WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListInbox 返回需要该成员处理的问题单：
// 作为负责人的待接单、已接单、修复中、已重开；作为发起人的草稿、待验证，以及 7 天内被退回的单。
func ListInbox(ctx context.Context, q DBTX, memberID int64) ([]Issue, error) {
	rows, err := q.Query(ctx, `SELECT `+issueColumns+issueFrom+`
		WHERE (i.owner_id = $1 AND i.status IN ('OPEN','ACCEPTED','FIXING','REOPENED'))
		   OR (i.source_member_id = $1 AND i.status IN ('DRAFT','READY_FOR_VERIFICATION'))
		   OR (i.source_member_id = $1 AND i.status = 'DECLINED' AND i.updated_at > now() - interval '7 days')
		ORDER BY i.updated_at DESC, i.id DESC`, memberID)
	if err != nil {
		return nil, err
	}
	return collectIssues(rows)
}

// SearchIssues 在工作空间内按关键词与服务查找问题单，用于查重。
// 别人的草稿不可见。query 为空时不按关键词过滤。
func SearchIssues(ctx context.Context, q DBTX, wsID, viewerID int64, query, service string, limit int) ([]Issue, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := q.Query(ctx, `SELECT `+issueColumns+issueFrom+`
		WHERE i.workspace_id = $1
		  AND (i.status <> 'DRAFT' OR i.source_member_id = $2)
		  AND ($3 = '' OR i.title ILIKE '%' || $3 || '%' OR i.summary ILIKE '%' || $3 || '%'
		       OR i.actual ILIKE '%' || $3 || '%')
		  AND ($4 = '' OR i.target_service = $4)
		ORDER BY i.updated_at DESC, i.id DESC
		LIMIT $5`, wsID, viewerID, query, service, limit)
	if err != nil {
		return nil, err
	}
	return collectIssues(rows)
}

// ---- 事件 ----

func InsertEvent(ctx context.Context, q DBTX, ev Event) error {
	payload := ev.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("issue event payload: %w", err)
	}
	_, err = q.Exec(ctx, `
		INSERT INTO issue_events (issue_id, actor_member_id, actor_tool, action, from_status, to_status, note, payload)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`,
		ev.IssueID, ev.ActorMemberID, ev.ActorTool, string(ev.Action),
		string(ev.FromStatus), string(ev.ToStatus), ev.Note, b)
	return err
}

func ListEvents(ctx context.Context, q DBTX, issueID int64) ([]Event, error) {
	rows, err := q.Query(ctx, `
		SELECT e.id, e.issue_id, e.actor_member_id, a.name, e.actor_tool, e.action,
		       e.from_status, e.to_status, e.note, e.payload, e.created_at
		FROM issue_events e JOIN members m ON m.id = e.actor_member_id JOIN accounts a ON a.id = m.account_id
		WHERE e.issue_id = $1 ORDER BY e.id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var ev Event
		var action, from, to string
		var payload []byte
		if err := rows.Scan(&ev.ID, &ev.IssueID, &ev.ActorMemberID, &ev.ActorName, &ev.ActorTool,
			&action, &from, &to, &ev.Note, &payload, &ev.CreatedAt); err != nil {
			return nil, err
		}
		ev.Action, ev.FromStatus, ev.ToStatus = issue.Action(action), issue.Status(from), issue.Status(to)
		if err := json.Unmarshal(payload, &ev.Payload); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

// ---- 幂等 ----

// IdemRecord 记录某个幂等键第一次用于哪张问题单的哪个动作。
type IdemRecord struct {
	IssueID int64
	Action  string
}

func GetIdempotent(ctx context.Context, q DBTX, tokenID int64, key string) (IdemRecord, bool, error) {
	var r IdemRecord
	err := q.QueryRow(ctx, `SELECT issue_id, action FROM idempotency WHERE token_id = $1 AND key = $2`,
		tokenID, key).Scan(&r.IssueID, &r.Action)
	if errors.Is(err, pgx.ErrNoRows) {
		return IdemRecord{}, false, nil
	}
	return r, err == nil, err
}

func PutIdempotent(ctx context.Context, q DBTX, tokenID int64, key string, r IdemRecord) error {
	_, err := q.Exec(ctx, `INSERT INTO idempotency (token_id, key, issue_id, action) VALUES ($1, $2, $3, $4)`,
		tokenID, key, r.IssueID, r.Action)
	return err
}

// ---- 管理用列表 ----

type TokenInfo struct {
	ID         int64
	MemberName string
	Tool       string
	CreatedAt  time.Time
	RevokedAt  *time.Time
}

type ServiceInfo struct {
	Name      string
	OwnerName string
}

func ListTokens(ctx context.Context, q DBTX, wsID int64) ([]TokenInfo, error) {
	rows, err := q.Query(ctx, `
		SELECT t.id, a.name, t.tool, t.created_at, t.revoked_at
		FROM tokens t JOIN members m ON m.id = t.member_id JOIN accounts a ON a.id = m.account_id
		WHERE m.workspace_id = $1 ORDER BY t.id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TokenInfo
	for rows.Next() {
		var ti TokenInfo
		if err := rows.Scan(&ti.ID, &ti.MemberName, &ti.Tool, &ti.CreatedAt, &ti.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, ti)
	}
	return out, rows.Err()
}

func ListServices(ctx context.Context, q DBTX, wsID int64) ([]ServiceInfo, error) {
	rows, err := q.Query(ctx, `
		SELECT s.name, a.name FROM services s JOIN members m ON m.id = s.owner_id JOIN accounts a ON a.id = m.account_id
		WHERE s.workspace_id = $1 ORDER BY s.name`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceInfo
	for rows.Next() {
		var si ServiceInfo
		if err := rows.Scan(&si.Name, &si.OwnerName); err != nil {
			return nil, err
		}
		out = append(out, si)
	}
	return out, rows.Err()
}

// DeleteIssue 删除一张问题单及其附属行（事件、幂等记录、会话内提醒、群通知）。只用于删除草稿：
// 草稿只有发起人看得见，删掉不留痕。
func DeleteIssue(ctx context.Context, q DBTX, id int64) error {
	for _, sql := range []string{
		`DELETE FROM idempotency WHERE issue_id = $1`,
		`DELETE FROM issue_events WHERE issue_id = $1`,
		`DELETE FROM member_alerts WHERE issue_id = $1`,
		`DELETE FROM notifications WHERE issue_id = $1`,
	} {
		if _, err := q.Exec(ctx, sql, id); err != nil {
			return err
		}
	}
	tag, err := q.Exec(ctx, `DELETE FROM issues WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
