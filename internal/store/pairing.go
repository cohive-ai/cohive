package store

import (
	"context"
	"time"
)

// ---- 成员 ----

const memberColumns = `id, workspace_id, name, display_name, telegram_username, telegram_user_id, role`

func scanMember(row interface{ Scan(...any) error }) (Member, error) {
	var m Member
	err := row.Scan(&m.ID, &m.WorkspaceID, &m.Name, &m.DisplayName, &m.TelegramUsername, &m.TelegramUserID, &m.Role)
	return m, notFound(err)
}

// MemberByID 返回成员。
func MemberByID(ctx context.Context, q DBTX, id int64) (Member, error) {
	return scanMember(q.QueryRow(ctx, `SELECT `+memberColumns+` FROM members WHERE id = $1`, id))
}

// MemberByTelegramUserID 返回工作空间里连接到该 Telegram 账号的成员。
func MemberByTelegramUserID(ctx context.Context, q DBTX, wsID, tgUserID int64) (Member, error) {
	return scanMember(q.QueryRow(ctx, `SELECT `+memberColumns+` FROM members
		WHERE workspace_id = $1 AND telegram_user_id = $2`, wsID, tgUserID))
}

// HasAdmin 返回工作空间里是否有管理员。
func HasAdmin(ctx context.Context, q DBTX, wsID int64) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM members WHERE workspace_id = $1 AND role = 'admin')`, wsID).Scan(&ok)
	return ok, err
}

// ---- 设备 ----

// Device 是成员的一台电脑。
type Device struct {
	ID         int64
	MemberID   int64
	MemberName string // 只读，列表时关联得到
	Name       string
	CreatedAt  time.Time
	RevokedAt  *time.Time
	Tools      []string // 只读：仍有效的令牌对应的工具
}

// CreateDevice 新建设备。
func CreateDevice(ctx context.Context, q DBTX, memberID int64, name string) (Device, error) {
	d := Device{MemberID: memberID, Name: name}
	err := q.QueryRow(ctx, `INSERT INTO devices (member_id, name) VALUES ($1, $2) RETURNING id, created_at`,
		memberID, name).Scan(&d.ID, &d.CreatedAt)
	return d, err
}

// IssueDeviceToken 在设备上为成员的某个工具签发令牌；同一设备同一工具的旧令牌一并吊销。
func IssueDeviceToken(ctx context.Context, q DBTX, memberID, deviceID int64, tool string) (string, error) {
	if _, err := q.Exec(ctx, `UPDATE tokens SET revoked_at = now()
		WHERE device_id = $1 AND tool = $2 AND revoked_at IS NULL`, deviceID, tool); err != nil {
		return "", err
	}
	plain, id, err := IssueToken(ctx, q, memberID, tool)
	if err != nil {
		return "", err
	}
	_, err = q.Exec(ctx, `UPDATE tokens SET device_id = $2 WHERE id = $1`, id, deviceID)
	return plain, err
}

// ListDevices 列出工作空间的设备（含已吊销），可按成员过滤（memberID 为 0 时不过滤）。
func ListDevices(ctx context.Context, q DBTX, wsID, memberID int64) ([]Device, error) {
	rows, err := q.Query(ctx, `
		SELECT d.id, d.member_id, m.name, d.name, d.created_at, d.revoked_at,
			coalesce(array_agg(t.tool ORDER BY t.tool) FILTER (WHERE t.id IS NOT NULL), '{}')
		FROM devices d
		JOIN members m ON m.id = d.member_id
		LEFT JOIN tokens t ON t.device_id = d.id AND t.revoked_at IS NULL
		WHERE m.workspace_id = $1 AND ($2 = 0 OR d.member_id = $2)
		GROUP BY d.id, m.name ORDER BY d.id`, wsID, memberID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.MemberID, &d.MemberName, &d.Name, &d.CreatedAt, &d.RevokedAt, &d.Tools); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// RevokeDevice 吊销工作空间里的设备及其全部令牌；设备不存在或已吊销时返回 ErrNotFound。
func RevokeDevice(ctx context.Context, q DBTX, wsID, deviceID int64) error {
	tag, err := q.Exec(ctx, `UPDATE devices d SET revoked_at = now() FROM members m
		WHERE d.id = $2 AND m.id = d.member_id AND m.workspace_id = $1 AND d.revoked_at IS NULL`, wsID, deviceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	_, err = q.Exec(ctx, `UPDATE tokens SET revoked_at = now() WHERE device_id = $1 AND revoked_at IS NULL`, deviceID)
	return err
}

// ---- 邀请 ----

// 邀请类型
const (
	InvitePersonal = "personal" // 一次性，注册即生效
	InviteTeam     = "team"     // 可多人使用，注册后需管理员批准
)

// Invite 是一条邀请。
type Invite struct {
	ID          int64
	WorkspaceID int64
	Kind        string
	Role        string
	MaxUses     int
	Uses        int
	CreatedBy   *int64
	ExpiresAt   time.Time
	RevokedAt   *time.Time
	CreatedAt   time.Time
}

// Usable 判断邀请当前是否可用。
func (i Invite) Usable(now time.Time) bool {
	return i.RevokedAt == nil && i.Uses < i.MaxUses && now.Before(i.ExpiresAt)
}

const inviteColumns = `id, workspace_id, kind, role, max_uses, uses, created_by, expires_at, revoked_at, created_at`

func scanInvite(row interface{ Scan(...any) error }) (Invite, error) {
	var i Invite
	err := row.Scan(&i.ID, &i.WorkspaceID, &i.Kind, &i.Role, &i.MaxUses, &i.Uses, &i.CreatedBy, &i.ExpiresAt, &i.RevokedAt, &i.CreatedAt)
	return i, notFound(err)
}

// CreateInvite 保存邀请（只存邀请码的哈希）。
func CreateInvite(ctx context.Context, q DBTX, i Invite, codeHash []byte) (Invite, error) {
	return scanInvite(q.QueryRow(ctx, `INSERT INTO invites (workspace_id, code_hash, kind, role, max_uses, created_by, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+inviteColumns,
		i.WorkspaceID, codeHash, i.Kind, i.Role, i.MaxUses, i.CreatedBy, i.ExpiresAt))
}

// InviteByCode 按邀请码哈希读取邀请；forUpdate 为真时锁行。
func InviteByCode(ctx context.Context, q DBTX, codeHash []byte, forUpdate bool) (Invite, error) {
	sql := `SELECT ` + inviteColumns + ` FROM invites WHERE code_hash = $1`
	if forUpdate {
		sql += ` FOR UPDATE`
	}
	return scanInvite(q.QueryRow(ctx, sql, codeHash))
}

// UseInvite 把邀请的已用次数加一。
func UseInvite(ctx context.Context, q DBTX, id int64) error {
	_, err := q.Exec(ctx, `UPDATE invites SET uses = uses + 1 WHERE id = $1`, id)
	return err
}

// ListInvites 列出工作空间的邀请（最近 100 条）。
func ListInvites(ctx context.Context, q DBTX, wsID int64) ([]Invite, error) {
	rows, err := q.Query(ctx, `SELECT `+inviteColumns+` FROM invites WHERE workspace_id = $1 ORDER BY id DESC LIMIT 100`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		i, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// RevokeInvite 作废工作空间里的邀请；不存在或已作废时返回 ErrNotFound。
func RevokeInvite(ctx context.Context, q DBTX, wsID, id int64) error {
	tag, err := q.Exec(ctx, `UPDATE invites SET revoked_at = now() WHERE id = $2 AND workspace_id = $1 AND revoked_at IS NULL`, wsID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- 注册申请 ----

// 注册申请状态
const (
	SignupPending  = "pending"
	SignupApproved = "approved"
	SignupRejected = "rejected"
)

// Signup 是团队邀请的注册申请。
type Signup struct {
	ID           int64
	InviteID     int64
	WorkspaceID  int64
	Name         string
	DisplayName  string
	PasswordHash string
	Status       string
	MemberID     *int64
	CreatedAt    time.Time
}

const signupColumns = `id, invite_id, workspace_id, name, display_name, password_hash, status, member_id, created_at`

func scanSignup(row interface{ Scan(...any) error }) (Signup, error) {
	var s Signup
	err := row.Scan(&s.ID, &s.InviteID, &s.WorkspaceID, &s.Name, &s.DisplayName, &s.PasswordHash, &s.Status, &s.MemberID, &s.CreatedAt)
	return s, notFound(err)
}

// CreateSignup 保存注册申请。
func CreateSignup(ctx context.Context, q DBTX, s Signup) (Signup, error) {
	return scanSignup(q.QueryRow(ctx, `INSERT INTO signups (invite_id, workspace_id, name, display_name, password_hash)
		VALUES ($1, $2, $3, $4, $5) RETURNING `+signupColumns, s.InviteID, s.WorkspaceID, s.Name, s.DisplayName, s.PasswordHash))
}

// GetSignupForUpdate 在事务内读取并锁定注册申请。
func GetSignupForUpdate(ctx context.Context, q DBTX, id int64) (Signup, error) {
	return scanSignup(q.QueryRow(ctx, `SELECT `+signupColumns+` FROM signups WHERE id = $1 FOR UPDATE`, id))
}

// PendingSignupByName 返回工作空间里该成员名的待审申请。
func PendingSignupByName(ctx context.Context, q DBTX, wsID int64, name string) (Signup, error) {
	return scanSignup(q.QueryRow(ctx, `SELECT `+signupColumns+` FROM signups WHERE workspace_id = $1 AND name = $2 AND status = 'pending'`, wsID, name))
}

// PendingSignupsByName 返回所有工作空间里该成员名的待审申请（登录时提示“待审批”用）。
func PendingSignupsByName(ctx context.Context, q DBTX, name string) ([]Signup, error) {
	return listSignups(ctx, q, `SELECT `+signupColumns+` FROM signups WHERE name = $1 AND status = 'pending'`, name)
}

// ListPendingSignups 列出工作空间的待审注册申请。
func ListPendingSignups(ctx context.Context, q DBTX, wsID int64) ([]Signup, error) {
	return listSignups(ctx, q, `SELECT `+signupColumns+` FROM signups WHERE workspace_id = $1 AND status = 'pending' ORDER BY id`, wsID)
}

func listSignups(ctx context.Context, q DBTX, sql string, arg any) ([]Signup, error) {
	rows, err := q.Query(ctx, sql, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Signup
	for rows.Next() {
		s, err := scanSignup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DecideSignup 写回审批结果。
func DecideSignup(ctx context.Context, q DBTX, id int64, status string, memberID, decidedBy *int64) error {
	_, err := q.Exec(ctx, `UPDATE signups SET status = $2, member_id = $3, decided_by = $4, decided_at = now() WHERE id = $1`,
		id, status, memberID, decidedBy)
	return err
}

// ---- 设备授权 ----

// 设备授权状态
const (
	AuthPending       = "pending"        // 等待成员在浏览器里授权
	AuthAwaitingAdmin = "awaiting_admin" // 非首台设备，等待管理员批准
	AuthApproved      = "approved"       // 已批准，等待终端领取令牌
	AuthDelivered     = "delivered"      // 令牌已交付
	AuthDenied        = "denied"
	AuthExpired       = "expired"
)

// DeviceAuth 是一次设备授权。
type DeviceAuth struct {
	ID             int64
	Nonce          string
	PollSecretHash []byte
	Tools          []string
	DeviceName     string
	Status         string
	MemberID       *int64
	MemberName     string // 只读，关联得到
	WorkspaceID    *int64
	DeviceID       *int64
	DecidedBy      *int64
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

const authColumns = `a.id, a.nonce, a.poll_secret_hash, a.tools, a.device_name, a.status, a.member_id, coalesce(m.name, ''),
	a.workspace_id, a.device_id, a.decided_by, a.created_at, a.expires_at`

const authFrom = ` FROM device_auths a LEFT JOIN members m ON m.id = a.member_id `

func scanAuth(row interface{ Scan(...any) error }) (DeviceAuth, error) {
	var a DeviceAuth
	err := row.Scan(&a.ID, &a.Nonce, &a.PollSecretHash, &a.Tools, &a.DeviceName, &a.Status, &a.MemberID, &a.MemberName,
		&a.WorkspaceID, &a.DeviceID, &a.DecidedBy, &a.CreatedAt, &a.ExpiresAt)
	return a, notFound(err)
}

// CreateDeviceAuth 保存设备授权请求。
func CreateDeviceAuth(ctx context.Context, q DBTX, a DeviceAuth) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `INSERT INTO device_auths (nonce, poll_secret_hash, tools, device_name, expires_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, a.Nonce, a.PollSecretHash, a.Tools, a.DeviceName, a.ExpiresAt).Scan(&id)
	return id, err
}

// GetDeviceAuth 按编号读取（forUpdate 为真时锁行）。
func GetDeviceAuth(ctx context.Context, q DBTX, id int64, forUpdate bool) (DeviceAuth, error) {
	sql := `SELECT ` + authColumns + authFrom + `WHERE a.id = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF a`
	}
	return scanAuth(q.QueryRow(ctx, sql, id))
}

// DeviceAuthByNonce 按授权页参数读取（forUpdate 为真时锁行）。
func DeviceAuthByNonce(ctx context.Context, q DBTX, nonce string, forUpdate bool) (DeviceAuth, error) {
	sql := `SELECT ` + authColumns + authFrom + `WHERE a.nonce = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF a`
	}
	return scanAuth(q.QueryRow(ctx, sql, nonce))
}

// UpdateDeviceAuth 写回状态、成员、工作空间、设备与审批人。
func UpdateDeviceAuth(ctx context.Context, q DBTX, a DeviceAuth) error {
	_, err := q.Exec(ctx, `UPDATE device_auths SET status = $2, member_id = $3, workspace_id = $4, device_id = $5,
		decided_by = $6, expires_at = $7 WHERE id = $1`, a.ID, a.Status, a.MemberID, a.WorkspaceID, a.DeviceID, a.DecidedBy, a.ExpiresAt)
	return err
}

// ListAwaitingDeviceAuths 列出工作空间里等待管理员批准的设备。
func ListAwaitingDeviceAuths(ctx context.Context, q DBTX, wsID int64) ([]DeviceAuth, error) {
	rows, err := q.Query(ctx, `SELECT `+authColumns+authFrom+`WHERE a.workspace_id = $1 AND a.status = 'awaiting_admin'
		AND a.expires_at > now() ORDER BY a.id`, wsID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceAuth
	for rows.Next() {
		a, err := scanAuth(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// CountEverDevices 返回成员有过的设备数（含已吊销）。首台设备免审只看“从未有过设备”，
// 吊销全部设备后再配对不算首台。
func CountEverDevices(ctx context.Context, q DBTX, memberID int64) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM devices WHERE member_id = $1`, memberID).Scan(&n)
	return n, err
}

// LockMember 在事务内锁住成员行，让同一成员的并发操作（例如两次设备授权）排队执行。
func LockMember(ctx context.Context, q DBTX, memberID int64) error {
	var id int64
	return notFound(q.QueryRow(ctx, `SELECT id FROM members WHERE id = $1 FOR UPDATE`, memberID).Scan(&id))
}

// ---- 通讯工具连接 ----

// 连接状态
const (
	LinkPending = "pending"
	LinkLinked  = "linked"
	LinkFailed  = "failed"
	LinkExpired = "expired" // 只用于展示：过期的请求在库里仍是 pending
)

// LinkRequest 是一次通讯工具连接请求。
type LinkRequest struct {
	ID        int64
	MemberID  int64
	Provider  string
	Nonce     string
	Status    string
	Detail    string
	ExpiresAt time.Time
}

const linkColumns = `id, member_id, provider, nonce, status, detail, expires_at`

func scanLink(row interface{ Scan(...any) error }) (LinkRequest, error) {
	var l LinkRequest
	err := row.Scan(&l.ID, &l.MemberID, &l.Provider, &l.Nonce, &l.Status, &l.Detail, &l.ExpiresAt)
	return l, notFound(err)
}

// CreateLinkRequest 保存连接请求。
func CreateLinkRequest(ctx context.Context, q DBTX, l LinkRequest) (LinkRequest, error) {
	return scanLink(q.QueryRow(ctx, `INSERT INTO link_requests (member_id, provider, nonce, expires_at)
		VALUES ($1, $2, $3, $4) RETURNING `+linkColumns, l.MemberID, l.Provider, l.Nonce, l.ExpiresAt))
}

// LinkRequestByNonceForUpdate 按链接参数读取并锁定连接请求。
func LinkRequestByNonceForUpdate(ctx context.Context, q DBTX, provider, nonce string) (LinkRequest, error) {
	return scanLink(q.QueryRow(ctx, `SELECT `+linkColumns+` FROM link_requests
		WHERE provider = $1 AND nonce = $2 FOR UPDATE`, provider, nonce))
}

// GetLinkRequest 读取成员自己的连接请求。
func GetLinkRequest(ctx context.Context, q DBTX, memberID, id int64) (LinkRequest, error) {
	return scanLink(q.QueryRow(ctx, `SELECT `+linkColumns+` FROM link_requests WHERE id = $1 AND member_id = $2`, id, memberID))
}

// FinishLinkRequest 写回连接结果。
func FinishLinkRequest(ctx context.Context, q DBTX, id int64, status, detail string) error {
	_, err := q.Exec(ctx, `UPDATE link_requests SET status = $2, detail = $3 WHERE id = $1`, id, status, detail)
	return err
}
