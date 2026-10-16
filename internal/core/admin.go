package core

import (
	"context"
	"strconv"
	"strings"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// SetRole 由管理员修改成员角色；工作空间至少保留一名管理员。
func (s *Service) SetRole(ctx context.Context, actor *store.Member, memberID int64, role string) error {
	if role != store.RoleAdmin && role != store.RoleMember {
		return inputErr("角色应为管理员或成员")
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		m, err := store.MemberByID(ctx, tx, memberID)
		if err != nil {
			return err
		}
		// 锁住本工作空间的全部管理员行：两名管理员同时互相降级时排队执行，后一个会看到前一个的结果
		if err := store.LockAdmins(ctx, tx, m.WorkspaceID); err != nil {
			return err
		}
		if actor != nil {
			// 按库里当前的角色判断，不用请求开始时读到的（可能已被别人降级）
			fresh, err := store.MemberByID(ctx, tx, actor.ID)
			if err != nil {
				return err
			}
			actor = &fresh
		}
		if err := requireAdmin(actor, m.WorkspaceID); err != nil {
			return err
		}
		if err := store.SetMemberRole(ctx, tx, m.ID, role); err != nil {
			return err
		}
		n, err := store.CountAdmins(ctx, tx, m.WorkspaceID)
		if err != nil {
			return err
		}
		if n == 0 {
			return inputErr("工作空间至少要保留一名管理员")
		}
		return nil
	})
}

// WorkspaceSettings 是管理页可修改的工作空间设置。
type WorkspaceSettings struct {
	PublicURL    string
	TelegramChat string
	ShowTitle    bool
	ShowBrief    bool
}

// UpdateWorkspace 由管理员修改工作空间设置。群编号为空表示关闭群通知。
func (s *Service) UpdateWorkspace(ctx context.Context, actor *store.Member, wsID int64, in WorkspaceSettings) error {
	if err := requireAdmin(actor, wsID); err != nil {
		return err
	}
	pub := strings.TrimRight(strings.TrimSpace(in.PublicURL), "/")
	if pub != "" && !strings.HasPrefix(pub, "https://") && !strings.HasPrefix(pub, "http://") {
		return inputErr("公网地址应以 https:// 或 http:// 开头")
	}
	chat := strings.TrimSpace(in.TelegramChat)
	if chat != "" {
		if _, err := strconv.ParseInt(chat, 10, 64); err != nil {
			return inputErr("Telegram 群编号应为数字，例如 -1001234567890（群里发 /chatid 获取）")
		}
	}
	cfg := store.NotifyConfig{Target: chat, ShowTitle: in.ShowTitle, ShowBrief: in.ShowBrief}
	if chat != "" {
		cfg.Channel = "telegram"
	}
	return store.SetWorkspaceNotify(ctx, s.Pool, wsID, cfg, pub)
}

// Workspace 返回工作空间。
func (s *Service) Workspace(ctx context.Context, id int64) (store.Workspace, error) {
	return store.GetWorkspace(ctx, s.Pool, id)
}

// MemberByTelegram 返回工作空间里连接到该 Telegram 账号的成员（机器人识别点按钮的人）。
func (s *Service) MemberByTelegram(ctx context.Context, wsID, tgUserID int64) (store.Member, error) {
	return store.MemberByTelegramUserID(ctx, s.Pool, wsID, tgUserID)
}
