package core

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrNoWorkspaceForChat 表示这个群没有配置成任何工作空间的群。
	ErrNoWorkspaceForChat = errors.New("这个群还没有对应的 Cohive 工作空间")
	// ErrNotAdmin 表示操作者不是该工作空间的管理员。
	ErrNotAdmin = errors.New("只有工作空间管理员能审批")
	// ErrJoinDecided 表示申请已经处理过。
	ErrJoinDecided = errors.New("这条申请已经处理过了")
)

// JoinResult 是群里 /join 的结果：已是成员、直接加入、或建了待审申请，三者只有一个非空。
type JoinResult struct {
	Workspace     store.Workspace
	AlreadyMember *store.Member
	Member        *store.Member
	Request       *store.JoinRequest
}

// JoinDecision 是管理员审批的结果；批准时 Member 为新成员。
type JoinDecision struct {
	Workspace store.Workspace
	Request   store.JoinRequest
	Member    *store.Member
	Decider   store.Member
}

var nameUnsafe = regexp.MustCompile(`[^a-z0-9_-]`)

// baseMemberName 用 Telegram 用户名（小写）做成员名；没有用户名时用 tg 加编号。
func baseMemberName(username string, tgUserID int64) string {
	name := nameUnsafe.ReplaceAllString(strings.ToLower(username), "")
	if name == "" {
		name = "tg" + strconv.FormatInt(tgUserID, 10)
	}
	return name
}

// createJoinedMember 建成员并绑定 Telegram 编号与用户名；成员名冲突时加 -2、-3 后缀。
func createJoinedMember(ctx context.Context, tx pgx.Tx, wsID, tgUserID int64, username, display string) (store.Member, error) {
	base := baseMemberName(username, tgUserID)
	name := base
	for i := 2; ; i++ {
		if _, err := store.MemberByName(ctx, tx, wsID, name); errors.Is(err, store.ErrNotFound) {
			break
		} else if err != nil {
			return store.Member{}, err
		}
		if i > 50 {
			return store.Member{}, fmt.Errorf("成员名 %s 冲突太多", base)
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
	m, err := store.CreateMember(ctx, tx, wsID, name, display)
	if err != nil {
		return store.Member{}, err
	}
	if err := store.SetMemberTelegramUserID(ctx, tx, m.ID, &tgUserID); err != nil {
		return store.Member{}, err
	}
	if username != "" {
		if err := store.SetMemberTelegram(ctx, tx, m.ID, username); err != nil {
			return store.Member{}, err
		}
	}
	return store.MemberByID(ctx, tx, m.ID)
}

// RequestJoin 处理群里的 /join。
func (s *Service) RequestJoin(ctx context.Context, chatID string, tgUserID int64, username, display string) (JoinResult, error) {
	ws, err := store.WorkspaceByTelegramChat(ctx, s.Pool, chatID)
	if errors.Is(err, store.ErrNotFound) {
		return JoinResult{}, ErrNoWorkspaceForChat
	}
	if err != nil {
		return JoinResult{}, err
	}
	res := JoinResult{Workspace: ws}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if m, err := store.MemberByTelegramUserID(ctx, tx, ws.ID, tgUserID); err == nil {
			res.AlreadyMember = &m
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if !ws.JoinRequiresApproval {
			m, err := createJoinedMember(ctx, tx, ws.ID, tgUserID, username, display)
			if err != nil {
				return err
			}
			res.Member = &m
			return nil
		}
		if j, err := store.PendingJoinRequest(ctx, tx, ws.ID, tgUserID); err == nil {
			res.Request = &j
			return nil
		} else if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		j, err := store.CreateJoinRequest(ctx, tx, ws.ID, tgUserID, username, display)
		if err != nil {
			return err
		}
		res.Request = &j
		return nil
	})
	return res, err
}

// DecideJoin 由管理员批准或拒绝加入申请。deciderTgUserID 是点按钮的人的 Telegram 编号。
func (s *Service) DecideJoin(ctx context.Context, requestID, deciderTgUserID int64, approve bool) (JoinDecision, error) {
	return s.decideJoin(ctx, requestID, approve, func(tx pgx.Tx, j store.JoinRequest) (*store.Member, error) {
		decider, err := store.MemberByTelegramUserID(ctx, tx, j.WorkspaceID, deciderTgUserID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !decider.IsAdmin()) {
			return nil, ErrNotAdmin
		}
		if err != nil {
			return nil, err
		}
		return &decider, nil
	})
}

// DecideJoinByOperator 由服务器管理员在命令行处理申请（机器人不可用时的备用），不做成员身份校验。
func (s *Service) DecideJoinByOperator(ctx context.Context, requestID int64, approve bool) (JoinDecision, error) {
	return s.decideJoin(ctx, requestID, approve, func(pgx.Tx, store.JoinRequest) (*store.Member, error) {
		return nil, nil
	})
}

// decideJoin 是审批的公共流程；authorize 返回审批人（命令行时为 nil）或拒绝原因。
func (s *Service) decideJoin(ctx context.Context, requestID int64, approve bool,
	authorize func(tx pgx.Tx, j store.JoinRequest) (*store.Member, error)) (JoinDecision, error) {
	var d JoinDecision
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		j, err := store.GetJoinRequestForUpdate(ctx, tx, requestID)
		if err != nil {
			return err
		}
		decider, err := authorize(tx, j)
		if err != nil {
			return err
		}
		if j.Status != "pending" {
			return ErrJoinDecided
		}
		status := "rejected"
		if approve {
			status = "approved"
			// 申请期间可能已被管理员手工加进来
			if m, err := store.MemberByTelegramUserID(ctx, tx, j.WorkspaceID, j.TelegramUserID); err == nil {
				d.Member = &m
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			} else {
				m, err := createJoinedMember(ctx, tx, j.WorkspaceID, j.TelegramUserID, j.TelegramUsername, j.DisplayName)
				if err != nil {
					return err
				}
				d.Member = &m
			}
		}
		var deciderID *int64
		if decider != nil {
			deciderID = &decider.ID
			d.Decider = *decider
		} else {
			d.Decider = store.Member{Name: "服务器管理员"}
		}
		if err := store.DecideJoinRequest(ctx, tx, j.ID, status, deciderID); err != nil {
			return err
		}
		j.Status = status
		d.Request = j
		d.Workspace, err = store.GetWorkspace(ctx, tx, j.WorkspaceID)
		return err
	})
	return d, err
}
