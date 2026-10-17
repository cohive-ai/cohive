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

// StartLink 由账号在个人中心发起通讯工具连接，返回链接参数。
func (s *Service) StartLink(ctx context.Context, accountID int64, provider string) (store.LinkRequest, error) {
	if provider != ProviderTelegram {
		return store.LinkRequest{}, inputErr("暂只支持连接 telegram")
	}
	nonce, err := randomString(18)
	if err != nil {
		return store.LinkRequest{}, err
	}
	return store.CreateLinkRequest(ctx, s.Pool, store.LinkRequest{AccountID: accountID, Provider: provider,
		Nonce: LinkStartPrefix + nonce, ExpiresAt: time.Now().Add(LinkTTL)})
}

// LinkStatus 返回账号自己的连接请求状态。
func (s *Service) LinkStatus(ctx context.Context, accountID, id int64) (store.LinkRequest, error) {
	l, err := store.GetLinkRequest(ctx, s.Pool, accountID, id)
	if err == nil && l.Status == store.LinkPending && time.Now().After(l.ExpiresAt) {
		l.Status = store.LinkExpired
	}
	return l, err
}

// LinkResult 是机器人完成连接后的结果。
type LinkResult struct {
	Account string
}

// CompleteTelegramLink 处理机器人私聊里的 /start ln_xxx：把这个 Telegram 账号连接到发起的 Cohive 账号。
// 一个 Telegram 账号只能连接一个 Cohive 账号（全服务器）。
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
		a, err := store.LockAccount(ctx, tx, l.AccountID)
		if err != nil {
			return err
		}
		if a.Disabled() {
			result = store.ErrAccountDisabled
			return store.FinishLinkRequest(ctx, tx, l.ID, store.LinkFailed, result.Error())
		}
		res = LinkResult{Account: a.Name}
		if other, err := store.AccountByTelegramUserID(ctx, tx, tgUserID); err == nil && other.ID != a.ID {
			result = inputErr("这个 Telegram 账号已经连接了账号 %s，请先在网页上解除", other.Name)
			return store.FinishLinkRequest(ctx, tx, l.ID, store.LinkFailed, result.Error())
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := store.SetAccountTelegram(ctx, tx, a.ID, &tgUserID, username); err != nil {
			if isUniqueViolation(err) {
				// 同一 Telegram 账号并发连接两个账号时，由唯一索引兜底
				result = inputErr("这个 Telegram 账号已经连接了别的账号，请先在网页上解除")
				return nil
			}
			return err
		}
		detail := fmt.Sprintf("已连接 Telegram 账号 @%s", username)
		if username == "" {
			detail = fmt.Sprintf("已连接 Telegram 账号（编号 %d，没有设置用户名，群通知无法 @ 到你）", tgUserID)
		}
		if err := store.FinishLinkRequest(ctx, tx, l.ID, store.LinkLinked, detail); err != nil {
			return err
		}
		return record(ctx, tx, BotActor(a), store.AdminEvent{Action: ActTelegramLink, TargetKind: "account", TargetID: idp(a.ID),
			TargetName: a.Name, Detail: map[string]any{"telegram_username": username}})
	})
	if err != nil {
		return LinkResult{}, err
	}
	return res, result
}

// UnlinkTelegram 断开账号的 Telegram 连接：本人或服务器管理员可操作。
func (s *Service) UnlinkTelegram(ctx context.Context, act Actor, accountID int64) error {
	return s.Unlink(ctx, act, accountID, ProviderTelegram)
}

// unlinkActions 是各通讯工具“断开连接”的管理操作。
var unlinkActions = map[string]string{ProviderTelegram: ActTelegramUnlink}

// Unlink 断开账号在某种通讯工具上的连接：本人或服务器管理员可操作；没有连接时什么也不做。
func (s *Service) Unlink(ctx context.Context, act Actor, accountID int64, provider string) error {
	action, ok := unlinkActions[provider]
	if !ok {
		return inputErr("不支持的通讯工具 %q", provider)
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		self := act.Account != nil && act.Account.ID == accountID
		if !self {
			if err := requireServerAdmin(ctx, tx, act.Account); err != nil {
				return err
			}
		}
		a, err := store.LockAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if _, err := store.ChatIdentityOf(ctx, tx, accountID, provider); errors.Is(err, store.ErrNotFound) {
			return nil
		} else if err != nil {
			return err
		}
		if err := store.DeleteChatIdentity(ctx, tx, accountID, provider); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{Action: action, TargetKind: "account", TargetID: idp(a.ID),
			TargetName: a.Name, Detail: map[string]any{"self": self}})
	})
}
