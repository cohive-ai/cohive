package core

import (
	"context"
	"crypto/subtle"
	"errors"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// DeviceSecretPrefix 是设备密钥的前缀。设备密钥只证明“这是某账号的一台已批准的电脑”，
// 只能用来签发本人在册工作空间的令牌、列工作空间、cohive web 免密登录（设计稿第 3.6 节）。
const DeviceSecretPrefix = "cohive_dev_"

// ErrNotMember 表示账号不是该工作空间的在册成员。
var ErrNotMember = i18n.Errorf("core.err.not_member")

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
	Workspaces  []string // 批准后这台电脑可用于的工作空间（显示名）
}

func effectiveStatus(a store.DeviceAuth) string {
	if (a.Status == store.AuthPending || a.Status == store.AuthAwaitingAdmin || a.Status == store.AuthApproved) &&
		time.Now().After(a.ExpiresAt) {
		return store.AuthExpired
	}
	return a.Status
}

// workspaceNames 返回给人看的工作空间名（显示名，没设时为路径）。
func workspaceNames(ms []store.Membership) []string {
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		out = append(out, m.Label)
	}
	return out
}

// ViewDeviceAuth 返回授权页内容。已被别的账号处理过的请求视为失效。
func (s *Service) ViewDeviceAuth(ctx context.Context, viewer store.Account, nonce string) (DeviceAuthView, error) {
	a, err := store.DeviceAuthByNonce(ctx, s.Pool, nonce, false)
	if errors.Is(err, store.ErrNotFound) || (err == nil && a.AccountID != nil && *a.AccountID != viewer.ID) {
		return DeviceAuthView{}, ErrAuthGone
	}
	if err != nil {
		return DeviceAuthView{}, err
	}
	n, err := store.CountEverDevices(ctx, s.Pool, viewer.ID)
	if err != nil {
		return DeviceAuthView{}, err
	}
	ms, err := store.AccountMemberships(ctx, s.Pool, viewer.ID)
	if err != nil {
		return DeviceAuthView{}, err
	}
	return DeviceAuthView{DeviceName: a.DeviceName, Tools: a.Tools, CreatedAt: a.CreatedAt, Status: effectiveStatus(a),
		FirstDevice: n == 0, Workspaces: workspaceNames(ms)}, nil
}

// AuthorizeDevice 由已登录网页的账号授权或拒绝终端发起的设备授权。
// 账号从未有过设备时直接批准（批准时建设备）；否则转给管理员批准。
func (s *Service) AuthorizeDevice(ctx context.Context, acc store.Account, nonce string, approve bool) (store.DeviceAuth, error) {
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
		// 先锁账号行：同一账号并发授权时第二个会看到第一个建的设备；同时与停用互斥
		fresh, err := store.LockAccount(ctx, tx, acc.ID)
		if err != nil {
			return err
		}
		if fresh.Disabled() {
			return store.ErrAccountDisabled
		}
		a.AccountID, a.AccountName, a.DisplayName = &fresh.ID, fresh.Name, fresh.DisplayName
		switch {
		case !approve:
			a.Status = store.AuthDenied
		default:
			n, err := store.CountEverDevices(ctx, tx, fresh.ID)
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
		if err := store.UpdateDeviceAuth(ctx, tx, a); err != nil {
			return err
		}
		if a.Status != store.AuthApproved {
			return nil
		}
		// 首台设备由本人授权即生效：也是一次凭据的发放，记下来
		return record(ctx, tx, WebActor(fresh), store.AdminEvent{Action: ActDeviceSelfApprove, TargetKind: "device_auth",
			TargetID: idp(a.ID), TargetName: a.DeviceName,
			Detail: map[string]any{"owner": fresh.Name, "device_name": a.DeviceName, "tools": a.Tools}})
	})
	return out, err
}

// approveAuth 批准设备授权：批准时就建设备（沿用 M5），领取时才生成设备密钥并签发令牌。
// 这样“吊销此人全部设备”能拦住已批准、尚未领取的授权。
func approveAuth(ctx context.Context, tx pgx.Tx, a *store.DeviceAuth) error {
	d, err := store.CreateDevice(ctx, tx, *a.AccountID, a.DeviceName)
	if err != nil {
		return err
	}
	a.Status, a.DeviceID = store.AuthApproved, &d.ID
	return nil
}

// DeviceDecision 是管理员审批设备的结果。
type DeviceDecision struct {
	Auth       store.DeviceAuth
	Workspaces []string // 申请人在册的工作空间（批准后这台电脑可用于这些）
	Decider    string
}

// DecideDeviceAuth 由管理员批准或拒绝非首台设备。批准人资格在批准这一刻按在册关系重算：
// wsID 非 0（工作空间管理页、本群按钮）时要求申请人在该工作空间在册且操作者能管理它；
// 为 0（服务器管理页）时要求操作者管得到此人。
func (s *Service) DecideDeviceAuth(ctx context.Context, act Actor, wsID, id int64, approve bool) (DeviceDecision, error) {
	actor := act.Account
	var d DeviceDecision
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := store.GetDeviceAuth(ctx, tx, id, true)
		if err != nil {
			return err
		}
		if a.AccountID == nil {
			return ErrDecided
		}
		if wsID != 0 {
			err = canManagePersonIn(ctx, tx, actor, wsID, *a.AccountID)
		} else {
			err = canManagePerson(ctx, tx, actor, *a.AccountID)
		}
		if err != nil {
			return err
		}
		if a.Status != store.AuthAwaitingAdmin || time.Now().After(a.ExpiresAt) {
			return ErrDecided
		}
		applicant, err := store.LockAccount(ctx, tx, *a.AccountID)
		if err != nil {
			return err
		}
		// 审批人写进群里的审批消息：从工作空间管理页批的按那个工作空间的群消息语言，否则用服务器缺省语言
		d.Decider = deciderName(i18n.Pick(wsLanguage(ctx, tx, wsID)), actor)
		a.DecidedBy = actorID(actor)
		if approve && !applicant.Disabled() {
			if err := approveAuth(ctx, tx, &a); err != nil {
				return err
			}
		} else {
			a.Status = store.AuthDenied
		}
		if err := store.UpdateDeviceAuth(ctx, tx, a); err != nil {
			return err
		}
		detail := map[string]any{"owner": applicant.Name, "device_name": a.DeviceName, "tools": a.Tools}
		action := ActDeviceApprove
		if a.Status == store.AuthDenied {
			action = ActDeviceDeny
			if approve {
				detail["requested"], detail["reason"] = "approve", "applicant_disabled"
			}
		}
		if err := record(ctx, tx, act, store.AdminEvent{WorkspaceID: wsp(wsID), Action: action, TargetKind: "device_auth",
			TargetID: idp(a.ID), TargetName: a.DeviceName, Detail: detail}); err != nil {
			return err
		}
		d.Auth = a
		ms, err := store.AccountMemberships(ctx, tx, applicant.ID)
		d.Workspaces = workspaceNames(ms)
		return err
	})
	return d, err
}

// PendingDeviceAuths 列出等待批准的设备申请：wsID 非 0 时为该工作空间管理页（申请人在册），
// 为 0 时为服务器管理页（全部）。
func (s *Service) PendingDeviceAuths(ctx context.Context, actor *store.Account, wsID int64) ([]store.DeviceAuth, error) {
	var err error
	if wsID != 0 {
		err = canAdmin(ctx, s.Pool, actor, wsID)
	} else {
		err = requireServerAdmin(ctx, s.Pool, actor)
	}
	if err != nil {
		return nil, err
	}
	return store.ListAwaitingDeviceAuths(ctx, s.Pool, wsID)
}

// WorkspaceTokens 是一个工作空间的令牌（按工具）。
type WorkspaceTokens struct {
	Workspace string
	Role      string
	Tokens    map[string]string
}

// DeviceDelivery 是交给终端的结果；设备密钥与令牌只交付一次。
type DeviceDelivery struct {
	Status       string
	Account      string
	DeviceID     int64
	DeviceSecret string
	Workspaces   []WorkspaceTokens
}

// PollDeviceAuth 由终端轮询；已批准的在这里生成设备密钥、为申请人每个在册工作空间签发令牌，只交付一次。
// 轮询不需身份，所以先不加锁核对密钥，只有交付或标记过期时才锁行写库。
func (s *Service) PollDeviceAuth(ctx context.Context, id int64, secret string) (DeviceDelivery, error) {
	a, err := store.GetDeviceAuth(ctx, s.Pool, id, false)
	if errors.Is(err, store.ErrNotFound) {
		return DeviceDelivery{}, ErrAuthGone
	}
	if err != nil {
		return DeviceDelivery{}, err
	}
	if subtle.ConstantTimeCompare(a.PollSecretHash, hashSecret("poll", secret)) != 1 {
		return DeviceDelivery{}, ErrAuthGone
	}
	if a.Status != store.AuthApproved {
		return DeviceDelivery{Status: effectiveStatus(a)}, nil
	}
	var out DeviceDelivery
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		a, err := store.GetDeviceAuth(ctx, tx, id, true)
		if err != nil {
			return err
		}
		out.Status = effectiveStatus(a)
		if a.Status != store.AuthApproved || a.AccountID == nil || a.DeviceID == nil {
			return nil
		}
		if out.Status == store.AuthExpired {
			// 已批准但过期未领：吊销批准时建好的设备，不留孤儿设备
			a.Status = store.AuthExpired
			if err := store.RevokeDeviceByID(ctx, tx, *a.DeviceID); err != nil {
				return err
			}
			return store.UpdateDeviceAuth(ctx, tx, a)
		}
		deny := func() error {
			a.Status, out.Status = store.AuthDenied, store.AuthDenied
			return store.UpdateDeviceAuth(ctx, tx, a)
		}
		// 加锁顺序统一为“账号 → 设备”（与停用一致），避免互相等待。
		// 锁设备行并确认未吊销，与吊销互斥：批准后、领取前被吊销（含账号被停用）则不交付
		acc, err := store.LockAccount(ctx, tx, *a.AccountID)
		if err != nil {
			return err
		}
		if acc.Disabled() {
			return deny()
		}
		if err := store.LockActiveDevice(ctx, tx, *a.DeviceID); errors.Is(err, store.ErrDeviceRevoked) {
			return deny()
		} else if err != nil {
			return err
		}
		devSecret, err := randomString(32)
		if err != nil {
			return err
		}
		devSecret = DeviceSecretPrefix + devSecret
		if err := store.SetDeviceSecret(ctx, tx, *a.DeviceID, hashSecret("device", devSecret)); err != nil {
			return err
		}
		ms, err := store.AccountMemberships(ctx, tx, acc.ID)
		if err != nil {
			return err
		}
		out = DeviceDelivery{Status: store.AuthApproved, Account: acc.Name, DeviceID: *a.DeviceID, DeviceSecret: devSecret}
		for _, m := range ms {
			wt, err := issueTokens(ctx, tx, m, *a.DeviceID, a.Tools)
			if err != nil {
				return err
			}
			out.Workspaces = append(out.Workspaces, wt)
		}
		a.Status = store.AuthDelivered
		return store.UpdateDeviceAuth(ctx, tx, a)
	})
	return out, err
}

// issueTokens 在设备上为一个成员资格签发各工具的令牌（调用方已锁住设备行并确认未吊销）。
func issueTokens(ctx context.Context, tx pgx.Tx, m store.Membership, deviceID int64, tools []string) (WorkspaceTokens, error) {
	wt := WorkspaceTokens{Workspace: m.Workspace, Role: m.Role, Tokens: map[string]string{}}
	for _, t := range tools {
		plain, err := store.IssueDeviceToken(ctx, tx, m.MemberID, deviceID, t)
		if err != nil {
			return WorkspaceTokens{}, err
		}
		wt.Tokens[t] = plain
	}
	return wt, nil
}

// AuthenticateDevice 用设备密钥找到设备与账号；设备已吊销或账号已停用时返回 store.ErrUnauthorized。
func (s *Service) AuthenticateDevice(ctx context.Context, secret string) (store.Device, store.Account, error) {
	if !strings.HasPrefix(secret, DeviceSecretPrefix) || len(secret) > 128 {
		return store.Device{}, store.Account{}, store.ErrUnauthorized
	}
	return store.DeviceBySecret(ctx, s.Pool, hashSecret("device", secret))
}

// IssueWorkspaceTokens 用设备密钥为本人在册的某个工作空间签发令牌（新加入工作空间后不必重新配对）。
// 同一设备、同一工作空间、同一工具的旧令牌一并吊销。
func (s *Service) IssueWorkspaceTokens(ctx context.Context, dev store.Device, workspace string, tools []string) (WorkspaceTokens, error) {
	tools, err := cleanTools(tools)
	if err != nil {
		return WorkspaceTokens{}, err
	}
	var out WorkspaceTokens
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		// 加锁顺序：账号 → 设备 → 成员资格（与停用、移出一致）
		acc, err := store.LockAccount(ctx, tx, dev.AccountID)
		if err != nil {
			return err
		}
		if acc.Disabled() {
			return store.ErrUnauthorized
		}
		if err := store.LockActiveDevice(ctx, tx, dev.ID); errors.Is(err, store.ErrDeviceRevoked) {
			return store.ErrUnauthorized
		} else if err != nil {
			return err
		}
		ws, err := store.WorkspaceByName(ctx, tx, workspace)
		if errors.Is(err, store.ErrNotFound) {
			return i18n.Wrap(ErrNotMember, "core.err.workspace_named", i18n.D{"Name": workspace})
		}
		if err != nil {
			return err
		}
		// 锁成员资格行，与移出互斥：移出后不会再签出这个工作空间的令牌
		m, err := store.MemberOf(ctx, tx, acc.ID, ws.ID)
		if errors.Is(err, store.ErrNotFound) {
			return i18n.Wrap(ErrNotMember, "core.err.workspace_named", i18n.D{"Name": workspace})
		}
		if err != nil {
			return err
		}
		if m, err = store.LockMember(ctx, tx, m.ID); err != nil {
			return err
		}
		if m.Status != store.MemberActive {
			return i18n.Wrap(ErrNotMember, "core.err.workspace_named", i18n.D{"Name": workspace})
		}
		out, err = issueTokens(ctx, tx, store.Membership{MemberID: m.ID, WorkspaceID: ws.ID, Workspace: ws.Name, Role: m.Role},
			dev.ID, tools)
		return err
	})
	return out, err
}

// RevokeDevice 吊销设备及其全部令牌：本人可吊销自己的；管理员可吊销他管得到的人的
// （wsID 非 0 时要求设备主人是该工作空间在册成员）。看不到的设备一律报不存在。
func (s *Service) RevokeDevice(ctx context.Context, act Actor, wsID, deviceID int64) error {
	if act.Account == nil {
		return i18n.Errorf("core.err.revoke_needs_actor")
	}
	actor := *act.Account
	return s.inTx(ctx, func(tx pgx.Tx) error {
		d, err := store.GetDevice(ctx, tx, deviceID)
		if err != nil {
			return err
		}
		if d.AccountID != actor.ID {
			if wsID != 0 {
				err = canManagePersonIn(ctx, tx, &actor, wsID, d.AccountID)
			} else {
				err = canManagePerson(ctx, tx, &actor, d.AccountID)
			}
			if errors.Is(err, ErrNotManager) || errors.Is(err, ErrNotAdmin) {
				return i18n.Wrap(store.ErrNotFound, "core.err.device_id", i18n.D{"ID": deviceID})
			}
			if err != nil {
				return err
			}
		}
		if d.RevokedAt != nil {
			return i18n.Wrap(store.ErrNotFound, "core.err.device_id", i18n.D{"ID": deviceID})
		}
		if err := store.LockActiveDevice(ctx, tx, deviceID); err != nil {
			return err
		}
		if err := store.RevokeDeviceByID(ctx, tx, deviceID); err != nil {
			return err
		}
		self := d.AccountID == actor.ID
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: wsp(wsID), Action: ActDeviceRevoke, TargetKind: "device",
			TargetID: idp(d.ID), TargetName: d.Name, Detail: map[string]any{"owner": d.AccountName, "device_name": d.Name, "self": self}})
	})
}

// MyDevices 列出账号自己的设备。
func (s *Service) MyDevices(ctx context.Context, accountID int64) ([]store.Device, error) {
	return store.ListAccountDevices(ctx, s.Pool, accountID)
}

// WorkspaceDevices 由管理员列出工作空间在册成员的设备。
func (s *Service) WorkspaceDevices(ctx context.Context, actor *store.Account, wsID int64) ([]store.Device, error) {
	if err := canAdmin(ctx, s.Pool, actor, wsID); err != nil {
		return nil, err
	}
	return store.ListWorkspaceDevices(ctx, s.Pool, wsID)
}
