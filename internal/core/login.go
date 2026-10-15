package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// LoginCodeTTL 是登录码的有效期。
const LoginCodeTTL = 10 * time.Minute

var (
	// ErrNotBound 表示这个 Telegram 账号还没有绑定任何成员。
	ErrNotBound = errors.New("这个 Telegram 账号还没有绑定 Cohive 成员")
	// ErrBadLoginCode 表示登录码不存在、已使用或已过期。
	ErrBadLoginCode = errors.New("登录码无效、已使用或已过期")
)

// 登录码字母表：去掉 0、O、1、I、L 等易混字符。8 位约 40 位熵。
const loginAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

var toolName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// LoginCodeResult 是签发结果。绑定了多个工作空间且未指定时，Code 为空，Choices 列出可选的工作空间。
type LoginCodeResult struct {
	Code      string
	Member    store.Member
	Workspace store.Workspace
	Choices   []string
}

func normalizeLoginCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func hashLoginCode(normalized string) []byte {
	h := sha256.Sum256([]byte("cohive-login:" + normalized))
	return h[:]
}

func newLoginCode() (string, error) {
	// 丢弃 248 及以上的字节再取余，保证每个字符概率相同
	limit := byte(256 - 256%len(loginAlphabet))
	out := make([]byte, 0, 9)
	buf := make([]byte, 1)
	for n := 0; n < 8; {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		if buf[0] >= limit {
			continue
		}
		if n == 4 {
			out = append(out, '-')
		}
		out = append(out, loginAlphabet[int(buf[0])%len(loginAlphabet)])
		n++
	}
	return string(out), nil
}

// IssueLoginCode 给绑定到该 Telegram 用户编号的成员签发一次性登录码。
// workspace 为空且绑定了多个工作空间时，不签发，返回可选的工作空间。
func (s *Service) IssueLoginCode(ctx context.Context, telegramUserID int64, workspace string) (LoginCodeResult, error) {
	bound, err := store.MembersByTelegramUserID(ctx, s.Pool, telegramUserID)
	if err != nil {
		return LoginCodeResult{}, err
	}
	if len(bound) == 0 {
		return LoginCodeResult{}, ErrNotBound
	}
	names := make([]string, len(bound))
	for i, b := range bound {
		names[i] = b.WorkspaceName
	}
	var pick *store.BoundMember
	switch {
	case workspace != "":
		for i := range bound {
			if bound[i].WorkspaceName == workspace {
				pick = &bound[i]
			}
		}
		if pick == nil {
			return LoginCodeResult{}, inputErr("你没有加入工作空间 %q，可选：%s", workspace, strings.Join(names, "、"))
		}
	case len(bound) == 1:
		pick = &bound[0]
	default:
		return LoginCodeResult{Choices: names}, nil
	}

	code, err := newLoginCode()
	if err != nil {
		return LoginCodeResult{}, err
	}
	// 同一成员同时只保留一个有效码：作废之前没用掉的，再保存新码
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := store.RevokeUnusedLoginCodes(ctx, tx, pick.ID); err != nil {
			return err
		}
		return store.CreateLoginCode(ctx, tx, pick.ID, hashLoginCode(normalizeLoginCode(code)), time.Now().Add(LoginCodeTTL))
	})
	if err != nil {
		return LoginCodeResult{}, err
	}
	ws, err := store.GetWorkspace(ctx, s.Pool, pick.WorkspaceID)
	if err != nil {
		return LoginCodeResult{}, err
	}
	return LoginCodeResult{Code: code, Member: pick.Member, Workspace: ws}, nil
}

// ExchangeLoginCode 用登录码换取成员在某个工具上的令牌。登录码只能用一次。
func (s *Service) ExchangeLoginCode(ctx context.Context, code, tool string) (string, store.Identity, error) {
	if !toolName.MatchString(tool) {
		return "", store.Identity{}, inputErr("工具名 %q 不合法，例如 claude-code、codex", tool)
	}
	normalized := normalizeLoginCode(code)
	if len(normalized) != 8 {
		return "", store.Identity{}, ErrBadLoginCode
	}
	var plain string
	var who store.Identity
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		memberID, err := store.ConsumeLoginCode(ctx, tx, hashLoginCode(normalized))
		if errors.Is(err, store.ErrNotFound) {
			return ErrBadLoginCode
		}
		if err != nil {
			return err
		}
		if _, err := store.MemberByID(ctx, tx, memberID); err != nil {
			return fmt.Errorf("读取成员：%w", err)
		}
		plain, _, err = store.IssueToken(ctx, tx, memberID, tool)
		if err != nil {
			return err
		}
		who, err = store.LookupToken(ctx, tx, plain)
		return err
	})
	if err != nil {
		return "", store.Identity{}, err
	}
	return plain, who, nil
}
