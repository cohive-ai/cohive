package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"math/big"
	mrand "math/rand/v2"
	"strings"
	"time"
	"unicode"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// DeviceLoginTTL 是设备授权登录请求的有效期；确认后终端还有 DeviceDeliverGrace 的时间领取令牌。
const (
	DeviceLoginTTL     = 10 * time.Minute
	DeviceDeliverGrace = 2 * time.Minute
	// DeviceStartPrefix 是机器人深链参数的前缀：t.me/机器人?start=dl_xxx
	DeviceStartPrefix = "dl_"
)

var (
	// ErrDeviceGone 表示登录请求不存在、已处理或已过期。
	ErrDeviceGone = errors.New("这次登录请求已失效，请在终端重新执行 cohive login")
	// ErrDeviceTaken 表示登录链接已被另一个 Telegram 账号打开过。
	ErrDeviceTaken = errors.New("这个登录链接已被别的 Telegram 账号打开过，本次登录已作废。请在终端重新执行 cohive login")
	// ErrDeviceWrongNumber 表示选的数字和终端显示的不一致。
	ErrDeviceWrongNumber = errors.New("数字和终端显示的不一致，本次登录已作废。请在终端重新执行 cohive login")
)

// DeviceStart 是终端发起登录后拿到的信息。PollSecret 只在这里出现一次。
type DeviceStart struct {
	RequestID   int64
	PollSecret  string
	Nonce       string
	MatchNumber int
	ExpiresIn   time.Duration
}

// DeviceView 是机器人私聊里展示的登录确认内容。
// 需要先选工作空间时 Choices 非空、Numbers 为空；否则 Numbers 是打乱的三个数字，其中一个与终端一致。
type DeviceView struct {
	ID         int64
	DeviceName string
	Tools      []string
	Workspace  string
	MemberName string
	Choices    []store.BoundMember
	Numbers    []int
}

// DevicePoll 是终端轮询的结果。Status 为 approved 时附带各工具的令牌（只交付这一次）。
type DevicePoll struct {
	Status    string
	Member    string
	Workspace string
	Tokens    map[string]string
}

func hashPollSecret(secret string) []byte {
	h := sha256.Sum256([]byte("cohive-device:" + secret))
	return h[:]
}

func randomString(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// cleanDeviceName 去掉控制字符并限制长度；设备名来自终端，只用于展示。
func cleanDeviceName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > 64 {
		s = string(r[:64])
	}
	if s == "" {
		s = "未命名设备"
	}
	return s
}

// StartDeviceLogin 由终端发起一次登录：tools 是要签发令牌的工具，workspace 可为空。
func (s *Service) StartDeviceLogin(ctx context.Context, tools []string, deviceName, workspace string) (DeviceStart, error) {
	var uniq []string
	seen := map[string]bool{}
	for _, t := range tools {
		t = strings.TrimSpace(t)
		if !toolName.MatchString(t) {
			return DeviceStart{}, inputErr("工具名 %q 不合法，例如 claude-code、codex", t)
		}
		if !seen[t] {
			seen[t] = true
			uniq = append(uniq, t)
		}
	}
	if len(uniq) == 0 || len(uniq) > 4 {
		return DeviceStart{}, inputErr("工具数量应为 1 到 4 个")
	}
	workspace = strings.TrimSpace(workspace)
	if len(workspace) > 64 {
		return DeviceStart{}, inputErr("工作空间名太长")
	}
	secret, err := randomString(32)
	if err != nil {
		return DeviceStart{}, err
	}
	nonce, err := randomString(18)
	if err != nil {
		return DeviceStart{}, err
	}
	n, err := rand.Int(rand.Reader, big.NewInt(90))
	if err != nil {
		return DeviceStart{}, err
	}
	d, err := store.CreateDeviceLogin(ctx, s.Pool, store.DeviceLogin{
		PollSecretHash: hashPollSecret(secret),
		StartNonce:     nonce,
		MatchNumber:    10 + int(n.Int64()),
		Tools:          uniq,
		DeviceName:     cleanDeviceName(deviceName),
		WorkspaceName:  workspace,
		ExpiresAt:      time.Now().Add(DeviceLoginTTL),
	})
	if err != nil {
		return DeviceStart{}, err
	}
	return DeviceStart{RequestID: d.ID, PollSecret: secret, Nonce: DeviceStartPrefix + nonce,
		MatchNumber: d.MatchNumber, ExpiresIn: DeviceLoginTTL}, nil
}

// numberChoices 返回打乱的三个两位数，其中一个是 match。
func numberChoices(match int) []int {
	out := []int{match}
	for len(out) < 3 {
		n := 10 + mrand.IntN(90)
		if n != out[0] && (len(out) < 2 || n != out[1]) {
			out = append(out, n)
		}
	}
	mrand.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

// pickBound 在 Telegram 账号绑定的成员里找出本次登录对应的成员；未指定且有多个时返回 nil。
func pickBound(bound []store.BoundMember, workspace string) (*store.BoundMember, error) {
	if workspace != "" {
		for i := range bound {
			if bound[i].WorkspaceName == workspace {
				return &bound[i], nil
			}
		}
		names := make([]string, len(bound))
		for i, b := range bound {
			names[i] = b.WorkspaceName
		}
		return nil, inputErr("你没有加入工作空间 %q，你加入的是：%s", workspace, strings.Join(names, "、"))
	}
	if len(bound) == 1 {
		return &bound[0], nil
	}
	return nil, nil
}

// usable 检查请求是否仍可确认；过期时顺带标记，返回 false。
func usable(d *store.DeviceLogin) bool {
	if d.Status != store.DevicePending {
		return false
	}
	if time.Now().After(d.ExpiresAt) {
		d.Status = store.DeviceExpired
		return false
	}
	return true
}

// deviceStep 锁定请求执行 fn；fn 返回的 result 错误在写回请求之后才返回，事务照常提交（例如“作废并报错”）。
func (s *Service) deviceStep(ctx context.Context, lock func(tx pgx.Tx) (store.DeviceLogin, error),
	fn func(tx pgx.Tx, d *store.DeviceLogin) (result error, err error)) error {
	var result error
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		d, err := lock(tx)
		if errors.Is(err, store.ErrNotFound) {
			result = ErrDeviceGone
			return nil
		}
		if err != nil {
			return err
		}
		if result, err = fn(tx, &d); err != nil {
			return err
		}
		return store.UpdateDeviceLogin(ctx, tx, d)
	})
	if err != nil {
		return err
	}
	return result
}

// claim 检查请求可用且属于这个 Telegram 账号；被别人打开过时作废。
func claim(d *store.DeviceLogin, tgUserID int64) error {
	if !usable(d) {
		return ErrDeviceGone
	}
	if d.TelegramUserID != nil && *d.TelegramUserID != tgUserID {
		d.Status = store.DeviceDenied
		return ErrDeviceTaken
	}
	d.TelegramUserID = &tgUserID
	return nil
}

func deviceView(d store.DeviceLogin, b *store.BoundMember) DeviceView {
	v := DeviceView{ID: d.ID, DeviceName: d.DeviceName, Tools: d.Tools}
	if b != nil {
		v.Workspace, v.MemberName, v.Numbers = b.WorkspaceName, b.Name, numberChoices(d.MatchNumber)
	}
	return v
}

// OpenDeviceLogin 处理机器人私聊里的 /start dl_xxx：认领请求，返回确认内容。
func (s *Service) OpenDeviceLogin(ctx context.Context, startParam string, tgUserID int64) (DeviceView, error) {
	nonce, ok := strings.CutPrefix(startParam, DeviceStartPrefix)
	if !ok || nonce == "" {
		return DeviceView{}, ErrDeviceGone
	}
	var v DeviceView
	err := s.deviceStep(ctx, func(tx pgx.Tx) (store.DeviceLogin, error) {
		return store.DeviceLoginByNonceForUpdate(ctx, tx, nonce)
	}, func(tx pgx.Tx, d *store.DeviceLogin) (error, error) {
		if err := claim(d, tgUserID); err != nil {
			return err, nil
		}
		bound, err := store.MembersByTelegramUserID(ctx, tx, tgUserID)
		if err != nil {
			return nil, err
		}
		if len(bound) == 0 {
			return ErrNotBound, nil
		}
		b, err := pickBound(bound, d.WorkspaceName)
		if err != nil {
			return err, nil
		}
		v = deviceView(*d, b)
		if b == nil {
			v.Choices = bound
		}
		return nil, nil
	})
	return v, err
}

// ChooseDeviceWorkspace 在加入了多个工作空间时选定本次登录的工作空间。
func (s *Service) ChooseDeviceWorkspace(ctx context.Context, id, tgUserID, workspaceID int64) (DeviceView, error) {
	var v DeviceView
	err := s.deviceStep(ctx, func(tx pgx.Tx) (store.DeviceLogin, error) {
		return store.DeviceLoginForUpdate(ctx, tx, id)
	}, func(tx pgx.Tx, d *store.DeviceLogin) (error, error) {
		if d.TelegramUserID == nil || *d.TelegramUserID != tgUserID {
			return ErrDeviceGone, nil
		}
		if !usable(d) {
			return ErrDeviceGone, nil
		}
		bound, err := store.MembersByTelegramUserID(ctx, tx, tgUserID)
		if err != nil {
			return nil, err
		}
		for i := range bound {
			if bound[i].WorkspaceID == workspaceID {
				d.WorkspaceName = bound[i].WorkspaceName
				v = deviceView(*d, &bound[i])
				return nil, nil
			}
		}
		return ErrDeviceGone, nil
	})
	return v, err
}

// ConfirmDeviceLogin 处理数字按钮：选对则批准，选错则作废。
func (s *Service) ConfirmDeviceLogin(ctx context.Context, id, tgUserID int64, number int) (DeviceView, error) {
	var v DeviceView
	err := s.deviceStep(ctx, func(tx pgx.Tx) (store.DeviceLogin, error) {
		return store.DeviceLoginForUpdate(ctx, tx, id)
	}, func(tx pgx.Tx, d *store.DeviceLogin) (error, error) {
		if d.TelegramUserID == nil || *d.TelegramUserID != tgUserID || !usable(d) {
			return ErrDeviceGone, nil
		}
		bound, err := store.MembersByTelegramUserID(ctx, tx, tgUserID)
		if err != nil {
			return nil, err
		}
		b, err := pickBound(bound, d.WorkspaceName)
		if err != nil || b == nil {
			return ErrDeviceGone, nil
		}
		if number != d.MatchNumber {
			d.Status = store.DeviceDenied
			return ErrDeviceWrongNumber, nil
		}
		d.Status, d.MemberID, d.WorkspaceName = store.DeviceApproved, &b.ID, b.WorkspaceName
		v = DeviceView{ID: d.ID, DeviceName: d.DeviceName, Tools: d.Tools, Workspace: b.WorkspaceName, MemberName: b.Name}
		return nil, nil
	})
	return v, err
}

// CancelDeviceLogin 处理取消按钮。
func (s *Service) CancelDeviceLogin(ctx context.Context, id, tgUserID int64) error {
	return s.deviceStep(ctx, func(tx pgx.Tx) (store.DeviceLogin, error) {
		return store.DeviceLoginForUpdate(ctx, tx, id)
	}, func(tx pgx.Tx, d *store.DeviceLogin) (error, error) {
		if d.TelegramUserID == nil || *d.TelegramUserID != tgUserID || !usable(d) {
			return ErrDeviceGone, nil
		}
		d.Status = store.DeviceDenied
		return nil, nil
	})
}

// PollDeviceLogin 由终端轮询登录结果；确认后签发各工具的令牌并只交付一次。
// 编号或轮询密钥不对时返回 ErrDeviceGone。
func (s *Service) PollDeviceLogin(ctx context.Context, id int64, secret string) (DevicePoll, error) {
	var p DevicePoll
	err := s.deviceStep(ctx, func(tx pgx.Tx) (store.DeviceLogin, error) {
		return store.DeviceLoginForUpdate(ctx, tx, id)
	}, func(tx pgx.Tx, d *store.DeviceLogin) (error, error) {
		if subtle.ConstantTimeCompare(d.PollSecretHash, hashPollSecret(secret)) != 1 {
			return ErrDeviceGone, nil
		}
		now := time.Now()
		switch {
		case d.Status == store.DevicePending && now.After(d.ExpiresAt),
			d.Status == store.DeviceApproved && now.After(d.ExpiresAt.Add(DeviceDeliverGrace)):
			d.Status = store.DeviceExpired
		case d.Status == store.DeviceApproved && d.MemberID != nil:
			m, err := store.MemberByID(ctx, tx, *d.MemberID)
			if err != nil {
				return nil, err
			}
			p.Tokens = map[string]string{}
			for _, tool := range d.Tools {
				plain, _, err := store.IssueToken(ctx, tx, m.ID, tool)
				if err != nil {
					return nil, err
				}
				p.Tokens[tool] = plain
			}
			p.Member, p.Workspace = m.Name, d.WorkspaceName
			d.Status = store.DeviceDelivered
			p.Status = store.DeviceApproved
			return nil, nil
		}
		p.Status = d.Status
		return nil, nil
	})
	return p, err
}
