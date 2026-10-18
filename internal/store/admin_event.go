package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// AdminEvent 是一条管理操作记录。只追加：没有修改与删除的函数，库里有触发器拦截。
type AdminEvent struct {
	ID             int64
	At             time.Time
	ActorAccountID *int64 // 服务器命令行、系统为空
	ActorName      string
	Via            string
	WorkspaceID    *int64 // 服务器级操作为空；工作空间删除后仍保留编号
	WorkspaceName  string
	Action         string
	TargetKind     string
	TargetID       *int64
	TargetName     string
	Detail         map[string]any
}

// InsertAdminEvent 写一条管理操作记录。只接受事务：记录必须与业务操作一起提交或一起回滚。
func InsertAdminEvent(ctx context.Context, tx pgx.Tx, e AdminEvent) error {
	detail := e.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("admin event detail: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO admin_events (actor_account_id, actor_name, via, workspace_id, workspace_name,
		action, target_kind, target_id, target_name, detail) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		e.ActorAccountID, e.ActorName, e.Via, e.WorkspaceID, e.WorkspaceName, e.Action, e.TargetKind, e.TargetID,
		e.TargetName, raw)
	return err
}

// AdminEventFilter 是查看记录的条件；零值字段不参与筛选。
type AdminEventFilter struct {
	WorkspaceID    *int64
	WorkspaceName  string // 按名字快照筛选（已删除的工作空间）
	ActorAccountID *int64
	Actions        []string
	Via            string
	Before         int64 // 只取编号小于它的（游标翻页）
	Limit          int
}

// ListAdminEvents 按编号倒序列出记录。
func ListAdminEvents(ctx context.Context, q DBTX, f AdminEventFilter) ([]AdminEvent, error) {
	var where []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(cond, len(args)))
	}
	if f.WorkspaceID != nil {
		add("workspace_id = $%d", *f.WorkspaceID)
	}
	if f.WorkspaceName != "" {
		add("workspace_name = $%d", f.WorkspaceName)
	}
	if f.ActorAccountID != nil {
		add("actor_account_id = $%d", *f.ActorAccountID)
	}
	if len(f.Actions) > 0 {
		add("action = ANY($%d)", f.Actions)
	}
	if f.Via != "" {
		add("via = $%d", f.Via)
	}
	if f.Before > 0 {
		add("id < $%d", f.Before)
	}
	sql := `SELECT id, at, actor_account_id, actor_name, via, workspace_id, workspace_name, action, target_kind,
		target_id, target_name, detail FROM admin_events`
	if len(where) > 0 {
		sql += " WHERE " + strings.Join(where, " AND ")
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	args = append(args, limit)
	sql += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AdminEvent
	for rows.Next() {
		var e AdminEvent
		var raw []byte
		if err := rows.Scan(&e.ID, &e.At, &e.ActorAccountID, &e.ActorName, &e.Via, &e.WorkspaceID, &e.WorkspaceName,
			&e.Action, &e.TargetKind, &e.TargetID, &e.TargetName, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
