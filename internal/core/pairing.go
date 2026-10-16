package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// 有效期
const (
	DefaultInviteTTL   = 7 * 24 * time.Hour
	MaxInviteTTL       = 30 * 24 * time.Hour
	MaxInviteUses      = 100
	DeviceAuthTTL      = 15 * time.Minute // 等待成员在浏览器里授权
	DeviceAwaitTTL     = 24 * time.Hour   // 等待管理员批准与终端领取
	DeviceAuthPrefix   = "da_"
	inviteCodeBytes    = 24
	minPasswordLength  = 10
	maxDisplayNameRune = 64
)

var (
	// ErrInviteInvalid 表示邀请不存在、已作废、已用完或已过期。
	ErrInviteInvalid = errors.New("邀请链接无效、已用完或已过期，请向管理员要一个新的")
	// ErrAuthGone 表示设备授权不存在、已处理、已过期，或轮询密钥不对。
	ErrAuthGone = errors.New("这次设备授权已失效，请在终端重新执行 cohive login")
	// ErrNotAdmin 表示操作者不是该工作空间的管理员。
	ErrNotAdmin = errors.New("只有工作空间管理员能执行这个操作")
	// ErrDecided 表示申请已经处理过。
	ErrDecided = errors.New("这条申请已经处理过了")
	// ErrNoDevice 表示令牌不属于任何设备（管理员手工签发的令牌）。
	ErrNoDevice = errors.New("这个令牌不属于任何设备，请执行 cohive login 重新配对这台电脑")
)

var (
	toolName         = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)
	memberNameRule   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
	deviceNameUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)
)

func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func hashSecret(kind, secret string) []byte {
	h := sha256.Sum256([]byte("cohive-" + kind + ":" + secret))
	return h[:]
}

// cleanDeviceName 只保留主机名字符并限制长度。设备名由终端自报，只用于展示。
func cleanDeviceName(s string) string {
	s = deviceNameUnsafe.ReplaceAllString(s, "")
	if len(s) > 32 {
		s = s[:32]
	}
	if s == "" {
		s = "unknown"
	}
	return s
}

// cleanTools 校验并去重工具名。
func cleanTools(tools []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, t := range tools {
		t = strings.TrimSpace(t)
		if !toolName.MatchString(t) {
			return nil, inputErr("工具名 %q 不合法，例如 claude-code、codex", t)
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 || len(out) > 4 {
		return nil, inputErr("工具数量应为 1 到 4 个")
	}
	return out, nil
}

func checkMemberName(name string) error {
	if !memberNameRule.MatchString(name) {
		return inputErr("成员名 %q 不合法：小写字母、数字、下划线、横线，1 到 32 个字符", name)
	}
	return nil
}

func cleanDisplayName(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxDisplayNameRune {
		s = string(r[:maxDisplayNameRune])
	}
	return s
}

// requireAdmin 校验操作者是该工作空间的管理员；actor 为空表示服务器管理员在命令行操作。
func requireAdmin(actor *store.Member, wsID int64) error {
	if actor != nil && (!actor.IsAdmin() || actor.WorkspaceID != wsID) {
		return ErrNotAdmin
	}
	return nil
}

// ---- 邀请 ----

// InviteSpec 描述要生成的邀请。
type InviteSpec struct {
	Kind    string        // store.InvitePersonal 或 store.InviteTeam
	Admin   bool          // 个人邀请：注册后为管理员
	MaxUses int           // 团队邀请的可用次数，0 时为 20
	TTL     time.Duration // 0 时为 7 天
}

// CreatedInvite 是生成的邀请；Code 只在这里出现一次。
type CreatedInvite struct {
	Invite store.Invite
	Code   string
}

// CreateInvite 由管理员生成邀请。
func (s *Service) CreateInvite(ctx context.Context, actor *store.Member, wsID int64, spec InviteSpec) (CreatedInvite, error) {
	if err := requireAdmin(actor, wsID); err != nil {
		return CreatedInvite{}, err
	}
	inv := store.Invite{WorkspaceID: wsID, Kind: spec.Kind, Role: store.RoleMember, MaxUses: 1}
	if actor != nil {
		inv.CreatedBy = &actor.ID
	}
	switch spec.Kind {
	case store.InvitePersonal:
		if spec.Admin {
			inv.Role = store.RoleAdmin
		}
	case store.InviteTeam:
		if spec.Admin {
			return CreatedInvite{}, inputErr("团队邀请不能设为管理员；需要管理员请用个人邀请")
		}
		inv.MaxUses = spec.MaxUses
		if inv.MaxUses == 0 {
			inv.MaxUses = 20
		}
		if inv.MaxUses < 1 || inv.MaxUses > MaxInviteUses {
			return CreatedInvite{}, inputErr("团队邀请的可用次数应为 1 到 %d", MaxInviteUses)
		}
	default:
		return CreatedInvite{}, inputErr("邀请类型应为个人邀请或团队邀请")
	}
	ttl := spec.TTL
	if ttl == 0 {
		ttl = DefaultInviteTTL
	}
	if ttl < time.Hour || ttl > MaxInviteTTL {
		return CreatedInvite{}, inputErr("邀请有效期应在 1 小时到 30 天之间")
	}
	inv.ExpiresAt = time.Now().Add(ttl)
	code, err := randomString(inviteCodeBytes)
	if err != nil {
		return CreatedInvite{}, err
	}
	saved, err := store.CreateInvite(ctx, s.Pool, inv, hashSecret("invite", code))
	if err != nil {
		return CreatedInvite{}, err
	}
	return CreatedInvite{Invite: saved, Code: code}, nil
}

// RevokeInvite 由管理员作废邀请。
func (s *Service) RevokeInvite(ctx context.Context, actor *store.Member, wsID, id int64) error {
	if err := requireAdmin(actor, wsID); err != nil {
		return err
	}
	return store.RevokeInvite(ctx, s.Pool, wsID, id)
}

// InviteInfo 是注册页展示的邀请信息。
type InviteInfo struct {
	Workspace string
	Kind      string
	Valid     bool
}

// LookupInvite 返回邀请信息（不消耗次数）。
func (s *Service) LookupInvite(ctx context.Context, code string) (InviteInfo, error) {
	if code == "" || len(code) > 64 {
		return InviteInfo{}, ErrInviteInvalid
	}
	inv, err := store.InviteByCode(ctx, s.Pool, hashSecret("invite", code), false)
	if errors.Is(err, store.ErrNotFound) {
		return InviteInfo{}, ErrInviteInvalid
	}
	if err != nil {
		return InviteInfo{}, err
	}
	ws, err := store.GetWorkspace(ctx, s.Pool, inv.WorkspaceID)
	if err != nil {
		return InviteInfo{}, err
	}
	return InviteInfo{Workspace: ws.Name, Kind: inv.Kind, Valid: inv.Usable(time.Now())}, nil
}

// ---- 注册 ----

// RegisterInput 是注册表单。
type RegisterInput struct {
	Code        string
	Name        string
	DisplayName string
	Password    string
}

// RegisterResult 是注册结果：个人邀请直接得到成员；团队邀请得到待审申请。
type RegisterResult struct {
	Member *store.Member
	Signup *store.Signup
}

// Register 凭邀请注册。成员名在工作空间里不能与已有成员或待审申请重复。
func (s *Service) Register(ctx context.Context, in RegisterInput) (RegisterResult, error) {
	name := strings.TrimSpace(in.Name)
	if err := checkMemberName(name); err != nil {
		return RegisterResult{}, err
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return RegisterResult{}, err
	}
	code := strings.TrimSpace(in.Code)
	if code == "" || len(code) > 64 {
		return RegisterResult{}, ErrInviteInvalid
	}
	var res RegisterResult
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		inv, err := store.InviteByCode(ctx, tx, hashSecret("invite", code), true)
		if errors.Is(err, store.ErrNotFound) {
			return ErrInviteInvalid
		}
		if err != nil {
			return err
		}
		if !inv.Usable(time.Now()) {
			return ErrInviteInvalid
		}
		if _, err := store.MemberByName(ctx, tx, inv.WorkspaceID, name); err == nil {
			return inputErr("成员名 %s 已被使用，请换一个", name)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if _, err := store.PendingSignupByName(ctx, tx, inv.WorkspaceID, name); err == nil {
			return inputErr("成员名 %s 已有人申请，请换一个", name)
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := store.UseInvite(ctx, tx, inv.ID); err != nil {
			return err
		}
		display := cleanDisplayName(in.DisplayName)
		if inv.Kind == store.InviteTeam {
			sg, err := store.CreateSignup(ctx, tx, store.Signup{InviteID: inv.ID, WorkspaceID: inv.WorkspaceID,
				Name: name, DisplayName: display, PasswordHash: hash})
			res.Signup = &sg
			return err
		}
		m, err := createAccount(ctx, tx, inv.WorkspaceID, name, display, hash, inv.Role)
		res.Member = &m
		return err
	})
	return res, err
}

func createAccount(ctx context.Context, tx pgx.Tx, wsID int64, name, display, passwordHash, role string) (store.Member, error) {
	m, err := store.CreateMember(ctx, tx, wsID, name, display)
	if err != nil {
		if isUniqueViolation(err) {
			return store.Member{}, inputErr("成员名 %s 已被使用，请换一个", name)
		}
		return store.Member{}, err
	}
	if err := store.SetMemberPassword(ctx, tx, m.ID, passwordHash, false); err != nil {
		return store.Member{}, err
	}
	if role == store.RoleAdmin {
		if err := store.SetMemberRole(ctx, tx, m.ID, store.RoleAdmin); err != nil {
			return store.Member{}, err
		}
		m.Role = store.RoleAdmin
	}
	return m, nil
}

// SignupDecision 是注册申请的审批结果。
type SignupDecision struct {
	Signup    store.Signup
	Workspace store.Workspace
	Member    *store.Member
	Decider   string
}

// DecideSignup 由管理员批准或拒绝注册申请。
func (s *Service) DecideSignup(ctx context.Context, actor *store.Member, id int64, approve bool) (SignupDecision, error) {
	var d SignupDecision
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		sg, err := store.GetSignupForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := requireAdmin(actor, sg.WorkspaceID); err != nil {
			return err
		}
		if sg.Status != store.SignupPending {
			return ErrDecided
		}
		var decidedBy *int64
		d.Decider = "服务器管理员"
		if actor != nil {
			decidedBy, d.Decider = &actor.ID, actor.Name
		}
		sg.Status = store.SignupRejected
		if approve {
			m, err := createAccount(ctx, tx, sg.WorkspaceID, sg.Name, sg.DisplayName, sg.PasswordHash, store.RoleMember)
			if err != nil {
				return err
			}
			sg.Status, sg.MemberID, d.Member = store.SignupApproved, &m.ID, &m
		}
		if err := store.DecideSignup(ctx, tx, sg.ID, sg.Status, sg.MemberID, decidedBy); err != nil {
			return err
		}
		d.Signup = sg
		d.Workspace, err = store.GetWorkspace(ctx, tx, sg.WorkspaceID)
		return err
	})
	return d, err
}

// ---- 设备授权 ----

// DeviceAuthStart 是终端发起授权的结果；PollSecret 只在这里出现一次。
type DeviceAuthStart struct {
	RequestID  int64
	Nonce      string
	PollSecret string
	ExpiresIn  time.Duration
}

// StartDeviceAuth 由终端发起设备授权。
func (s *Service) StartDeviceAuth(ctx context.Context, tools []string, deviceName string) (DeviceAuthStart, error) {
	tools, err := cleanTools(tools)
	if err != nil {
		return DeviceAuthStart{}, err
	}
	secret, err := randomString(32)
	if err != nil {
		return DeviceAuthStart{}, err
	}
	nonce, err := randomString(18)
	if err != nil {
		return DeviceAuthStart{}, err
	}
	nonce = DeviceAuthPrefix + nonce
	id, err := store.CreateDeviceAuth(ctx, s.Pool, store.DeviceAuth{Nonce: nonce, PollSecretHash: hashSecret("poll", secret),
		Tools: tools, DeviceName: cleanDeviceName(deviceName), ExpiresAt: time.Now().Add(DeviceAuthTTL)})
	if err != nil {
		return DeviceAuthStart{}, err
	}
	return DeviceAuthStart{RequestID: id, Nonce: nonce, PollSecret: secret, ExpiresIn: DeviceAuthTTL}, nil
}

// DeviceAuthView 是授权页展示的内容。
type DeviceAuthView struct {
	DeviceName  string
	Tools       []string
	CreatedAt   time.Time
	Status      string
	FirstDevice bool
}

func effectiveStatus(a store.DeviceAuth) string {
	if (a.Status == store.AuthPending || a.Status == store.AuthAwaitingAdmin || a.Status == store.AuthApproved) &&
		time.Now().After(a.ExpiresAt) {
		return store.AuthExpired
	}
	return a.Status
}

// ViewDeviceAuth 返回授权页内容。已被别的成员处理过的请求视为失效。
func (s *Service) ViewDeviceAuth(ctx context.Context, viewer store.Member, nonce string) (DeviceAuthView, error) {
	a, err := store.DeviceAuthByNonce(ctx, s.Pool, nonce, false)
	if errors.Is(err, store.ErrNotFound) || (err == nil && a.MemberID != nil && *a.MemberID != viewer.ID) {
		return DeviceAuthView{}, ErrAuthGone
	}
	if err != nil {
		return DeviceAuthView{}, err
	}
	n, err := store.CountEverDevices(ctx, s.Pool, viewer.ID)
	if err != nil {
		return DeviceAuthView{}, err
	}
	return DeviceAuthView{DeviceName: a.DeviceName, Tools: a.Tools, CreatedAt: a.CreatedAt, Status: effectiveStatus(a), FirstDevice: n == 0}, nil
}

// AuthorizeDevice 由已登录网页的成员授权或拒绝终端发起的设备授权。
// 成员还没有任何设备时直接批准；否则转给管理员批准。
func (s *Service) AuthorizeDevice(ctx context.Context, m store.Member, nonce string, approve bool) (store.DeviceAuth, error) {
	var out store.DeviceAuth
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := store.DeviceAuthByNonce(ctx, tx, nonce, true)
		if errors.Is(err, store.ErrNotFound) {
			return ErrAuthGone
		}
		if err != nil {
			return err
		}
		if a.Status != store.AuthPending || time.Now().After(a.ExpiresAt) {
			return ErrAuthGone
		}
		a.MemberID, a.WorkspaceID, a.MemberName = &m.ID, &m.WorkspaceID, m.Name
		switch {
		case !approve:
			a.Status = store.AuthDenied
		default:
			// 先锁成员行，同一成员并发授权时第二个会看到第一个建的设备
			if err := store.LockMember(ctx, tx, m.ID); err != nil {
				return err
			}
			n, err := store.CountEverDevices(ctx, tx, m.ID)
			if err != nil {
				return err
			}
			a.ExpiresAt = time.Now().Add(DeviceAwaitTTL)
			if n > 0 {
				a.Status = store.AuthAwaitingAdmin
			} else if err := approveAuth(ctx, tx, &a); err != nil {
				return err
			}
		}
		out = a
		return store.UpdateDeviceAuth(ctx, tx, a)
	})
	return out, err
}

func approveAuth(ctx context.Context, tx pgx.Tx, a *store.DeviceAuth) error {
	d, err := store.CreateDevice(ctx, tx, *a.MemberID, a.DeviceName)
	if err != nil {
		return err
	}
	a.Status, a.DeviceID = store.AuthApproved, &d.ID
	return nil
}

// DeviceDecision 是管理员审批设备的结果。
type DeviceDecision struct {
	Auth      store.DeviceAuth
	Workspace store.Workspace
	Decider   string
}

// DecideDeviceAuth 由管理员批准或拒绝非首台设备。
func (s *Service) DecideDeviceAuth(ctx context.Context, actor *store.Member, id int64, approve bool) (DeviceDecision, error) {
	var d DeviceDecision
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := store.GetDeviceAuth(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if a.WorkspaceID == nil {
			return ErrDecided
		}
		if err := requireAdmin(actor, *a.WorkspaceID); err != nil {
			return err
		}
		if a.Status != store.AuthAwaitingAdmin || time.Now().After(a.ExpiresAt) {
			return ErrDecided
		}
		d.Decider = "服务器管理员"
		if actor != nil {
			a.DecidedBy, d.Decider = &actor.ID, actor.Name
		}
		if approve {
			if err := approveAuth(ctx, tx, &a); err != nil {
				return err
			}
		} else {
			a.Status = store.AuthDenied
		}
		if err := store.UpdateDeviceAuth(ctx, tx, a); err != nil {
			return err
		}
		d.Auth = a
		d.Workspace, err = store.GetWorkspace(ctx, tx, *a.WorkspaceID)
		return err
	})
	return d, err
}

// DeviceTokens 是交给终端的结果；Tokens 只交付一次。
type DeviceTokens struct {
	Status    string
	Member    string
	Workspace string
	DeviceID  int64
	Tokens    map[string]string
}

// PollDeviceAuth 由终端轮询；批准后签发令牌并只交付一次。轮询不需身份，
// 所以先不加锁核对密钥，只有交付或标记过期时才锁行写库。
func (s *Service) PollDeviceAuth(ctx context.Context, id int64, secret string) (DeviceTokens, error) {
	a, err := store.GetDeviceAuth(ctx, s.Pool, id, false)
	if errors.Is(err, store.ErrNotFound) {
		return DeviceTokens{}, ErrAuthGone
	}
	if err != nil {
		return DeviceTokens{}, err
	}
	if subtle.ConstantTimeCompare(a.PollSecretHash, hashSecret("poll", secret)) != 1 {
		return DeviceTokens{}, ErrAuthGone
	}
	status := effectiveStatus(a)
	// 已批准的才需要交付；已批准但过期未领的，要把批准时建好的设备吊销掉，不留孤儿设备
	if a.Status != store.AuthApproved {
		return DeviceTokens{Status: status}, nil
	}
	var out DeviceTokens
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := store.GetDeviceAuth(ctx, tx, id, true)
		if err != nil {
			return err
		}
		out.Status = effectiveStatus(a)
		if a.Status != store.AuthApproved || a.MemberID == nil || a.DeviceID == nil {
			return nil
		}
		if out.Status == store.AuthExpired {
			a.Status = store.AuthExpired
			if err := store.RevokeDeviceByID(ctx, tx, *a.DeviceID); err != nil {
				return err
			}
			return store.UpdateDeviceAuth(ctx, tx, a)
		}
		res, err := issueTokens(ctx, tx, *a.MemberID, *a.DeviceID, a.Tools)
		if errors.Is(err, store.ErrDeviceRevoked) {
			// 批准后、领取前设备被吊销：不再交付
			a.Status, out.Status = store.AuthDenied, store.AuthDenied
			return store.UpdateDeviceAuth(ctx, tx, a)
		}
		if err != nil {
			return err
		}
		out = res
		out.Status = store.AuthApproved
		a.Status = store.AuthDelivered
		return store.UpdateDeviceAuth(ctx, tx, a)
	})
	return out, err
}

// issueTokens 在设备上签发令牌；先锁住设备行并确认未吊销，与吊销互斥。
func issueTokens(ctx context.Context, tx pgx.Tx, memberID, deviceID int64, tools []string) (DeviceTokens, error) {
	if err := store.LockActiveDevice(ctx, tx, deviceID); err != nil {
		return DeviceTokens{}, err
	}
	m, err := store.MemberByID(ctx, tx, memberID)
	if err != nil {
		return DeviceTokens{}, err
	}
	ws, err := store.GetWorkspace(ctx, tx, m.WorkspaceID)
	if err != nil {
		return DeviceTokens{}, err
	}
	out := DeviceTokens{Member: m.Name, Workspace: ws.Name, DeviceID: deviceID, Tokens: map[string]string{}}
	for _, t := range tools {
		plain, err := store.IssueDeviceToken(ctx, tx, memberID, deviceID, t)
		if err != nil {
			return DeviceTokens{}, err
		}
		out.Tokens[t] = plain
	}
	return out, nil
}

// AddDeviceTools 在已配对的设备上为其他工具补签令牌，用本机已有令牌证明身份，不需审批。
func (s *Service) AddDeviceTools(ctx context.Context, who store.Identity, tools []string) (DeviceTokens, error) {
	if who.DeviceID == nil {
		return DeviceTokens{}, ErrNoDevice
	}
	tools, err := cleanTools(tools)
	if err != nil {
		return DeviceTokens{}, err
	}
	var out DeviceTokens
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		out, err = issueTokens(ctx, tx, who.MemberID, *who.DeviceID, tools)
		return err
	})
	if errors.Is(err, store.ErrDeviceRevoked) {
		return DeviceTokens{}, ErrNoDevice
	}
	out.Status = store.AuthApproved
	return out, err
}

// RevokeDevice 吊销设备及其令牌：管理员可吊销本工作空间任何设备，成员可吊销自己的设备。
func (s *Service) RevokeDevice(ctx context.Context, actor store.Member, deviceID int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if !actor.IsAdmin() {
			devs, err := store.ListDevices(ctx, tx, actor.WorkspaceID, actor.ID)
			if err != nil {
				return err
			}
			own := false
			for _, d := range devs {
				own = own || d.ID == deviceID
			}
			if !own {
				return fmt.Errorf("设备 %d：%w", deviceID, store.ErrNotFound)
			}
		}
		return store.RevokeDevice(ctx, tx, actor.WorkspaceID, deviceID)
	})
}
