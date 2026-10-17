package store

import (
	"context"
	"time"
)

// Workspace 是一个业务组：独立的成员、服务、问题单与群通知配置。
type Workspace struct {
	ID          int64
	Name        string // 路径：出现在链接与项目配置里，不可改
	DisplayName string // 显示名：给人看，可随时改；空表示与路径相同
	Notify      NotifyConfig
}

// Label 返回给人看的名字：显示名，没设时为路径。
func (w Workspace) Label() string {
	if w.DisplayName != "" {
		return w.DisplayName
	}
	return w.Name
}

// NotifyConfig 是工作空间的群通知配置。Channel 为空表示不发群通知。
type NotifyConfig struct {
	Channel   string // "telegram"
	Target    string // 群聊编号
	ShowTitle bool
	ShowBrief bool
}

const workspaceColumns = `id, name, display_name, notify_channel, notify_target, notify_show_title, notify_show_brief`

func scanWorkspace(row interface{ Scan(...any) error }) (Workspace, error) {
	var w Workspace
	err := row.Scan(&w.ID, &w.Name, &w.DisplayName, &w.Notify.Channel, &w.Notify.Target, &w.Notify.ShowTitle, &w.Notify.ShowBrief)
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

func SetWorkspaceNotify(ctx context.Context, q DBTX, wsID int64, cfg NotifyConfig) error {
	tag, err := q.Exec(ctx, `UPDATE workspaces SET notify_channel = $2, notify_target = $3,
		notify_show_title = $4, notify_show_brief = $5 WHERE id = $1`,
		wsID, cfg.Channel, cfg.Target, cfg.ShowTitle, cfg.ShowBrief)
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

// SetMemberRole 设置成员角色。
func SetMemberRole(ctx context.Context, q DBTX, memberID int64, role string) error {
	return execOne(ctx, q, `UPDATE members SET role = $2 WHERE id = $1`, memberID, role)
}

// WorkspaceByNotifyTarget 返回绑定了该群的工作空间（一个群只绑一个工作空间）。
func WorkspaceByNotifyTarget(ctx context.Context, q DBTX, channel, target string) (Workspace, error) {
	return scanWorkspace(q.QueryRow(ctx, `SELECT `+workspaceColumns+` FROM workspaces
		WHERE notify_channel = $1 AND notify_target = $2 AND notify_target <> ''`, channel, target))
}

// SetWorkspaceCreator 记下建工作空间的账号。
func SetWorkspaceCreator(ctx context.Context, q DBTX, wsID, accountID int64) error {
	return execOne(ctx, q, `UPDATE workspaces SET created_by = $2 WHERE id = $1`, wsID, accountID)
}

// SetWorkspaceDisplayName 修改显示名（空表示与路径相同）。
func SetWorkspaceDisplayName(ctx context.Context, q DBTX, wsID int64, display string) error {
	return execOne(ctx, q, `UPDATE workspaces SET display_name = $2 WHERE id = $1`, wsID, display)
}

// ServerSettings 是服务器级设置（只有一行）。
type ServerSettings struct {
	ExternalURL string // 对外地址；空表示没设
	UpdatedAt   time.Time
	UpdatedBy   *int64
}

// GetServerSettings 读服务器级设置；forUpdate 为真时锁行。
func GetServerSettings(ctx context.Context, q DBTX, forUpdate bool) (ServerSettings, error) {
	sql := `SELECT external_url, updated_at, updated_by FROM server_settings`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	var st ServerSettings
	err := q.QueryRow(ctx, sql).Scan(&st.ExternalURL, &st.UpdatedAt, &st.UpdatedBy)
	return st, notFound(err)
}

// SetExternalURL 写对外地址。
func SetExternalURL(ctx context.Context, q DBTX, url string, by *int64) error {
	return execOne(ctx, q, `UPDATE server_settings SET external_url = $1, updated_at = now(), updated_by = $2`, url, by)
}
