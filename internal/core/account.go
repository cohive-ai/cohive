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
	// ErrBadLogin 表示成员名或密码不对。不区分是哪一项，避免探测成员名。
	ErrBadLogin = errors.New("成员名或密码不对")
	// ErrSignupPending 表示注册申请还在等管理员批准。
	ErrSignupPending = errors.New("你的注册申请还在等待管理员批准，批准后即可登录")
	// ErrWebLoginCode 表示一次性网页登录码无效。
	ErrWebLoginCode = errors.New("登录链接已失效，请在终端重新执行 cohive web")
)

// AmbiguousLoginError 表示同名成员存在于多个工作空间，需要指定工作空间。
type AmbiguousLoginError struct{ Workspaces []string }

func (e *AmbiguousLoginError) Error() string {
	return "这个成员名在多个工作空间里都有，请选择工作空间"
}

// 一个固定的哈希，成员不存在时也做一次比较，让响应时间不暴露成员名是否存在。
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("cohive-dummy-password"), bcrypt.DefaultCost)

// Session 是新建的网页会话；ID 只在这里出现一次，写进 Cookie。
type Session struct {
	ID     string
	Member store.Member
}

func (s *Service) newSession(ctx context.Context, q store.DBTX, m store.Member) (Session, error) {
	id, err := randomString(32)
	if err != nil {
		return Session{}, err
	}
	if err := store.CreateSession(ctx, q, hashSecret("session", id), m.ID, time.Now().Add(SessionTTL)); err != nil {
		return Session{}, err
	}
	return Session{ID: id, Member: m}, nil
}

// Login 用成员名与密码登录网页。workspace 为空且同名成员在多个工作空间时返回 AmbiguousLoginError。
func (s *Service) Login(ctx context.Context, name, password, workspace string) (Session, error) {
	name = strings.TrimSpace(name)
	all, err := store.MembersByName(ctx, s.Pool, name)
	if err != nil {
		return Session{}, err
	}
	var cands []store.Member
	var names []string
	for _, m := range all {
		ws, err := store.GetWorkspace(ctx, s.Pool, m.WorkspaceID)
		if err != nil {
			return Session{}, err
		}
		if workspace == "" || ws.Name == workspace {
			cands = append(cands, m)
			names = append(names, ws.Name)
		}
	}
	// 只在密码对得上的成员里选，避免借“请选择工作空间”探测成员名
	var ok []store.Member
	var okNames []string
	for i, m := range cands {
		pw, err := store.MemberPassword(ctx, s.Pool, m.ID)
		if err != nil {
			return Session{}, err
		}
		match, err := passwordMatches(ctx, pw.Hash, password)
		if err != nil {
			return Session{}, err
		}
		if match {
			ok = append(ok, m)
			okNames = append(okNames, names[i])
		}
	}
	switch len(ok) {
	case 1:
		return s.newSession(ctx, s.Pool, ok[0])
	case 0:
		if len(cands) == 0 {
			s.DummyPasswordCheck(ctx, password)
		}
		pending, err := store.PendingSignupsByName(ctx, s.Pool, name)
		if err != nil {
			return Session{}, err
		}
		for _, sg := range pending {
			if match, err := passwordMatches(ctx, sg.PasswordHash, password); err != nil {
				return Session{}, err
			} else if match {
				return Session{}, ErrSignupPending
			}
		}
		return Session{}, ErrBadLogin
	}
	return Session{}, &AmbiguousLoginError{Workspaces: okNames}
}

// SessionMember 返回会话所属成员。
func (s *Service) SessionMember(ctx context.Context, sessionID string) (store.Member, error) {
	if sessionID == "" || len(sessionID) > 64 {
		return store.Member{}, store.ErrUnauthorized
	}
	return store.SessionMember(ctx, s.Pool, hashSecret("session", sessionID))
}

// Logout 删除会话。
func (s *Service) Logout(ctx context.Context, sessionID string) error {
	return store.DeleteSession(ctx, s.Pool, hashSecret("session", sessionID))
}

// SessionForRegistered 为刚注册（个人邀请）的成员建会话。
func (s *Service) SessionForRegistered(ctx context.Context, m store.Member) (Session, error) {
	return s.newSession(ctx, s.Pool, m)
}

// ChangePassword 修改自己的密码；没设过密码时不校验旧密码。成功后其他会话失效。
func (s *Service) ChangePassword(ctx context.Context, m store.Member, currentSession, oldPw, newPw string) error {
	pw, err := store.MemberPassword(ctx, s.Pool, m.ID)
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
		if err := store.SetMemberPassword(ctx, tx, m.ID, hash, false); err != nil {
			return err
		}
		var keep []byte
		if currentSession != "" {
			keep = hashSecret("session", currentSession)
		}
		return store.DeleteMemberSessions(ctx, tx, m.ID, keep)
	})
	if err == nil && pw.MustChange {
		s.removeInitialPasswordFile(ctx, m)
	}
	return err
}

// PasswordStatus 返回成员是否设了密码、是否必须改密码。
func (s *Service) PasswordStatus(ctx context.Context, memberID int64) (store.PasswordInfo, error) {
	return store.MemberPassword(ctx, s.Pool, memberID)
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

// EnsureInitialAdmin 在还没有任何管理员时，于默认工作空间建管理员 admin，随机密码写入数据目录下的文件（仅本人可读）。
// 返回文件路径；已有管理员时返回空。密码不写进日志。
func (s *Service) EnsureInitialAdmin(ctx context.Context) (string, error) {
	if s.DataDir == "" {
		return "", errors.New("没有设置数据目录，无法保存初始管理员密码")
	}
	var path string
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `LOCK TABLE members IN SHARE ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
		has, err := store.AnyAdmin(ctx, tx)
		if err != nil || has {
			return err
		}
		wsID, err := store.DefaultWorkspace(ctx, tx)
		if err != nil {
			return err
		}
		name := "admin"
		for i := 2; ; i++ {
			if _, err := store.MemberByName(ctx, tx, wsID, name); errors.Is(err, store.ErrNotFound) {
				break
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
		m, err := createAccount(ctx, tx, wsID, name, "管理员", h, store.RoleAdmin)
		if err != nil {
			return err
		}
		if err := store.SetMemberPassword(ctx, tx, m.ID, h, true); err != nil {
			return err
		}
		if err := os.MkdirAll(s.DataDir, 0o700); err != nil {
			return fmt.Errorf("创建数据目录：%w", err)
		}
		path = filepath.Join(s.DataDir, InitialPasswordFile)
		content := fmt.Sprintf("Cohive 初始管理员\n成员名：%s\n密码：%s\n\n首次登录网页后必须修改密码，修改后本文件会自动删除。\n", name, pw)
		// 先写文件再提交：文件写失败则不建账号，避免出现无人知道密码的管理员
		return os.WriteFile(path, []byte(content), 0o600)
	})
	return path, err
}

// ResetPassword 由服务器管理员在命令行重置成员密码，返回新的随机密码；成员下次登录必须修改，其他会话全部失效。
func (s *Service) ResetPassword(ctx context.Context, wsID int64, name string) (string, error) {
	m, err := store.MemberByName(ctx, s.Pool, wsID, name)
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
		if err := store.SetMemberPassword(ctx, tx, m.ID, h, true); err != nil {
			return err
		}
		return store.DeleteMemberSessions(ctx, tx, m.ID, nil)
	})
	return pw, err
}

// CreateWebLoginCode 由已配对设备上的令牌换一个一次性网页登录码（cohive web）。
func (s *Service) CreateWebLoginCode(ctx context.Context, who store.Identity) (string, error) {
	code, err := randomString(24)
	if err != nil {
		return "", err
	}
	return code, store.CreateWebLoginCode(ctx, s.Pool, hashSecret("weblogin", code), who.MemberID, time.Now().Add(WebLoginCodeTTL))
}

// UseWebLoginCode 使用一次性网页登录码，建会话。
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
		m, err := store.MemberByID(ctx, tx, id)
		if err != nil {
			return err
		}
		sess, err = s.newSession(ctx, tx, m)
		return err
	})
	return sess, err
}

// removeInitialPasswordFile 在初始管理员本人改过密码后删除初始密码文件；
// 别的成员（例如被重置过密码的）改密码时不动它。
func (s *Service) removeInitialPasswordFile(ctx context.Context, m store.Member) {
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
		if v, ok := strings.CutPrefix(line, "成员名："); ok {
			name = strings.TrimSpace(v)
		}
	}
	wsID, err := store.DefaultWorkspace(ctx, s.Pool)
	if err != nil || name == "" || m.Name != name || m.WorkspaceID != wsID {
		return
	}
	_ = os.Remove(path)
}
