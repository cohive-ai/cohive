package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// 通讯工具连接
const (
	ProviderTelegram = "telegram"
	LinkTTL          = 10 * time.Minute
	// LinkStartPrefix 是 Telegram 机器人链接参数的前缀：t.me/机器人?start=ln_xxx
	LinkStartPrefix = "ln_"
)

// ErrLinkGone 表示连接链接不存在、已使用或已过期。
var ErrLinkGone = errors.New("这个连接链接已失效，请回到 Cohive 网页“个人中心”重新点“连接 Telegram”")

// StartLink 由成员在终端发起通讯工具连接，返回链接参数。
func (s *Service) StartLink(ctx context.Context, who store.Identity, provider string) (store.LinkRequest, error) {
	if provider != ProviderTelegram {
		return store.LinkRequest{}, inputErr("暂只支持连接 telegram")
	}
	nonce, err := randomString(18)
	if err != nil {
		return store.LinkRequest{}, err
	}
	return store.CreateLinkRequest(ctx, s.Pool, store.LinkRequest{MemberID: who.MemberID, Provider: provider,
		Nonce: LinkStartPrefix + nonce, ExpiresAt: time.Now().Add(LinkTTL)})
}

// LinkStatus 返回成员自己的连接请求状态。
func (s *Service) LinkStatus(ctx context.Context, who store.Identity, id int64) (store.LinkRequest, error) {
	l, err := store.GetLinkRequest(ctx, s.Pool, who.MemberID, id)
	if err == nil && l.Status == store.LinkPending && time.Now().After(l.ExpiresAt) {
		l.Status = store.LinkExpired
	}
	return l, err
}

// LinkResult 是机器人完成连接后的结果。
type LinkResult struct {
	Member    string
	Workspace string
}

// CompleteTelegramLink 处理机器人私聊里的 /start ln_xxx：把这个 Telegram 账号连接到发起的成员。
// 同一工作空间里一个 Telegram 账号只能连接一个成员。
func (s *Service) CompleteTelegramLink(ctx context.Context, startParam string, tgUserID int64, username string) (LinkResult, error) {
	if !strings.HasPrefix(startParam, LinkStartPrefix) {
		return LinkResult{}, ErrLinkGone
	}
	var res LinkResult
	var result error
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		l, err := store.LinkRequestByNonceForUpdate(ctx, tx, ProviderTelegram, startParam)
		if errors.Is(err, store.ErrNotFound) {
			result = ErrLinkGone
			return nil
		}
		if err != nil {
			return err
		}
		if l.Status != store.LinkPending || time.Now().After(l.ExpiresAt) {
			result = ErrLinkGone
			return nil
		}
		m, err := store.MemberByID(ctx, tx, l.MemberID)
		if err != nil {
			return err
		}
		ws, err := store.GetWorkspace(ctx, tx, m.WorkspaceID)
		if err != nil {
			return err
		}
		res = LinkResult{Member: m.Name, Workspace: ws.Name}
		if other, err := store.MemberByTelegramUserID(ctx, tx, m.WorkspaceID, tgUserID); err == nil && other.ID != m.ID {
			result = inputErr("这个 Telegram 账号已经连接了工作空间 %s 的成员 %s，请先在网页上解除", ws.Name, other.Name)
			return store.FinishLinkRequest(ctx, tx, l.ID, store.LinkFailed, result.Error())
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := store.SetMemberTelegramUserID(ctx, tx, m.ID, &tgUserID); err != nil {
			return err
		}
		if err := store.SetMemberTelegram(ctx, tx, m.ID, username); err != nil {
			return err
		}
		detail := fmt.Sprintf("已连接 Telegram 账号 @%s", username)
		if username == "" {
			detail = fmt.Sprintf("已连接 Telegram 账号（编号 %d，没有设置用户名，群通知无法 @ 到你）", tgUserID)
		}
		return store.FinishLinkRequest(ctx, tx, l.ID, store.LinkLinked, detail)
	})
	if err != nil {
		return LinkResult{}, err
	}
	return res, result
}

// UnlinkTelegram 解除成员的 Telegram 连接：成员自己或本工作空间管理员可操作。
func (s *Service) UnlinkTelegram(ctx context.Context, actor store.Member, memberID int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		m, err := store.MemberByID(ctx, tx, memberID)
		if err != nil {
			return err
		}
		if actor.ID != m.ID && !(actor.IsAdmin() && actor.WorkspaceID == m.WorkspaceID) {
			return ErrNotAdmin
		}
		if err := store.SetMemberTelegramUserID(ctx, tx, m.ID, nil); err != nil {
			return err
		}
		return store.SetMemberTelegram(ctx, tx, m.ID, "")
	})
}
