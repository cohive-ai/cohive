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
	Remind      RemindConfig
}

// RemindConfig 是超时催办的时长（小时），0 表示关闭。
type RemindConfig struct {
	OpenHours   int // 待接单
	VerifyHours int // 待验证
}

// 催办时长的默认值与上限（小时）
const (
	DefaultRemindOpenHours   = 4
	DefaultRemindVerifyHours = 24
	MaxRemindHours           = 720
)

// Label 返回给人看的名字：显示名，没设时为路径。
func (w Workspace) Label() string {
	if w.DisplayName != "" {
		return w.DisplayName
	}
	return w.Name
}

// NotifyConfig 是工作空间的群通知配置。Channel 为空表示不发群通知。
// Channel 与 Target 取自工作空间群表里的第一个群（第一期一个工作空间只允许一个群）。
type NotifyConfig struct {
	Channel   string // 通讯工具：telegram、dingtalk
	Target    string // 群编号
	ShowTitle bool
	ShowBrief bool
}

const workspaceColumns = `w.id, w.name, w.display_name,
	coalesce((SELECT wc.provider FROM workspace_chats wc WHERE wc.workspace_id = w.id ORDER BY wc.id LIMIT 1), ''),
	coalesce((SELECT wc.chat_id FROM workspace_chats wc WHERE wc.workspace_id = w.id ORDER BY wc.id LIMIT 1), ''),
	w.notify_show_title, w.notify_show_brief, w.remind_open_hours, w.remind_verify_hours`

func scanWorkspace(row interface{ Scan(...any) error }) (Workspace, error) {
	var w Workspace
	err := row.Scan(&w.ID, &w.Name, &w.DisplayName, &w.Notify.Channel, &w.Notify.Target, &w.Notify.ShowTitle, &w.Notify.ShowBrief,
		&w.Remind.OpenHours, &w.Remind.VerifyHours)
	return w, notFound(err)
}

func CreateWorkspace(ctx context.Context, q DBTX, name string) (Workspace, error) {
	return scanWorkspace(q.QueryRow(ctx, `INSERT INTO workspaces AS w (name) VALUES ($1) RETURNING `+workspaceColumns, name))
}

func GetWorkspace(ctx context.Context, q DBTX, id int64) (Workspace, error) {
	return scanWorkspace(q.QueryRow(ctx, `SELECT `+workspaceColumns+` FROM workspaces w WHERE w.id = $1`, id))
}

func WorkspaceByName(ctx context.Context, q DBTX, name string) (Workspace, error) {
	return scanWorkspace(q.QueryRow(ctx, `SELECT `+workspaceColumns+` FROM workspaces w WHERE w.name = $1`, name))
}

func ListWorkspaces(ctx context.Context, q DBTX) ([]Workspace, error) {
	rows, err := q.Query(ctx, `SELECT `+workspaceColumns+` FROM workspaces w ORDER BY w.id`)
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

// SetWorkspaceNotify 保存群通知配置：显示开关写在工作空间上；Channel 为空时解除全部群，否则把该通讯工具的群设为 Target
// 并解除其他通讯工具的群（第一期一个工作空间只允许一个群）。
func SetWorkspaceNotify(ctx context.Context, q DBTX, wsID int64, cfg NotifyConfig) error {
	if err := execOne(ctx, q, `UPDATE workspaces SET notify_show_title = $2, notify_show_brief = $3 WHERE id = $1`,
		wsID, cfg.ShowTitle, cfg.ShowBrief); err != nil {
		return err
	}
	if cfg.Channel == "" || cfg.Target == "" {
		return DeleteWorkspaceChats(ctx, q, wsID, "")
	}
	if _, err := q.Exec(ctx, `DELETE FROM workspace_chats WHERE workspace_id = $1 AND provider <> $2`, wsID, cfg.Channel); err != nil {
		return err
	}
	cur, err := WorkspaceChats(ctx, q, wsID)
	if err != nil {
		return err
	}
	if len(cur) == 1 && cur[0].ChatID == cfg.Target {
		return nil
	}
	return SetWorkspaceChat(ctx, q, WorkspaceChat{WorkspaceID: wsID, Provider: cfg.Channel, ChatID: cfg.Target})
}

// SetWorkspaceRemind 保存超时催办时长。
func SetWorkspaceRemind(ctx context.Context, q DBTX, wsID int64, r RemindConfig) error {
	return execOne(ctx, q, `UPDATE workspaces SET remind_open_hours = $2, remind_verify_hours = $3 WHERE id = $1`,
		wsID, r.OpenHours, r.VerifyHours)
}

// SetWorkspaceNotifyTarget 只更新 Telegram 群编号（例如群升级为超级群后编号变化）。
func SetWorkspaceNotifyTarget(ctx context.Context, q DBTX, wsID int64, target string) error {
	return UpdateWorkspaceChatID(ctx, q, wsID, ProviderTelegram, target)
}

// SetMemberRole 设置成员角色。
func SetMemberRole(ctx context.Context, q DBTX, memberID int64, role string) error {
	return execOne(ctx, q, `UPDATE members SET role = $2 WHERE id = $1`, memberID, role)
}

// WorkspaceByNotifyTarget 返回绑定了该群的工作空间（一个群只绑一个工作空间）。
func WorkspaceByNotifyTarget(ctx context.Context, q DBTX, channel, target string) (Workspace, error) {
	return scanWorkspace(q.QueryRow(ctx, `SELECT `+workspaceColumns+` FROM workspaces w
		WHERE w.id = (SELECT workspace_id FROM workspace_chats WHERE provider = $1 AND tenant = '' AND chat_id = $2)`, channel, target))
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
