package store

import (
	"context"
	"time"
)

// 设备授权登录的状态。
const (
	DevicePending   = "pending"   // 等待在机器人私聊里确认
	DeviceApproved  = "approved"  // 已确认，等待终端领取令牌
	DeviceDelivered = "delivered" // 令牌已交付
	DeviceDenied    = "denied"    // 被取消，或链接被第二个账号打开
	DeviceExpired   = "expired"
)

// DeviceLogin 是一次设备授权登录请求。
type DeviceLogin struct {
	ID             int64
	PollSecretHash []byte
	StartNonce     string
	Tools          []string
	DeviceName     string
	WorkspaceName  string
	TelegramUserID *int64
	MemberID       *int64
	Status         string
	ExpiresAt      time.Time
	CreatedAt      time.Time
}

const deviceColumns = `id, poll_secret_hash, start_nonce, tools, device_name, workspace_name,
	telegram_user_id, member_id, status, expires_at, created_at`

func scanDevice(row interface{ Scan(...any) error }) (DeviceLogin, error) {
	var d DeviceLogin
	err := row.Scan(&d.ID, &d.PollSecretHash, &d.StartNonce, &d.Tools, &d.DeviceName, &d.WorkspaceName,
		&d.TelegramUserID, &d.MemberID, &d.Status, &d.ExpiresAt, &d.CreatedAt)
	return d, notFound(err)
}

// CreateDeviceLogin 保存一次设备授权登录请求。
func CreateDeviceLogin(ctx context.Context, q DBTX, d DeviceLogin) (DeviceLogin, error) {
	return scanDevice(q.QueryRow(ctx, `INSERT INTO device_logins
		(poll_secret_hash, start_nonce, tools, device_name, workspace_name, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING `+deviceColumns,
		d.PollSecretHash, d.StartNonce, d.Tools, d.DeviceName, d.WorkspaceName, d.ExpiresAt))
}

// DeviceLoginByNonceForUpdate 在事务内按深链参数读取并锁定请求。
func DeviceLoginByNonceForUpdate(ctx context.Context, q DBTX, nonce string) (DeviceLogin, error) {
	return scanDevice(q.QueryRow(ctx, `SELECT `+deviceColumns+` FROM device_logins WHERE start_nonce = $1 FOR UPDATE`, nonce))
}

// DeviceLoginForUpdate 在事务内按编号读取并锁定请求。
func DeviceLoginForUpdate(ctx context.Context, q DBTX, id int64) (DeviceLogin, error) {
	return scanDevice(q.QueryRow(ctx, `SELECT `+deviceColumns+` FROM device_logins WHERE id = $1 FOR UPDATE`, id))
}

// GetDeviceLogin 按编号读取请求（不加锁）。
func GetDeviceLogin(ctx context.Context, q DBTX, id int64) (DeviceLogin, error) {
	return scanDevice(q.QueryRow(ctx, `SELECT `+deviceColumns+` FROM device_logins WHERE id = $1`, id))
}

// UpdateDeviceLogin 写回请求的可变字段（打开人、工作空间、成员与状态）。
func UpdateDeviceLogin(ctx context.Context, q DBTX, d DeviceLogin) error {
	_, err := q.Exec(ctx, `UPDATE device_logins SET telegram_user_id = $2, workspace_name = $3, member_id = $4, status = $5
		WHERE id = $1`, d.ID, d.TelegramUserID, d.WorkspaceName, d.MemberID, d.Status)
	return err
}
