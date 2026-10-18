package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
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
	// ErrDecided 表示申请已经处理过。
	ErrDecided = errors.New("这条申请已经处理过了")
	// ErrNoDevice 表示令牌不属于任何设备（管理员手工签发的令牌）。
	ErrNoDevice = errors.New("这个令牌不属于任何设备，请执行 cohive login 重新配对这台电脑")
	// ErrJoinPending 表示已有待审的加入申请。
	ErrJoinPending = errors.New("你已提交过加入申请，正在等管理员批准")
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
		return inputErr("登录名 %q 不合法：小写字母、数字、下划线、横线，1 到 32 个字符", name)
	}
	return nil
}

// CheckName 校验登录名或工作空间名的格式。
func CheckName(name string) error { return checkMemberName(name) }

func cleanDisplayName(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxDisplayNameRune {
		s = string(r[:maxDisplayNameRune])
	}
	return s
}

// deciderName 是审批消息里显示的审批人。
func deciderName(actor *store.Account) string {
	if actor == nil {
		return "服务器管理员"
	}
	return actor.Name
}

func actorID(actor *store.Account) *int64 {
	if actor == nil {
		return nil
	}
	id := actor.ID
	return &id
}

// ---- 邀请 ----

// InviteSpec 描述要生成的邀请。
type InviteSpec struct {
	Kind    string        // store.InvitePersonal 或 store.InviteTeam
	Admin   bool          // 个人邀请：加入后为管理员
	MaxUses int           // 团队邀请的可用次数，0 时为 20
	TTL     time.Duration // 0 时为 7 天
}

// CreatedInvite 是生成的邀请；Code 只在这里出现一次。
type CreatedInvite struct {
	Invite store.Invite
	Code   string
}

// CreateInvite 由工作空间管理员（或服务器管理员）生成邀请。
func (s *Service) CreateInvite(ctx context.Context, act Actor, wsID int64, spec InviteSpec) (CreatedInvite, error) {
	actor := act.Account
	inv := store.Invite{WorkspaceID: wsID, Kind: spec.Kind, Role: store.RoleMember, MaxUses: 1, CreatedBy: actorID(actor)}
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
	var saved store.Invite
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := canAdmin(ctx, tx, actor, wsID); err != nil {
			return err
		}
		var err error
		if saved, err = store.CreateInvite(ctx, tx, inv, hashSecret("invite", code)); err != nil {
			return err
		}
		// 邀请码绝不进记录
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(wsID), Action: ActInviteCreate,
			TargetKind: "invite", TargetID: idp(saved.ID), Detail: map[string]any{"kind": saved.Kind, "role": saved.Role,
				"max_uses": saved.MaxUses, "expires_at": saved.ExpiresAt.UTC().Format(time.RFC3339)}})
	})
	if err != nil {
		return CreatedInvite{}, err
	}
	return CreatedInvite{Invite: saved, Code: code}, nil
}

// RevokeInvite 由管理员作废邀请。
func (s *Service) RevokeInvite(ctx context.Context, act Actor, wsID, id int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		kind, err := store.RevokeInvite(ctx, tx, wsID, id)
		if err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(wsID), Action: ActInviteRevoke,
			TargetKind: "invite", TargetID: idp(id), Detail: map[string]any{"kind": kind}})
	})
}

// ListInvites 由管理员列出工作空间的邀请。
func (s *Service) ListInvites(ctx context.Context, actor *store.Account, wsID int64) ([]store.Invite, error) {
	if err := canAdmin(ctx, s.Pool, actor, wsID); err != nil {
		return nil, err
	}
	return store.ListInvites(ctx, s.Pool, wsID)
}

// InviteInfo 是注册页展示的邀请信息。
type InviteInfo struct {
	Workspace      string // 路径
	WorkspaceLabel string // 显示名
	Kind           string
	Role           string
	Valid          bool
	AlreadyMember  bool // 查看者（已登录时）已是该工作空间的在册成员
}

// LookupInvite 返回邀请信息（不消耗次数）；viewer 为已登录的账号（可为空）。
func (s *Service) LookupInvite(ctx context.Context, code string, viewer *store.Account) (InviteInfo, error) {
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
	info := InviteInfo{Workspace: ws.Name, WorkspaceLabel: ws.Label(), Kind: inv.Kind, Role: inv.Role, Valid: inv.Usable(time.Now())}
	if viewer != nil {
		if info.AlreadyMember, err = store.IsActiveMemberOf(ctx, s.Pool, viewer.ID, ws.ID); err != nil {
			return InviteInfo{}, err
		}
	}
	return info, nil
}

// ---- 注册（新人）与加入（已有账号） ----

// RegisterInput 是注册表单。
type RegisterInput struct {
	Code        string
	Name        string
	DisplayName string
	Password    string
	Language    string // 注册时的界面语言，写到新账号上；不认识的语言忽略
}

// RegisterResult 是注册结果：个人邀请直接得到账号与成员资格；团队邀请得到待审申请。
type RegisterResult struct {
	Account   *store.Account
	Member    *store.Member
	Workspace store.Workspace
	Signup    *store.Signup
}

// nameTaken 判断登录名是否已被账号或新人待审申请占用（全局唯一）。
func nameTaken(ctx context.Context, q store.DBTX, name string) error {
	if _, err := store.AccountByName(ctx, q, name); err == nil {
		return inputErr("登录名 %s 已被使用，请换一个", name)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	if _, err := store.PendingSignupByName(ctx, q, name); err == nil {
		return inputErr("登录名 %s 已有人申请，请换一个", name)
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	return nil
}

// usableInvite 在事务内锁住并校验邀请。
func usableInvite(ctx context.Context, tx pgx.Tx, code string) (store.Invite, error) {
	inv, err := store.InviteByCode(ctx, tx, hashSecret("invite", code), true)
	if errors.Is(err, store.ErrNotFound) {
		return inv, ErrInviteInvalid
	}
	if err != nil {
		return inv, err
	}
	if !inv.Usable(time.Now()) {
		return inv, ErrInviteInvalid
	}
	return inv, nil
}

// Register 凭邀请注册新账号。登录名全服务器唯一：不能与已有账号或别的新人待审申请重复。
func (s *Service) Register(ctx context.Context, in RegisterInput) (RegisterResult, error) {
	name := strings.TrimSpace(in.Name)
	if err := checkMemberName(name); err != nil {
		return RegisterResult{}, err
	}
	code := strings.TrimSpace(in.Code)
	if code == "" || len(code) > 64 {
		return RegisterResult{}, ErrInviteInvalid
	}
	// 先确认邀请有效再算密码哈希，拿无效邀请码刷注册接口耗不了服务器 CPU
	if inv, err := store.InviteByCode(ctx, s.Pool, hashSecret("invite", code), false); errors.Is(err, store.ErrNotFound) ||
		(err == nil && !inv.Usable(time.Now())) {
		return RegisterResult{}, ErrInviteInvalid
	} else if err != nil {
		return RegisterResult{}, err
	}
	hash, err := hashPassword(ctx, in.Password)
	if err != nil {
		return RegisterResult{}, err
	}
	var res RegisterResult
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		inv, err := usableInvite(ctx, tx, code)
		if err != nil {
			return err
		}
		if err := nameTaken(ctx, tx, name); err != nil {
			return err
		}
		if err := store.UseInvite(ctx, tx, inv.ID); err != nil {
			return err
		}
		if res.Workspace, err = store.GetWorkspace(ctx, tx, inv.WorkspaceID); err != nil {
			return err
		}
		display := cleanDisplayName(in.DisplayName)
		lang, _ := normLanguage(in.Language)
		if inv.Kind == store.InviteTeam {
			sg, err := store.CreateSignup(ctx, tx, store.Signup{InviteID: inv.ID, WorkspaceID: inv.WorkspaceID,
				Name: name, DisplayName: display, PasswordHash: hash, Language: lang})
			if isUniqueViolation(err) {
				// 用不同邀请同时申请同一个名字时，由唯一约束兜底
				return inputErr("登录名 %s 已有人申请，请换一个", name)
			}
			res.Signup = &sg
			return err
		}
		acc, m, err := createAccount(ctx, tx, inv.WorkspaceID, name, display, hash, lang, inv.Role, nil)
		if err != nil {
			return err
		}
		res.Account, res.Member = &acc, &m
		return recordInviteUse(ctx, tx, acc, m, inv, true)
	})
	return res, err
}

// recordInviteUse 记下本人凭个人邀请得到成员资格。
func recordInviteUse(ctx context.Context, tx pgx.Tx, acc store.Account, m store.Member, inv store.Invite, created bool) error {
	d := map[string]any{"invite_id": inv.ID, "role": m.Role}
	if created {
		d["account_created"] = true
	}
	return record(ctx, tx, WebActor(acc), store.AdminEvent{WorkspaceID: idp(m.WorkspaceID), Action: ActInviteUse,
		TargetKind: "member", TargetID: idp(m.ID), TargetName: acc.Name, Detail: d})
}

// createAccount 新建有密码的账号并加入工作空间。
func createAccount(ctx context.Context, tx pgx.Tx, wsID int64, name, display, passwordHash, lang, role string, addedBy *int64) (store.Account, store.Member, error) {
	acc, err := store.CreateAccount(ctx, tx, name, display)
	if err != nil {
		if isUniqueViolation(err) {
			return store.Account{}, store.Member{}, inputErr("登录名 %s 已被使用，请换一个", name)
		}
		return store.Account{}, store.Member{}, err
	}
	if err := store.SetAccountPassword(ctx, tx, acc.ID, passwordHash, false); err != nil {
		return store.Account{}, store.Member{}, err
	}
	if lang != "" {
		if err := store.SetAccountLanguage(ctx, tx, acc.ID, lang); err != nil {
			return store.Account{}, store.Member{}, err
		}
		acc.Language = lang
	}
	m, err := store.AddMembership(ctx, tx, acc.ID, wsID, role, addedBy)
	return acc, m, err
}

// AcceptResult 是已有账号凭邀请加入的结果：个人邀请直接成为成员；团队邀请生成待审的加入申请。
type AcceptResult struct {
	Workspace store.Workspace
	Member    *store.Member
	Signup    *store.Signup
}

// AcceptInvite 由已登录的账号接受邀请。已是在册成员时不消耗邀请。
func (s *Service) AcceptInvite(ctx context.Context, acc store.Account, code string) (AcceptResult, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 64 {
		return AcceptResult{}, ErrInviteInvalid
	}
	var res AcceptResult
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := store.LockAccount(ctx, tx, acc.ID)
		if err != nil {
			return err
		}
		if a.Disabled() {
			return store.ErrAccountDisabled
		}
		inv, err := usableInvite(ctx, tx, code)
		if err != nil {
			return err
		}
		if res.Workspace, err = store.GetWorkspace(ctx, tx, inv.WorkspaceID); err != nil {
			return err
		}
		if ok, err := store.IsActiveMemberOf(ctx, tx, a.ID, inv.WorkspaceID); err != nil {
			return err
		} else if ok {
			return store.ErrAlreadyMember
		}
		if inv.Kind == store.InviteTeam {
			if _, err := store.PendingJoinRequest(ctx, tx, inv.WorkspaceID, a.ID); err == nil {
				return ErrJoinPending
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			sg, err := store.CreateJoinRequest(ctx, tx, inv.ID, inv.WorkspaceID, a.ID)
			if isUniqueViolation(err) {
				return ErrJoinPending
			}
			if err != nil {
				return err
			}
			res.Signup = &sg
			return store.UseInvite(ctx, tx, inv.ID)
		}
		m, err := store.AddMembership(ctx, tx, a.ID, inv.WorkspaceID, inv.Role, nil)
		if err != nil {
			return err
		}
		res.Member = &m
		if err := store.UseInvite(ctx, tx, inv.ID); err != nil {
			return err
		}
		return recordInviteUse(ctx, tx, a, m, inv, false)
	})
	return res, err
}

// SignupDecision 是注册或加入申请的审批结果。
type SignupDecision struct {
	Signup    store.Signup
	Workspace store.Workspace
	Account   *store.Account // 批准后的账号
	Member    *store.Member  // 批准后的成员资格
	Decider   string
}

// DecideSignup 由管理员批准或拒绝注册与加入申请。wsID 非 0 时申请必须属于该工作空间（管理页、本群按钮）。
func (s *Service) DecideSignup(ctx context.Context, act Actor, wsID, id int64, approve bool) (SignupDecision, error) {
	actor := act.Account
	var d SignupDecision
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		sg, err := store.GetSignupForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if wsID != 0 && sg.WorkspaceID != wsID {
			return fmt.Errorf("申请 %d：%w", id, store.ErrNotFound)
		}
		if err := canAdmin(ctx, tx, actor, sg.WorkspaceID); err != nil {
			return err
		}
		if sg.Status != store.SignupPending {
			return ErrDecided
		}
		d.Decider = deciderName(actor)
		var accID *int64
		sg.Status = store.SignupRejected
		if approve {
			var acc store.Account
			var m store.Member
			if sg.Existing {
				if acc, err = store.LockAccount(ctx, tx, *sg.AccountID); err != nil {
					return err
				}
				if acc.Disabled() {
					return inputErr("账号 %s 已停用，不能加入", acc.Name)
				}
				if m, err = store.AddMembership(ctx, tx, acc.ID, sg.WorkspaceID, store.RoleMember, actorID(actor)); err != nil {
					return err
				}
			} else if acc, m, err = createAccount(ctx, tx, sg.WorkspaceID, sg.Name, sg.DisplayName, sg.PasswordHash, sg.Language,
				store.RoleMember, actorID(actor)); err != nil {
				return err
			}
			accID = &acc.ID
			sg.Status, sg.MemberID, d.Account, d.Member = store.SignupApproved, &m.ID, &acc, &m
		}
		if err := store.DecideSignup(ctx, tx, sg.ID, sg.Status, accID, sg.MemberID, actorID(actor)); err != nil {
			return err
		}
		sg.PasswordHash = "" // 已决的申请不再保留密码哈希
		d.Signup = sg
		action := map[[2]bool]string{{false, true}: ActSignupApprove, {false, false}: ActSignupReject,
			{true, true}: ActJoinApprove, {true, false}: ActJoinReject}[[2]bool{sg.Existing, approve}]
		if err := record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(sg.WorkspaceID), Action: action,
			TargetKind: "signup", TargetID: idp(sg.ID), TargetName: sg.Name, Detail: map[string]any{"applicant": sg.Name}}); err != nil {
			return err
		}
		d.Workspace, err = store.GetWorkspace(ctx, tx, sg.WorkspaceID)
		return err
	})
	return d, err
}

// PendingSignups 由管理员列出工作空间的待审申请。
func (s *Service) PendingSignups(ctx context.Context, actor *store.Account, wsID int64) ([]store.Signup, error) {
	if err := canAdmin(ctx, s.Pool, actor, wsID); err != nil {
		return nil, err
	}
	return store.ListPendingSignups(ctx, s.Pool, wsID)
}
