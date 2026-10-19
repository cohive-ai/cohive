package store

import (
	"context"
	"time"
)

// 通讯工具。数据库的取值约束（迁移 0014）已放开到规划中的全部平台：telegram、dingtalk、slack、feishu、lark、discord。
const (
	ProviderTelegram = "telegram"
	ProviderDingTalk = "dingtalk"
	ProviderSlack    = "slack"
)

// Providers 按显示顺序列出已接入的通讯工具。
var Providers = []string{ProviderTelegram, ProviderDingTalk, ProviderSlack}

// ChatIdentity 是账号在某种通讯工具上连接的账号：一个 Cohive 账号每种通讯工具最多一个，
// 一个通讯账号（provider + tenant + user_id）全服务器只能连一个 Cohive 账号。
type ChatIdentity struct {
	AccountID   int64
	Provider    string
	Tenant      string // Telegram 为空；钉钉为 corpId；Slack 为工作区编号（team_id）
	UserID      string // Telegram 用户编号；钉钉 userId；Slack 用户编号。空表示只登记了用户名（Telegram 命令行兜底）
	Handle      string // Telegram 用户名
	DisplayName string // 钉钉昵称
	LinkedAt    time.Time
}

const chatIdentityColumns = `account_id, provider, tenant, user_id, handle, display_name, linked_at`

func scanChatIdentity(row interface{ Scan(...any) error }) (ChatIdentity, error) {
	var c ChatIdentity
	err := row.Scan(&c.AccountID, &c.Provider, &c.Tenant, &c.UserID, &c.Handle, &c.DisplayName, &c.LinkedAt)
	return c, notFound(err)
}

// ChatIdentityOf 返回账号在某种通讯工具上的连接。
func ChatIdentityOf(ctx context.Context, q DBTX, accountID int64, provider string) (ChatIdentity, error) {
	return scanChatIdentity(q.QueryRow(ctx, `SELECT `+chatIdentityColumns+` FROM chat_identities
		WHERE account_id = $1 AND provider = $2`, accountID, provider))
}

// ChatIdentitiesOf 返回一批账号在全部通讯工具上的连接，按账号分组。
func ChatIdentitiesOf(ctx context.Context, q DBTX, accountIDs []int64) (map[int64][]ChatIdentity, error) {
	out := map[int64][]ChatIdentity{}
	if len(accountIDs) == 0 {
		return out, nil
	}
	rows, err := q.Query(ctx, `SELECT `+chatIdentityColumns+` FROM chat_identities
		WHERE account_id = ANY($1) ORDER BY account_id, provider`, accountIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanChatIdentity(rows)
		if err != nil {
			return nil, err
		}
		out[c.AccountID] = append(out[c.AccountID], c)
	}
	return out, rows.Err()
}

// AccountByChatIdentity 返回连接到该通讯账号的 Cohive 账号。
func AccountByChatIdentity(ctx context.Context, q DBTX, provider, tenant, userID string) (Account, error) {
	if userID == "" {
		return Account{}, ErrNotFound
	}
	return scanAccount(q.QueryRow(ctx, `SELECT `+accountColumns+` FROM accounts a
		WHERE a.id = (SELECT account_id FROM chat_identities WHERE provider = $1 AND tenant = $2 AND user_id = $3)`,
		provider, tenant, userID))
}

// UpsertChatIdentity 写入或替换账号在该通讯工具上的连接；连接换了一个通讯账号时重记连接时间。
// 该通讯账号已连接别的 Cohive 账号时返回唯一约束错误。
func UpsertChatIdentity(ctx context.Context, q DBTX, c ChatIdentity) error {
	_, err := q.Exec(ctx, `INSERT INTO chat_identities (account_id, provider, tenant, user_id, handle, display_name)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (account_id, provider) DO UPDATE SET tenant = EXCLUDED.tenant, user_id = EXCLUDED.user_id,
			handle = EXCLUDED.handle, display_name = EXCLUDED.display_name,
			linked_at = CASE WHEN chat_identities.tenant = EXCLUDED.tenant AND chat_identities.user_id = EXCLUDED.user_id
				THEN chat_identities.linked_at ELSE now() END`,
		c.AccountID, c.Provider, c.Tenant, c.UserID, c.Handle, c.DisplayName)
	return err
}

// DeleteChatIdentity 断开账号在该通讯工具上的连接（没有连接时什么也不做）。
func DeleteChatIdentity(ctx context.Context, q DBTX, accountID int64, provider string) error {
	_, err := q.Exec(ctx, `DELETE FROM chat_identities WHERE account_id = $1 AND provider = $2`, accountID, provider)
	return err
}

// WorkspaceChat 是工作空间绑定的群。
type WorkspaceChat struct {
	WorkspaceID int64
	Provider    string
	Tenant      string
	ChatID      string // Telegram 群编号；钉钉 openConversationId；Slack 频道编号
	Title       string
	BoundBy     *int64
	CreatedAt   time.Time
}

const workspaceChatColumns = `workspace_id, provider, tenant, chat_id, title, bound_by, created_at`

func scanWorkspaceChat(row interface{ Scan(...any) error }) (WorkspaceChat, error) {
	var c WorkspaceChat
	err := row.Scan(&c.WorkspaceID, &c.Provider, &c.Tenant, &c.ChatID, &c.Title, &c.BoundBy, &c.CreatedAt)
	return c, notFound(err)
}

// WorkspaceChats 列出工作空间绑定的群（第一期最多一个）。
func WorkspaceChats(ctx context.Context, q DBTX, wsID int64) ([]WorkspaceChat, error) {
	rows, err := q.Query(ctx, `SELECT `+workspaceChatColumns+` FROM workspace_chats WHERE workspace_id = $1 ORDER BY id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkspaceChat
	for rows.Next() {
		c, err := scanWorkspaceChat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// WorkspaceChatOf 返回绑定了该群的工作空间群记录（一个群只绑一个工作空间）。
func WorkspaceChatOf(ctx context.Context, q DBTX, provider, tenant, chatID string) (WorkspaceChat, error) {
	return scanWorkspaceChat(q.QueryRow(ctx, `SELECT `+workspaceChatColumns+` FROM workspace_chats
		WHERE provider = $1 AND tenant = $2 AND chat_id = $3`, provider, tenant, chatID))
}

// SetWorkspaceChat 把工作空间在该通讯工具上的群设为 c（替换原有的）；群已绑别的工作空间时返回唯一约束错误。
func SetWorkspaceChat(ctx context.Context, q DBTX, c WorkspaceChat) error {
	_, err := q.Exec(ctx, `INSERT INTO workspace_chats (workspace_id, provider, tenant, chat_id, title, bound_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (workspace_id, provider) DO UPDATE SET tenant = EXCLUDED.tenant, chat_id = EXCLUDED.chat_id,
			title = EXCLUDED.title, bound_by = EXCLUDED.bound_by, created_at = now()`,
		c.WorkspaceID, c.Provider, c.Tenant, c.ChatID, c.Title, c.BoundBy)
	return err
}

// DeleteWorkspaceChats 解除工作空间绑定的群；provider 为空时解除全部。
func DeleteWorkspaceChats(ctx context.Context, q DBTX, wsID int64, provider string) error {
	_, err := q.Exec(ctx, `DELETE FROM workspace_chats WHERE workspace_id = $1 AND ($2 = '' OR provider = $2)`, wsID, provider)
	return err
}

// UpdateWorkspaceChatID 只改群编号（例如 Telegram 群升级为超级群后编号变化）。
func UpdateWorkspaceChatID(ctx context.Context, q DBTX, wsID int64, provider, chatID string) error {
	_, err := q.Exec(ctx, `UPDATE workspace_chats SET chat_id = $3 WHERE workspace_id = $1 AND provider = $2`, wsID, provider, chatID)
	return err
}

// 群绑定码状态
const (
	BindPending = "pending"
	BindBound   = "bound"
	BindFailed  = "failed"
)

// ChatBindRequest 是一次群绑定：工作空间管理员在网页生成绑定码，在群里 @机器人 发出后完成。
type ChatBindRequest struct {
	ID          int64
	WorkspaceID int64
	Provider    string
	CreatedBy   int64
	Code        string
	Status      string
	Detail      string
	ExpiresAt   time.Time
}

const chatBindColumns = `id, workspace_id, provider, created_by, code, status, detail, expires_at`

func scanChatBind(row interface{ Scan(...any) error }) (ChatBindRequest, error) {
	var r ChatBindRequest
	err := row.Scan(&r.ID, &r.WorkspaceID, &r.Provider, &r.CreatedBy, &r.Code, &r.Status, &r.Detail, &r.ExpiresAt)
	return r, notFound(err)
}

// CreateChatBindRequest 保存群绑定码。
func CreateChatBindRequest(ctx context.Context, q DBTX, r ChatBindRequest) (ChatBindRequest, error) {
	return scanChatBind(q.QueryRow(ctx, `INSERT INTO chat_bind_requests (workspace_id, provider, created_by, code, expires_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+chatBindColumns, r.WorkspaceID, r.Provider, r.CreatedBy, r.Code, r.ExpiresAt))
}

// ChatBindByCodeForUpdate 按绑定码读取并锁定群绑定请求。
func ChatBindByCodeForUpdate(ctx context.Context, q DBTX, provider, code string) (ChatBindRequest, error) {
	return scanChatBind(q.QueryRow(ctx, `SELECT `+chatBindColumns+` FROM chat_bind_requests
		WHERE provider = $1 AND code = $2 FOR UPDATE`, provider, code))
}

// GetChatBindRequest 读取某工作空间的群绑定请求。
func GetChatBindRequest(ctx context.Context, q DBTX, wsID, id int64) (ChatBindRequest, error) {
	return scanChatBind(q.QueryRow(ctx, `SELECT `+chatBindColumns+` FROM chat_bind_requests WHERE id = $1 AND workspace_id = $2`, id, wsID))
}

// FinishChatBindRequest 写回群绑定结果。
func FinishChatBindRequest(ctx context.Context, q DBTX, id int64, status, detail string) error {
	return execOne(ctx, q, `UPDATE chat_bind_requests SET status = $2, detail = $3 WHERE id = $1`, id, status, detail)
}
