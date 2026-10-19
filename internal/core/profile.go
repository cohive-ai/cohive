package core

import (
	"context"
	"unicode"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// CodeDisplayNameTaken 是新昵称与别人撞名时的稳定错误码。
const CodeDisplayNameTaken = "display_name_taken"

// DisplayNameTakenError 表示新昵称与本人所在某个工作空间里其他在册成员的登录名或昵称相同（不区分大小写）。
// 只说撞的是登录名还是昵称，不说是谁。
type DisplayNameTakenError struct {
	Clash string // store.ClashName 或 store.ClashDisplayName
}

func (e *DisplayNameTakenError) Error() string { return e.Localize(i18n.Default()) }

func (e *DisplayNameTakenError) Localize(l i18n.Lang) string {
	if e.Clash == store.ClashName {
		return l.T("core.err.display_name_taken_name")
	}
	return l.T("core.err.display_name_taken_display")
}

// SetMyDisplayName 由本人修改自己的昵称，返回清理后的昵称。昵称按 cleanDisplayName 清理，可以清空（各处回退显示登录名）；
// 不能含控制字符或不可见的格式字符；非空时不能与本人所在任一工作空间里其他在册成员的登录名或昵称相同，
// 防止冒充别人出现在群通知、审批消息和按名字找人的结果里。改了才记管理操作记录，与管理员改昵称是同一种记录。
func (s *Service) SetMyDisplayName(ctx context.Context, act Actor, display string) (string, error) {
	d := cleanDisplayName(display)
	for _, r := range d {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return "", inputErr("core.err.display_name_invisible")
		}
	}
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := store.LockDisplayNames(ctx, tx); err != nil {
			return err
		}
		a, err := store.LockAccount(ctx, tx, act.Account.ID)
		if err != nil {
			return err
		}
		if a.Disabled() {
			return store.ErrAccountDisabled
		}
		if d == a.DisplayName {
			return nil
		}
		if d != "" {
			clash, err := store.DisplayNameClash(ctx, tx, a.ID, d)
			if err != nil {
				return err
			}
			if clash != "" {
				return &DisplayNameTakenError{Clash: clash}
			}
		}
		if err := store.SetAccountDisplayName(ctx, tx, a.ID, d); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{Action: ActAccountUpdate, TargetKind: "account", TargetID: idp(a.ID),
			TargetName: a.Name, Detail: map[string]any{"self": true, "display_name": map[string]any{"from": a.DisplayName, "to": d}}})
	})
	if err != nil {
		return "", err
	}
	return d, nil
}
