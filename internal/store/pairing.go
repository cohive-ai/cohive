package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
)

// ---- 设备 ----

// Device 是账号的一台电脑；设备属于人，可用于他所在的全部工作空间。
type Device struct {
	ID          int64
	AccountID   int64
	AccountName string // 只读，列表时关联得到
	Name        string
	CreatedAt   time.Time
	RevokedAt   *time.Time
	LastUsedAt  *time.Time
	HasSecret   bool          // 是否已领取设备密钥（M5 配对的旧设备、已批准未领取的设备没有）
	Tokens      []DeviceToken // 只读：仍有效的令牌
}

// DeviceToken 是设备上一枚有效令牌所属的工作空间与工具。
type DeviceToken struct {
	Workspace string
	Tool      string
}

// CreateDevice 新建设备（批准设备授权时调用；设备密钥领取时才生成）。
func CreateDevice(ctx context.Context, q DBTX, accountID int64, name string) (Device, error) {
	d := Device{AccountID: accountID, Name: name}
	err := q.QueryRow(ctx, `INSERT INTO devices (account_id, name) VALUES ($1, $2) RETURNING id, created_at`,
		accountID, name).Scan(&d.ID, &d.CreatedAt)
	return d, err
}

// SetDeviceSecret 保存设备密钥的哈希。
func SetDeviceSecret(ctx context.Context, q DBTX, deviceID int64, secretHash []byte) error {
	return execOne(ctx, q, `UPDATE devices SET secret_hash = $2 WHERE id = $1`, deviceID, secretHash)
}

// DeviceBySecret 按设备密钥找到未吊销、账号未停用的设备，并记下使用时间；找不到时返回 ErrUnauthorized。
func DeviceBySecret(ctx context.Context, q DBTX, secretHash []byte) (Device, Account, error) {
	var d Device
	var a Account
	err := q.QueryRow(ctx, `UPDATE devices d SET last_used_at = now() FROM accounts a
		WHERE d.secret_hash = $1 AND d.revoked_at IS NULL AND a.id = d.account_id AND a.disabled_at IS NULL
		RETURNING d.id, d.account_id, d.name, d.created_at, `+accountColumns, secretHash).
		Scan(&d.ID, &d.AccountID, &d.Name, &d.CreatedAt,
			&a.ID, &a.Name, &a.DisplayName, &a.TelegramUsername, &a.TelegramUserID, &a.ServerAdmin, &a.Language, &a.DisabledAt, &a.CreatedAt)
	if errors.Is(notFound(err), ErrNotFound) {
		return Device{}, Account{}, ErrUnauthorized
	}
	d.AccountName, d.HasSecret = a.Name, true
	return d, a, err
}

// IssueDeviceToken 在设备上为成员资格的某个工具签发令牌；同一设备、同一成员资格、同一工具的旧令牌一并吊销。
func IssueDeviceToken(ctx context.Context, q DBTX, memberID, deviceID int64, tool string) (string, error) {
	if _, err := q.Exec(ctx, `UPDATE tokens SET revoked_at = now()
		WHERE device_id = $1 AND member_id = $2 AND tool = $3 AND revoked_at IS NULL`, deviceID, memberID, tool); err != nil {
		return "", err
	}
	plain, id, err := IssueToken(ctx, q, memberID, tool)
	if err != nil {
		return "", err
	}
	_, err = q.Exec(ctx, `UPDATE tokens SET device_id = $2 WHERE id = $1`, id, deviceID)
	return plain, err
}

// ErrDeviceRevoked 表示设备已被吊销。
var ErrDeviceRevoked = i18n.Errorf("store.err.device_revoked")

// LockActiveDevice 在事务内锁住设备行并确认未吊销；签发令牌前调用，和吊销互斥。
func LockActiveDevice(ctx context.Context, q DBTX, deviceID int64) error {
	var revoked *time.Time
	if err := q.QueryRow(ctx, `SELECT revoked_at FROM devices WHERE id = $1 FOR UPDATE`, deviceID).Scan(&revoked); err != nil {
		return notFound(err)
	}
	if revoked != nil {
		return ErrDeviceRevoked
	}
	return nil
}

// GetDevice 返回设备（不含令牌列表）。
func GetDevice(ctx context.Context, q DBTX, id int64) (Device, error) {
	var d Device
	err := q.QueryRow(ctx, `SELECT d.id, d.account_id, a.name, d.name, d.created_at, d.revoked_at, d.last_used_at,
		d.secret_hash IS NOT NULL FROM devices d JOIN accounts a ON a.id = d.account_id WHERE d.id = $1`, id).
		Scan(&d.ID, &d.AccountID, &d.AccountName, &d.Name, &d.CreatedAt, &d.RevokedAt, &d.LastUsedAt, &d.HasSecret)
	return d, notFound(err)
}

// RevokeDeviceByID 吊销设备及其令牌；已吊销时什么都不做。
func RevokeDeviceByID(ctx context.Context, q DBTX, deviceID int64) error {
	if _, err := q.Exec(ctx, `UPDATE devices SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, deviceID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `UPDATE tokens SET revoked_at = now() WHERE device_id = $1 AND revoked_at IS NULL`, deviceID)
	return err
}

// CountActiveDevices 返回账号未吊销的设备数。
func CountActiveDevices(ctx context.Context, q DBTX, accountID int64) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM devices WHERE account_id = $1 AND revoked_at IS NULL`, accountID).Scan(&n)
	return n, err
}

// RevokeAccountDevices 吊销账号的全部设备与全部令牌（停用时）。
func RevokeAccountDevices(ctx context.Context, q DBTX, accountID int64) error {
	if _, err := q.Exec(ctx, `UPDATE devices SET revoked_at = now() WHERE account_id = $1 AND revoked_at IS NULL`, accountID); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `UPDATE tokens t SET revoked_at = now() FROM members m
		WHERE t.member_id = m.id AND m.account_id = $1 AND t.revoked_at IS NULL`, accountID)
	return err
}

const deviceListSQL = `
	SELECT d.id, d.account_id, a.name, d.name, d.created_at, d.revoked_at, d.last_used_at, d.secret_hash IS NOT NULL,
		coalesce(array_agg(w.name || '/' || t.tool ORDER BY w.name, t.tool) FILTER (WHERE t.id IS NOT NULL), '{}')
	FROM devices d
	JOIN accounts a ON a.id = d.account_id
	LEFT JOIN tokens t ON t.device_id = d.id AND t.revoked_at IS NULL
	LEFT JOIN members tm ON tm.id = t.member_id
	LEFT JOIN workspaces w ON w.id = tm.workspace_id`

func collectDevices(ctx context.Context, q DBTX, where string, args ...any) ([]Device, error) {
	rows, err := q.Query(ctx, deviceListSQL+` WHERE `+where+` GROUP BY d.id, a.name ORDER BY d.id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		var toks []string
		if err := rows.Scan(&d.ID, &d.AccountID, &d.AccountName, &d.Name, &d.CreatedAt, &d.RevokedAt, &d.LastUsedAt,
			&d.HasSecret, &toks); err != nil {
			return nil, err
		}
		for _, t := range toks {
			ws, tool, _ := strings.Cut(t, "/")
			d.Tokens = append(d.Tokens, DeviceToken{Workspace: ws, Tool: tool})
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListAccountDevices 列出账号的设备（含已吊销）。
func ListAccountDevices(ctx context.Context, q DBTX, accountID int64) ([]Device, error) {
	return collectDevices(ctx, q, `d.account_id = $1`, accountID)
}

// ListWorkspaceDevices 列出工作空间在册成员的设备（含已吊销）。
func ListWorkspaceDevices(ctx context.Context, q DBTX, wsID int64) ([]Device, error) {
	return collectDevices(ctx, q, `d.account_id IN (SELECT account_id FROM members WHERE workspace_id = $1 AND status = 'active')`, wsID)
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
	CreatedBy   *int64 // 账号
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
func RevokeInvite(ctx context.Context, q DBTX, wsID, id int64) (kind string, err error) {
	err = q.QueryRow(ctx, `UPDATE invites SET revoked_at = now() WHERE id = $2 AND workspace_id = $1 AND revoked_at IS NULL
		RETURNING kind`, wsID, id).Scan(&kind)
	return kind, notFound(err)
}

// ---- 注册申请 ----

// 注册申请状态
const (
	SignupPending  = "pending"
	SignupApproved = "approved"
	SignupRejected = "rejected"
)

// Signup 是团队邀请的一条待审申请：新人的“注册申请”（有登录名与密码哈希），
// 或已有账号的“加入申请”（AccountID 非空，Name 等为该账号的）。
type Signup struct {
	ID           int64
	InviteID     int64
	WorkspaceID  int64
	AccountID    *int64 // 加入申请时为申请人；注册申请获批后回填
	Existing     bool   // 是否为已有账号的加入申请
	Name         string
	DisplayName  string
	PasswordHash string
	Language     string // 注册时的界面语言，获批建账号时写到账号上
	Status       string
	MemberID     *int64
	CreatedAt    time.Time
}

// 加入申请时名字取账号的：申请行上的 name 为空
const signupColumns = `s.id, s.invite_id, s.workspace_id, s.account_id, s.name IS NULL,
	coalesce(s.name, a.name, ''), CASE WHEN s.name IS NULL THEN coalesce(a.display_name, '') ELSE s.display_name END,
	coalesce(s.password_hash, ''), coalesce(s.language, ''),
	s.status, s.member_id, s.created_at`

const signupFrom = ` FROM signups s LEFT JOIN accounts a ON a.id = s.account_id `

func scanSignup(row interface{ Scan(...any) error }) (Signup, error) {
	var s Signup
	err := row.Scan(&s.ID, &s.InviteID, &s.WorkspaceID, &s.AccountID, &s.Existing, &s.Name, &s.DisplayName, &s.PasswordHash,
		&s.Language, &s.Status, &s.MemberID, &s.CreatedAt)
	return s, notFound(err)
}

// CreateSignup 保存新人的注册申请。
func CreateSignup(ctx context.Context, q DBTX, s Signup) (Signup, error) {
	var id int64
	if err := q.QueryRow(ctx, `INSERT INTO signups (invite_id, workspace_id, name, display_name, password_hash, language)
		VALUES ($1, $2, $3, $4, $5, nullif($6, '')) RETURNING id`, s.InviteID, s.WorkspaceID, s.Name, s.DisplayName, s.PasswordHash,
		s.Language).Scan(&id); err != nil {
		return Signup{}, err
	}
	return GetSignup(ctx, q, id)
}

// CreateJoinRequest 保存已有账号的加入申请。
func CreateJoinRequest(ctx context.Context, q DBTX, inviteID, wsID, accountID int64) (Signup, error) {
	var id int64
	if err := q.QueryRow(ctx, `INSERT INTO signups (invite_id, workspace_id, account_id)
		VALUES ($1, $2, $3) RETURNING id`, inviteID, wsID, accountID).Scan(&id); err != nil {
		return Signup{}, err
	}
	return GetSignup(ctx, q, id)
}

// GetSignup 读取申请。
func GetSignup(ctx context.Context, q DBTX, id int64) (Signup, error) {
	return scanSignup(q.QueryRow(ctx, `SELECT `+signupColumns+signupFrom+`WHERE s.id = $1`, id))
}

// GetSignupForUpdate 在事务内读取并锁定申请。
func GetSignupForUpdate(ctx context.Context, q DBTX, id int64) (Signup, error) {
	return scanSignup(q.QueryRow(ctx, `SELECT `+signupColumns+signupFrom+`WHERE s.id = $1 FOR UPDATE OF s`, id))
}

// PendingSignupByName 返回该登录名的新人待审申请（登录名全局占位）。
func PendingSignupByName(ctx context.Context, q DBTX, name string) (Signup, error) {
	return scanSignup(q.QueryRow(ctx, `SELECT `+signupColumns+signupFrom+`
		WHERE s.name = $1 AND s.status = 'pending' AND s.account_id IS NULL`, name))
}

// PendingJoinRequest 返回账号在工作空间里待审的加入申请。
func PendingJoinRequest(ctx context.Context, q DBTX, wsID, accountID int64) (Signup, error) {
	return scanSignup(q.QueryRow(ctx, `SELECT `+signupColumns+signupFrom+`
		WHERE s.workspace_id = $1 AND s.account_id = $2 AND s.status = 'pending'`, wsID, accountID))
}

// ListPendingSignups 列出工作空间的待审申请（注册与加入）。
func ListPendingSignups(ctx context.Context, q DBTX, wsID int64) ([]Signup, error) {
	return listSignups(ctx, q, `SELECT `+signupColumns+signupFrom+`WHERE s.workspace_id = $1 AND s.status = 'pending' ORDER BY s.id`, wsID)
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

// DecideSignup 写回审批结果；accountID 为获批后的账号（拒绝时为空，加入申请时保持原值）。
func DecideSignup(ctx context.Context, q DBTX, id int64, status string, accountID, memberID, decidedBy *int64) error {
	// 已决的申请清掉密码哈希：拒绝的没有用处，批准的已复制进账号表
	_, err := q.Exec(ctx, `UPDATE signups SET status = $2, account_id = coalesce($3, account_id), member_id = $4,
		decided_by = $5, decided_at = now(), password_hash = NULL WHERE id = $1`, id, status, accountID, memberID, decidedBy)
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
	AccountID      *int64 // 授权的账号（浏览器里点授权后才有）
	AccountName    string // 只读，关联得到
	DisplayName    string // 只读，关联得到
	DeviceID       *int64 // 批准时建的设备
	DecidedBy      *int64 // 批准的账号
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

const authColumns = `x.id, x.nonce, x.poll_secret_hash, x.tools, x.device_name, x.status, x.account_id,
	coalesce(acc.name, ''), coalesce(acc.display_name, ''), x.device_id, x.decided_by, x.created_at, x.expires_at`

const authFrom = ` FROM device_auths x LEFT JOIN accounts acc ON acc.id = x.account_id `

func scanAuth(row interface{ Scan(...any) error }) (DeviceAuth, error) {
	var a DeviceAuth
	err := row.Scan(&a.ID, &a.Nonce, &a.PollSecretHash, &a.Tools, &a.DeviceName, &a.Status, &a.AccountID, &a.AccountName,
		&a.DisplayName, &a.DeviceID, &a.DecidedBy, &a.CreatedAt, &a.ExpiresAt)
	return a, notFound(err)
}

func collectAuths(ctx context.Context, q DBTX, sql string, args ...any) ([]DeviceAuth, error) {
	rows, err := q.Query(ctx, sql, args...)
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

// CreateDeviceAuth 保存设备授权请求。
func CreateDeviceAuth(ctx context.Context, q DBTX, a DeviceAuth) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `INSERT INTO device_auths (nonce, poll_secret_hash, tools, device_name, expires_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`, a.Nonce, a.PollSecretHash, a.Tools, a.DeviceName, a.ExpiresAt).Scan(&id)
	return id, err
}

// GetDeviceAuth 按编号读取（forUpdate 为真时锁行）。
func GetDeviceAuth(ctx context.Context, q DBTX, id int64, forUpdate bool) (DeviceAuth, error) {
	sql := `SELECT ` + authColumns + authFrom + `WHERE x.id = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF x`
	}
	return scanAuth(q.QueryRow(ctx, sql, id))
}

// DeviceAuthByNonce 按授权页参数读取（forUpdate 为真时锁行）。
func DeviceAuthByNonce(ctx context.Context, q DBTX, nonce string, forUpdate bool) (DeviceAuth, error) {
	sql := `SELECT ` + authColumns + authFrom + `WHERE x.nonce = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF x`
	}
	return scanAuth(q.QueryRow(ctx, sql, nonce))
}

// UpdateDeviceAuth 写回状态、账号、设备与审批人。
func UpdateDeviceAuth(ctx context.Context, q DBTX, a DeviceAuth) error {
	_, err := q.Exec(ctx, `UPDATE device_auths SET status = $2, account_id = $3, device_id = $4,
		decided_by = $5, expires_at = $6 WHERE id = $1`, a.ID, a.Status, a.AccountID, a.DeviceID, a.DecidedBy, a.ExpiresAt)
	return err
}

// ListAwaitingDeviceAuths 列出等待管理员批准的设备申请：wsID 非 0 时只列申请人在该工作空间在册的。
func ListAwaitingDeviceAuths(ctx context.Context, q DBTX, wsID int64) ([]DeviceAuth, error) {
	return collectAuths(ctx, q, `SELECT `+authColumns+authFrom+`WHERE x.status = 'awaiting_admin' AND x.expires_at > now()
		AND ($1 = 0 OR x.account_id IN (SELECT account_id FROM members WHERE workspace_id = $1 AND status = 'active'))
		ORDER BY x.id`, wsID)
}

// DenyPendingDeviceAuths 把账号等待中的设备申请改为拒绝（停用时）。
func DenyPendingDeviceAuths(ctx context.Context, q DBTX, accountID int64) error {
	_, err := q.Exec(ctx, `UPDATE device_auths SET status = 'denied'
		WHERE account_id = $1 AND status IN ('pending', 'awaiting_admin')`, accountID)
	return err
}

// CountEverDevices 返回账号有过的设备数（含已吊销、已批准未领取的）。首台设备免审只看“从未有过设备”，
// 吊销全部设备后再配对不算首台。
func CountEverDevices(ctx context.Context, q DBTX, accountID int64) (int, error) {
	var n int
	err := q.QueryRow(ctx, `SELECT count(*) FROM devices WHERE account_id = $1`, accountID).Scan(&n)
	return n, err
}

// ---- 通讯工具连接 ----

// 连接状态
const (
	LinkPending = "pending"
	LinkLinked  = "linked"
	LinkFailed  = "failed"
	LinkExpired = "expired" // 只用于展示：过期的请求在库里仍是 pending
	// LinkAwaitingConfirm 表示机器人已收到绑定码、记下了发码的人，等本人在网页确认（钉钉）
	LinkAwaitingConfirm = "awaiting_confirm"
)

// LinkRequest 是一次通讯工具连接请求。钉钉先由机器人记下发码的人（Candidate*，状态 awaiting_confirm），
// 本人在网页确认后才连接。
type LinkRequest struct {
	ID              int64
	AccountID       int64
	Provider        string
	Nonce           string // Telegram 是机器人链接参数；钉钉是发给机器人的绑定码
	Status          string
	Detail          string
	ExpiresAt       time.Time
	CandidateTenant string
	CandidateUserID string
	CandidateName   string
}

const linkColumns = `id, account_id, provider, nonce, status, detail, expires_at, candidate_tenant, candidate_user_id, candidate_name`

func scanLink(row interface{ Scan(...any) error }) (LinkRequest, error) {
	var l LinkRequest
	err := row.Scan(&l.ID, &l.AccountID, &l.Provider, &l.Nonce, &l.Status, &l.Detail, &l.ExpiresAt,
		&l.CandidateTenant, &l.CandidateUserID, &l.CandidateName)
	return l, notFound(err)
}

// CreateLinkRequest 保存连接请求。
func CreateLinkRequest(ctx context.Context, q DBTX, l LinkRequest) (LinkRequest, error) {
	return scanLink(q.QueryRow(ctx, `INSERT INTO link_requests (account_id, provider, nonce, expires_at)
		VALUES ($1, $2, $3, $4) RETURNING `+linkColumns, l.AccountID, l.Provider, l.Nonce, l.ExpiresAt))
}

// LinkRequestByNonceForUpdate 按链接参数读取并锁定连接请求。
func LinkRequestByNonceForUpdate(ctx context.Context, q DBTX, provider, nonce string) (LinkRequest, error) {
	return scanLink(q.QueryRow(ctx, `SELECT `+linkColumns+` FROM link_requests
		WHERE provider = $1 AND nonce = $2 FOR UPDATE`, provider, nonce))
}

// GetLinkRequest 读取账号自己的连接请求。
func GetLinkRequest(ctx context.Context, q DBTX, accountID, id int64) (LinkRequest, error) {
	return scanLink(q.QueryRow(ctx, `SELECT `+linkColumns+` FROM link_requests WHERE id = $1 AND account_id = $2`, id, accountID))
}

// LinkRequestForUpdate 读取并锁定账号自己的连接请求（网页确认时）。
func LinkRequestForUpdate(ctx context.Context, q DBTX, accountID, id int64) (LinkRequest, error) {
	return scanLink(q.QueryRow(ctx, `SELECT `+linkColumns+` FROM link_requests WHERE id = $1 AND account_id = $2 FOR UPDATE`, id, accountID))
}

// SetLinkCandidate 记下发来绑定码的通讯账号，状态改为等网页确认。
func SetLinkCandidate(ctx context.Context, q DBTX, id int64, tenant, userID, name, detail string) error {
	return execOne(ctx, q, `UPDATE link_requests SET status = 'awaiting_confirm', candidate_tenant = $2, candidate_user_id = $3,
		candidate_name = $4, detail = $5 WHERE id = $1`, id, tenant, userID, name, detail)
}

// FinishLinkRequest 写回连接结果。
func FinishLinkRequest(ctx context.Context, q DBTX, id int64, status, detail string) error {
	_, err := q.Exec(ctx, `UPDATE link_requests SET status = $2, detail = $3 WHERE id = $1`, id, status, detail)
	return err
}

// ---- 群里的审批消息 ----

// BotPost 是机器人在某个群里发的一条审批消息。
type BotPost struct {
	Kind        string
	RefID       int64
	WorkspaceID int64
	Provider    string
	ChatID      string // Telegram 群编号；钉钉 openConversationId
	MessageID   string // Telegram 消息编号；钉钉卡片的 outTrackId
}

const botPostColumns = `kind, ref_id, workspace_id, provider, chat_id, message_id`

// InsertBotPost 记下审批消息，处理后据此把各群里的消息改成结果。
func InsertBotPost(ctx context.Context, q DBTX, p BotPost) error {
	_, err := q.Exec(ctx, `INSERT INTO bot_posts (kind, ref_id, workspace_id, provider, chat_id, message_id)
		VALUES ($1, $2, $3, $4, $5, $6)`, p.Kind, p.RefID, p.WorkspaceID, p.Provider, p.ChatID, p.MessageID)
	return err
}

func collectBotPosts(ctx context.Context, q DBTX, sql string, args ...any) ([]BotPost, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BotPost
	for rows.Next() {
		var p BotPost
		if err := rows.Scan(&p.Kind, &p.RefID, &p.WorkspaceID, &p.Provider, &p.ChatID, &p.MessageID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ListBotPosts 列出某条申请的审批消息。
func ListBotPosts(ctx context.Context, q DBTX, kind string, refID int64) ([]BotPost, error) {
	return collectBotPosts(ctx, q, `SELECT `+botPostColumns+` FROM bot_posts WHERE kind = $1 AND ref_id = $2 ORDER BY id`, kind, refID)
}

// BotPostByMessage 按消息编号找审批消息。Telegram 的消息编号只在群内唯一，要带上群编号；
// 钉钉卡片的 outTrackId 全局唯一（卡片回调只带它），chatID 传空。
func BotPostByMessage(ctx context.Context, q DBTX, provider, chatID, messageID string) (BotPost, error) {
	ps, err := collectBotPosts(ctx, q, `SELECT `+botPostColumns+` FROM bot_posts
		WHERE provider = $1 AND message_id = $2 AND ($3 = '' OR chat_id = $3) ORDER BY id LIMIT 1`, provider, messageID, chatID)
	if err != nil {
		return BotPost{}, err
	}
	if len(ps) == 0 {
		return BotPost{}, ErrNotFound
	}
	return ps[0], nil
}
