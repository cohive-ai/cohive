package store

import (
	"context"
)

// Workspace 是一个业务组：独立的成员、服务、问题单与群通知配置。
type Workspace struct {
	ID        int64
	Name      string
	Notify    NotifyConfig
	PublicURL string // 看板的外部地址，用于群消息里的链接；可为空
	// JoinRequiresApproval 为 true 时，群里 /join 需要管理员批准
	JoinRequiresApproval bool
}

// NotifyConfig 是工作空间的群通知配置。Channel 为空表示不发群通知。
type NotifyConfig struct {
	Channel   string // "telegram"
	Target    string // 群聊编号
	ShowTitle bool
	ShowBrief bool
}

const workspaceColumns = `id, name, notify_channel, notify_target, notify_show_title, notify_show_brief, public_url, join_requires_approval`

func scanWorkspace(row interface{ Scan(...any) error }) (Workspace, error) {
	var w Workspace
	err := row.Scan(&w.ID, &w.Name, &w.Notify.Channel, &w.Notify.Target, &w.Notify.ShowTitle, &w.Notify.ShowBrief, &w.PublicURL, &w.JoinRequiresApproval)
	return w, notFound(err)
}

func CreateWorkspace(ctx context.Context, q DBTX, name string) (Workspace, error) {
	return scanWorkspace(q.QueryRow(ctx, `INSERT INTO workspaces (name) VALUES ($1) RETURNING `+workspaceColumns, name))
}

func GetWorkspace(ctx context.Context, q DBTX, id int64) (Workspace, error) {
	return scanWorkspace(q.QueryRow(ctx, `SELECT `+workspaceColumns+` FROM workspaces WHERE id = $1`, id))
}

func WorkspaceByName(ctx context.Context, q DBTX, name string) (Workspace, error) {
	return scanWorkspace(q.QueryRow(ctx, `SELECT `+workspaceColumns+` FROM workspaces WHERE name = $1`, name))
}

func ListWorkspaces(ctx context.Context, q DBTX) ([]Workspace, error) {
	rows, err := q.Query(ctx, `SELECT `+workspaceColumns+` FROM workspaces ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workspace
	for rows.Next() {
		w, err := scanWorkspace(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

func SetWorkspaceNotify(ctx context.Context, q DBTX, wsID int64, cfg NotifyConfig, publicURL string) error {
	tag, err := q.Exec(ctx, `UPDATE workspaces SET notify_channel = $2, notify_target = $3,
		notify_show_title = $4, notify_show_brief = $5, public_url = $6 WHERE id = $1`,
		wsID, cfg.Channel, cfg.Target, cfg.ShowTitle, cfg.ShowBrief, publicURL)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func SetMemberTelegram(ctx context.Context, q DBTX, memberID int64, username string) error {
	return updateMember(ctx, q, `UPDATE members SET telegram_username = $2 WHERE id = $1`, memberID, username)
}

// SetMemberTelegramUserID 绑定或解绑（nil）成员的 Telegram 用户编号。
func SetMemberTelegramUserID(ctx context.Context, q DBTX, memberID int64, userID *int64) error {
	return updateMember(ctx, q, `UPDATE members SET telegram_user_id = $2 WHERE id = $1`, memberID, userID)
}

func updateMember(ctx context.Context, q DBTX, sql string, memberID int64, value any) error {
	tag, err := q.Exec(ctx, sql, memberID, value)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetWorkspaceNotifyTarget 只更新群编号（例如群升级为超级群后编号变化）。
func SetWorkspaceNotifyTarget(ctx context.Context, q DBTX, wsID int64, target string) error {
	_, err := q.Exec(ctx, `UPDATE workspaces SET notify_target = $2 WHERE id = $1`, wsID, target)
	return err
}

// SetJoinApproval 设置加入工作空间是否需要管理员批准。
func SetJoinApproval(ctx context.Context, q DBTX, wsID int64, required bool) error {
	_, err := q.Exec(ctx, `UPDATE workspaces SET join_requires_approval = $2 WHERE id = $1`, wsID, required)
	return err
}

// SetMemberRole 设置成员角色。
func SetMemberRole(ctx context.Context, q DBTX, memberID int64, role string) error {
	return updateMember(ctx, q, `UPDATE members SET role = $2 WHERE id = $1`, memberID, role)
}

// WorkspaceByTelegramChat 按群聊编号找工作空间（群 /join 时用）。
func WorkspaceByTelegramChat(ctx context.Context, q DBTX, chatID string) (Workspace, error) {
	return scanWorkspace(q.QueryRow(ctx, `SELECT `+workspaceColumns+` FROM workspaces
		WHERE notify_channel = 'telegram' AND notify_target = $1 ORDER BY id LIMIT 1`, chatID))
}
