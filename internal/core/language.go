package core

import (
	"context"
	"strings"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// 语言设置（方案 docs/superpowers/plans/2026-10-18-i18n-community.md 第 3 节）：
// 账号语言是个人设置，网页、命令行、MCP、会话钩子都按它；工作空间语言决定群通知与群里机器人回复的语言。
// 两者为空都表示跟随服务器缺省语言。

// normLanguage 把输入的语言代码规范成已有语言包的代码；空字符串表示清除。
func normLanguage(code string) (string, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", nil
	}
	l, err := i18n.Parse(code)
	if err != nil {
		return "", inputErrT("core.language.unsupported", i18n.D{"Code": code})
	}
	return string(l), nil
}

// SetMyLanguage 修改本人的账号语言；空字符串表示跟随服务器缺省语言。个人偏好，不记管理操作记录。
func (s *Service) SetMyLanguage(ctx context.Context, acc store.Account, code string) (string, error) {
	lang, err := normLanguage(code)
	if err != nil {
		return "", err
	}
	return lang, store.SetAccountLanguage(ctx, s.Pool, acc.ID, lang)
}

// SetWorkspaceLanguage 修改工作空间的群消息语言，只有工作空间管理员（或服务器命令行）可以改。
func (s *Service) SetWorkspaceLanguage(ctx context.Context, act Actor, wsID int64, code string) error {
	lang, err := normLanguage(code)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		old, err := store.LockWorkspace(ctx, tx, wsID)
		if err != nil {
			return err
		}
		if old.Language == lang {
			return nil
		}
		if err := store.SetWorkspaceLanguage(ctx, tx, wsID, lang); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(wsID), Action: ActWorkspaceSettings,
			TargetKind: "workspace", TargetID: idp(wsID), TargetName: old.Name,
			Detail: map[string]any{"language": map[string]any{"from": old.Language, "to": lang}}})
	})
}
