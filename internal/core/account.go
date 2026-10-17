package core

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/bcrypt"
)

// 网页会话
const (
	SessionTTL      = 30 * 24 * time.Hour
	WebLoginCodeTTL = 2 * time.Minute
	// InitialPasswordFile 是初始管理员密码文件名（位于数据目录下）。
	InitialPasswordFile = "initial_admin_password"
)

var (
	// ErrBadLogin 表示登录名或密码不对。不区分是哪一项，避免探测登录名。
	ErrBadLogin = errors.New("登录名或密码不对")
	// ErrSignupPending 表示注册申请还在等管理员批准。
	ErrSignupPending = errors.New("你的注册申请还在等待管理员批准，批准后即可登录")
	// ErrWebLoginCode 表示一次性网页登录码无效。
	ErrWebLoginCode = errors.New("登录链接已失效，请在终端重新执行 cohive web")
)

// 一个固定的哈希，账号不存在时也做一次比较，让响应时间不暴露登录名是否存在。
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("cohive-dummy-password"), bcrypt.DefaultCost)

// Session 是新建的网页会话；ID 只在这里出现一次，写进 Cookie。会话属于账号，不属于某个工作空间。
type Session struct {
	ID      string
	Account store.Account
}

func (s *Service) newSession(ctx context.Context, q store.DBTX, a store.Account, via string) (Session, error) {
	id, err := randomString(32)
	if err != nil {
		return Session{}, err
	}
	if err := store.CreateSession(ctx, q, hashSecret("session", id), a.ID, via, time.Now().Add(SessionTTL)); err != nil {
		return Session{}, err
	}
	return Session{ID: id, Account: a}, nil
}

// Login 用登录名与密码登录网页。登录名全局唯一，不再选工作空间。
// 密码正确但账号已停用时返回 store.ErrAccountDisabled；其余失败一律 ErrBadLogin（或注册待审）。
func (s *Service) Login(ctx context.Context, name, password string) (Session, error) {
	name = strings.TrimSpace(name)
	acc, err := store.AccountByName(ctx, s.Pool, name)
	if errors.Is(err, store.ErrNotFound) {
		s.DummyPasswordCheck(ctx, password)
		// 新人注册申请待审：密码对得上才提示，避免借此探测登录名
		sg, err := store.PendingSignupByName(ctx, s.Pool, name)
		if errors.Is(err, store.ErrNotFound) {
			return Session{}, ErrBadLogin
		}
		if err != nil {
			return Session{}, err
		}
		if match, err := passwordMatches(ctx, sg.PasswordHash, password); err != nil {
			return Session{}, err
		} else if match {
			return Session{}, ErrSignupPending
		}
		return Session{}, ErrBadLogin
	}
	if err != nil {
		return Session{}, err
	}
	pw, err := store.AccountPassword(ctx, s.Pool, acc.ID)
	if err != nil {
		return Session{}, err
	}
	match, err := passwordMatches(ctx, pw.Hash, password)
	if err != nil {
		return Session{}, err
	}
	if pw.Hash == "" {
		s.DummyPasswordCheck(ctx, password)
	}
	if !match {
		return Session{}, ErrBadLogin
	}
	if acc.Disabled() {
		return Session{}, store.ErrAccountDisabled
	}
	return s.newSession(ctx, s.Pool, acc, store.SessionViaPassword)
}

// SessionAccount 返回会话所属账号与会话来源（store.SessionViaPassword 或 SessionViaDevice）。
func (s *Service) SessionAccount(ctx context.Context, sessionID string) (store.Account, string, error) {
	if sessionID == "" || len(sessionID) > 64 {
		return store.Account{}, "", store.ErrUnauthorized
	}
	return store.SessionAccount(ctx, s.Pool, hashSecret("session", sessionID))
}

// Logout 删除会话。
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return store.DeleteSession(ctx, s.Pool, hashSecret("session", sessionID))
}

// SessionForRegistered 为刚注册（个人邀请）的账号建会话。
func (s *Service) SessionForRegistered(ctx context.Context, a store.Account) (Session, error) {
	return s.newSession(ctx, s.Pool, a, store.SessionViaPassword)
}

// ChangePassword 修改自己的密码；没设过密码时不校验旧密码。成功后其他会话失效。
func (s *Service) ChangePassword(ctx context.Context, a store.Account, currentSession, oldPw, newPw string) error {
	pw, err := store.AccountPassword(ctx, s.Pool, a.ID)
	if err != nil {
		return err
	}
	if pw.Hash != "" {
		match, err := passwordMatches(ctx, pw.Hash, oldPw)
		if err != nil {
			return err
		}
		if !match {
			return inputErr("旧密码不对")
		}
	}
	if oldPw != "" && oldPw == newPw {
		return inputErr("新密码不能和旧密码相同")
	}
	hash, err := hashPassword(ctx, newPw)
	if err != nil {
		return err
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := store.SetAccountPassword(ctx, tx, a.ID, hash, false); err != nil {
			return err
		}
		var keep []byte
		if currentSession != "" {
			keep = hashSecret("session", currentSession)
		}
		if err := store.DeleteAccountSessions(ctx, tx, a.ID, keep); err != nil {
			return err
		}
		return record(ctx, tx, WebActor(a), store.AdminEvent{Action: ActPasswordChange, TargetKind: "account",
			TargetID: idp(a.ID), TargetName: a.Name})
	})
	if err == nil && pw.MustChange {
		s.removeInitialPasswordFile(a)
	}
	return err
}

// PasswordStatus 返回账号是否设了密码、是否必须改密码。
func (s *Service) PasswordStatus(ctx context.Context, accountID int64) (store.PasswordInfo, error) {
	return store.AccountPassword(ctx, s.Pool, accountID)
}

// randomPassword 生成易于抄写的随机密码（去掉易混字符），20 位约 100 位熵。
func randomPassword() (string, error) {
	const alphabet = "ABCDEFGHJKMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789"
	buf := make([]byte, 0, 20)
	limit := byte(256 - 256%len(alphabet))
	b := make([]byte, 1)
	for len(buf) < 20 {
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		if b[0] >= limit {
			continue
		}
		buf = append(buf, alphabet[int(b[0])%len(alphabet)])
	}
	return string(buf), nil
}

// EnsureInitialAdmin 在还没有任何未停用的服务器管理员时，建服务器管理员账号 admin（并让它成为 default
// 工作空间的管理员），随机密码写入数据目录下的文件（仅本人可读）。返回文件路径；已有服务器管理员时返回空。
// 密码不写进日志。
func (s *Service) EnsureInitialAdmin(ctx context.Context) (string, error) {
	if s.DataDir == "" {
		return "", errors.New("没有设置数据目录，无法保存初始管理员密码")
	}
	var path string
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `LOCK TABLE accounts IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
		has, err := store.AnyServerAdmin(ctx, tx)
		if err != nil || has {
			return err
		}
		wsID, err := store.DefaultWorkspace(ctx, tx)
		if err != nil {
			return err
		}
		name := "admin"
		for i := 2; ; i++ {
			if _, err := store.AccountByName(ctx, tx, name); errors.Is(err, store.ErrNotFound) {
				if _, err := store.PendingSignupByName(ctx, tx, name); errors.Is(err, store.ErrNotFound) {
					break
				} else if err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
			name = fmt.Sprintf("admin-%d", i)
		}
		pw, err := randomPassword()
		if err != nil {
			return err
		}
		h, err := bcryptHash(ctx, pw)
		if err != nil {
			return err
		}
		acc, err := store.CreateAccount(ctx, tx, name, "管理员")
		if err != nil {
			return err
		}
		if err := store.SetAccountPassword(ctx, tx, acc.ID, h, true); err != nil {
			return err
		}
		if err := store.SetServerAdmin(ctx, tx, acc.ID, true); err != nil {
			return err
		}
		if _, err := store.AddMembership(ctx, tx, acc.ID, wsID, store.RoleAdmin, nil); err != nil {
			return err
		}
		if err := record(ctx, tx, systemActor(), store.AdminEvent{Action: ActAccountCreate, TargetKind: "account",
			TargetID: idp(acc.ID), TargetName: acc.Name, Detail: map[string]any{"initial_admin": true}}); err != nil {
			return err
		}
		if err := os.MkdirAll(s.DataDir, 0o700); err != nil {
			return fmt.Errorf("创建数据目录：%w", err)
		}
		path = filepath.Join(s.DataDir, InitialPasswordFile)
		content := fmt.Sprintf("Cohive 初始管理员\n登录名：%s\n密码：%s\n\n首次登录网页后必须修改密码，修改后本文件会自动删除。\n", name, pw)
		// 先写文件再提交：文件写失败则不建账号，避免出现无人知道密码的管理员
		return os.WriteFile(path, []byte(content), 0o600)
	})
	return path, err
}

// ResetPassword 由服务器管理员在命令行重置账号密码，返回新的随机密码；下次登录必须修改，其他会话全部失效。
func (s *Service) ResetPassword(ctx context.Context, act Actor, name string) (string, error) {
	a, err := store.AccountByName(ctx, s.Pool, name)
	if err != nil {
		return "", err
	}
	pw, err := randomPassword()
	if err != nil {
		return "", err
	}
	h, err := bcryptHash(ctx, pw)
	if err != nil {
		return "", err
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := requireServerAdmin(ctx, tx, act.Account); err != nil {
			return err
		}
		if err := store.SetAccountPassword(ctx, tx, a.ID, h, true); err != nil {
			return err
		}
		if err := store.DeleteAccountSessions(ctx, tx, a.ID, nil); err != nil {
			return err
		}
		// 新密码绝不进记录
		return record(ctx, tx, act, store.AdminEvent{Action: ActResetPassword, TargetKind: "account",
			TargetID: idp(a.ID), TargetName: a.Name})
	})
	if err != nil {
		return "", err
	}
	return pw, nil
}

// CreateWebLoginCode 为已配对电脑的账号换一个一次性网页登录码（cohive web）。
func (s *Service) CreateWebLoginCode(ctx context.Context, accountID int64) (string, error) {
	code, err := randomString(24)
	if err != nil {
		return "", err
	}
	return code, store.CreateWebLoginCode(ctx, s.Pool, hashSecret("weblogin", code), accountID, time.Now().Add(WebLoginCodeTTL))
}

// UseWebLoginCode 使用一次性网页登录码，建免密会话。
func (s *Service) UseWebLoginCode(ctx context.Context, code string) (Session, error) {
	if code == "" || len(code) > 64 {
		return Session{}, ErrWebLoginCode
	}
	var sess Session
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		id, err := store.UseWebLoginCode(ctx, tx, hashSecret("weblogin", code))
		if errors.Is(err, store.ErrNotFound) {
			return ErrWebLoginCode
		}
		if err != nil {
			return err
		}
		a, err := store.AccountByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if a.Disabled() {
			return ErrWebLoginCode
		}
		sess, err = s.newSession(ctx, tx, a, store.SessionViaDevice)
		return err
	})
	return sess, err
}

// Memberships 返回账号在册的全部工作空间。
func (s *Service) Memberships(ctx context.Context, accountID int64) ([]store.Membership, error) {
	return store.AccountMemberships(ctx, s.Pool, accountID)
}

// Account 返回账号。
func (s *Service) Account(ctx context.Context, id int64) (store.Account, error) {
	return store.AccountByID(ctx, s.Pool, id)
}

// removeInitialPasswordFile 在初始管理员本人改过密码后删除初始密码文件；
// 别的账号（例如被重置过密码的）改密码时不动它。
func (s *Service) removeInitialPasswordFile(a store.Account) {
	if s.DataDir == "" {
		return
	}
	path := filepath.Join(s.DataDir, InitialPasswordFile)
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	name := ""
	for _, line := range strings.Split(string(b), "\n") {
		for _, prefix := range []string{"登录名：", "成员名："} { // 成员名：M5 生成的旧文件
			if v, ok := strings.CutPrefix(line, prefix); ok {
				name = strings.TrimSpace(v)
			}
		}
	}
	if name == "" || a.Name != name {
		return
	}
	_ = os.Remove(path)
}
