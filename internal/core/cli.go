package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// 只有服务器命令行用的管理操作（网页上没有对应入口）。操作者一般为 ServerCLI()，同样写管理操作记录。

// AccountUpdate 是命令行 account set 要改的项；为空的不改。
type AccountUpdate struct {
	DisplayName      *string
	TelegramUsername *string
	TelegramUserID   *int64
	ClearTelegramID  bool // 解绑 Telegram 用户编号
}

// UpdateAccount 修改账号的显示名、Telegram 用户名或用户编号。
func (s *Service) UpdateAccount(ctx context.Context, act Actor, name string, in AccountUpdate) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := requireServerAdmin(ctx, tx, act.Account); err != nil {
			return err
		}
		a, err := store.AccountByName(ctx, tx, name)
		if errors.Is(err, store.ErrNotFound) {
			return i18n.Wrap(store.ErrNotFound, "core.err.account_not_found", i18n.D{"Name": name})
		}
		if err != nil {
			return err
		}
		if a, err = store.LockAccount(ctx, tx, a.ID); err != nil {
			return err
		}
		changes := map[string]any{}
		if in.DisplayName != nil {
			d := cleanDisplayName(*in.DisplayName)
			if err := store.SetAccountDisplayName(ctx, tx, a.ID, d); err != nil {
				return err
			}
			if d != a.DisplayName {
				changes["display_name"] = map[string]any{"from": a.DisplayName, "to": d}
			}
		}
		if in.TelegramUsername != nil {
			if err := store.SetAccountTelegramUsername(ctx, tx, a.ID, *in.TelegramUsername); err != nil {
				return err
			}
			if *in.TelegramUsername != a.TelegramUsername {
				changes["telegram_username"] = map[string]any{"from": a.TelegramUsername, "to": *in.TelegramUsername}
			}
		}
		if in.TelegramUserID != nil || in.ClearTelegramID {
			to := in.TelegramUserID
			if in.ClearTelegramID {
				to = nil
			}
			if to != nil {
				if other, err := store.AccountByTelegramUserID(ctx, tx, *to); err == nil && other.ID != a.ID {
					return inputErr("core.err.telegram_linked_other_cli", i18n.D{"Name": other.Name})
				} else if err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
			}
			if err := store.SetAccountTelegramUserID(ctx, tx, a.ID, to); err != nil {
				if isUniqueViolation(err) {
					return inputErr("core.err.telegram_linked_elsewhere_short")
				}
				return err
			}
			var from, toV any
			if a.TelegramUserID != nil {
				from = *a.TelegramUserID
			}
			if to != nil {
				toV = *to
			}
			if fmt.Sprint(from) != fmt.Sprint(toV) {
				changes["telegram_user_id"] = map[string]any{"from": from, "to": toV}
			}
		}
		if len(changes) == 0 {
			return nil
		}
		return record(ctx, tx, act, store.AdminEvent{Action: ActAccountUpdate, TargetKind: "account", TargetID: idp(a.ID),
			TargetName: a.Name, Detail: changes})
	})
}

// IssuedToken 是签发的令牌；Plain 只在这里出现一次。
type IssuedToken struct {
	ID     int64
	Member string
	Plain  string
}

// IssueToken 为成员的某个工具签发令牌（不属于任何设备）。
func (s *Service) IssueToken(ctx context.Context, act Actor, wsID int64, memberName, tool string) (IssuedToken, error) {
	tool = strings.TrimSpace(tool)
	var out IssuedToken
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := writable(ctx, tx, wsID); err != nil {
			return err
		}
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		m, err := store.MemberByName(ctx, tx, wsID, memberName)
		if errors.Is(err, store.ErrNotFound) {
			return i18n.Wrap(store.ErrNotFound, "core.err.member_named_missing", i18n.D{"Name": strconv.Quote(memberName)})
		}
		if err != nil {
			return err
		}
		plain, id, err := store.IssueToken(ctx, tx, m.ID, tool)
		if err != nil {
			return err
		}
		out = IssuedToken{ID: id, Member: m.Name, Plain: plain}
		// 令牌本身绝不进记录
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(wsID), Action: ActTokenIssue, TargetKind: "token",
			TargetID: idp(id), TargetName: m.Name, Detail: map[string]any{"member": m.Name, "tool": tool}})
	})
	return out, err
}

// RevokeToken 吊销工作空间里的一枚令牌。
func (s *Service) RevokeToken(ctx context.Context, act Actor, wsID, tokenID int64) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		list, err := store.ListTokens(ctx, tx, wsID)
		if err != nil {
			return err
		}
		var info *store.TokenInfo
		for i := range list {
			if list[i].ID == tokenID {
				info = &list[i]
			}
		}
		if err := store.RevokeTokenInWorkspace(ctx, tx, wsID, tokenID); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(wsID), Action: ActTokenRevoke, TargetKind: "token",
			TargetID: idp(tokenID), TargetName: info.MemberName, Detail: map[string]any{"member": info.MemberName, "tool": info.Tool}})
	})
}

// SetServiceOwner 登记服务负责人（已存在则更新）。
func (s *Service) SetServiceOwner(ctx context.Context, act Actor, wsID int64, service, ownerName string) (store.Member, error) {
	service = strings.TrimSpace(service)
	var m store.Member
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := writable(ctx, tx, wsID); err != nil {
			return err
		}
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		var err error
		m, err = store.MemberByName(ctx, tx, wsID, ownerName)
		if errors.Is(err, store.ErrNotFound) {
			return i18n.Wrap(store.ErrNotFound, "core.err.member_named_missing", i18n.D{"Name": strconv.Quote(ownerName)})
		}
		if err != nil {
			return err
		}
		if m.AccountDisabled {
			return inputErr("core.err.member_disabled_owner", i18n.D{"Name": strconv.Quote(ownerName)})
		}
		list, err := store.ListServices(ctx, tx, wsID)
		if err != nil {
			return err
		}
		from := ""
		for _, sv := range list {
			if sv.Name == service {
				from = sv.OwnerName
			}
		}
		if from == m.Name {
			return nil
		}
		if err := store.UpsertService(ctx, tx, wsID, service, m.ID); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(wsID), Action: ActServiceOwner, TargetKind: "service",
			TargetName: service, Detail: map[string]any{"from": from, "to": m.Name}})
	})
	return m, err
}
