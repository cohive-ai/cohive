package core

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// 钉钉：账号连接（单聊发一次性码 + 网页确认）与群绑定（网页生成绑定码，群里 @机器人 发出）。

const (
	ProviderDingTalk = store.ProviderDingTalk
	// CodeLen 是绑定码长度：8 位、不含容易看错的字符，约 40 比特，10 分钟有效、只能用一次。
	CodeLen  = 8
	codeChar = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

var (
	// ErrLinkCodeGone 表示绑定码不存在、已用过或已过期。
	ErrLinkCodeGone = errors.New("这个绑定码已失效，请回到 Cohive 网页“个人中心”重新点“连接钉钉”")
	// ErrLinkCodeLeaked 表示账号绑定码出现在群里：码可能被别人看到，立即作废。
	ErrLinkCodeLeaked = errors.New("绑定码要私聊发给机器人。这个码出现在了群里，已作废，请回网页重新生成")
	// ErrBindCodeGone 表示群绑定码不存在、已用过或已过期。
	ErrBindCodeGone = errors.New("这个群绑定码已失效，请回到 Cohive 网页“管理 → 工作空间”重新生成")
)

// newCode 生成一个绑定码。
func newCode() (string, error) {
	b := make([]byte, CodeLen)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeChar))))
		if err != nil {
			return "", err
		}
		b[i] = codeChar[n.Int64()]
	}
	return string(b), nil
}

// NormalizeCode 把用户发来的码规范化：去空白与连字符、转大写。
func NormalizeCode(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.NewReplacer(" ", "", "-", "", "　", "").Replace(s)
}

// LooksLikeCode 判断一段文字是不是绑定码的样子（用于识别单聊里直接发来的码、群里误发的码）。
func LooksLikeCode(s string) bool {
	s = NormalizeCode(s)
	if len(s) != CodeLen {
		return false
	}
	for _, r := range s {
		if !strings.ContainsRune(codeChar, r) {
			return false
		}
	}
	return true
}

// ChatSender 是通讯工具平台填写的发消息的人。
type ChatSender struct {
	Tenant string // 钉钉 corpId
	UserID string // 钉钉 userId（senderStaffId）
	Name   string // 昵称
}

// LinkSubmit 是机器人收到账号绑定码后的结果。
type LinkSubmit struct {
	Account string // 待确认的 Cohive 账号
}

// SubmitLinkCode 处理机器人收到的账号绑定码：记下发码的人，等本人在网页确认。private 为假（码出现在群里）时作废这个码。
func (s *Service) SubmitLinkCode(ctx context.Context, provider, code string, who ChatSender, private bool) (LinkSubmit, error) {
	code = NormalizeCode(code)
	var res LinkSubmit
	var result error
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		l, err := store.LinkRequestByNonceForUpdate(ctx, tx, provider, code)
		if errors.Is(err, store.ErrNotFound) {
			result = ErrLinkCodeGone
			return nil
		}
		if err != nil {
			return err
		}
		if l.Status != store.LinkPending || time.Now().After(l.ExpiresAt) {
			result = ErrLinkCodeGone
			if !private && l.Status == store.LinkAwaitingConfirm { // 已收到过、等确认的码又出现在群里：一样作废
				result = ErrLinkCodeLeaked
				return store.FinishLinkRequest(ctx, tx, l.ID, store.LinkFailed, "绑定码出现在了群里，已作废")
			}
			return nil
		}
		if !private {
			result = ErrLinkCodeLeaked
			return store.FinishLinkRequest(ctx, tx, l.ID, store.LinkFailed, "绑定码出现在了群里，已作废")
		}
		if who.UserID == "" {
			result = inputErr("没能识别你的%s账号，请稍后再试", providerName(provider))
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
		if other, err := store.AccountByChatIdentity(ctx, tx, provider, who.Tenant, who.UserID); err == nil && other.ID != a.ID {
			result = inputErr("这个%s账号已经连接了账号 %s，请先在网页上解除", providerName(provider), other.Name)
			return store.FinishLinkRequest(ctx, tx, l.ID, store.LinkFailed, result.Error())
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		res = LinkSubmit{Account: a.Name}
		return store.SetLinkCandidate(ctx, tx, l.ID, who.Tenant, who.UserID, who.Name,
			fmt.Sprintf("收到%s用户 %s 发来的绑定码，等你确认", providerName(provider), who.Name))
	})
	if err != nil {
		return LinkSubmit{}, err
	}
	return res, result
}

// ConfirmLink 由本人在网页确认（accept）或取消待确认的连接；确认后才写入通讯账号。
func (s *Service) ConfirmLink(ctx context.Context, act Actor, id int64, accept bool) error {
	if act.Account == nil {
		return ErrNotAdmin
	}
	accountID := act.Account.ID
	return s.inTx(ctx, func(tx pgx.Tx) error {
		l, err := store.LinkRequestForUpdate(ctx, tx, accountID, id)
		if err != nil {
			return err
		}
		if l.Status != store.LinkAwaitingConfirm || time.Now().After(l.ExpiresAt) {
			return stateErr("这个连接请求已失效，请重新点“连接%s”", strings.TrimSpace(providerName(l.Provider)))
		}
		if !accept {
			return store.FinishLinkRequest(ctx, tx, l.ID, store.LinkFailed, "已取消")
		}
		a, err := store.LockAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if a.Disabled() {
			return store.ErrAccountDisabled
		}
		if other, err := store.AccountByChatIdentity(ctx, tx, l.Provider, l.CandidateTenant, l.CandidateUserID); err == nil && other.ID != a.ID {
			return stateErr("这个%s账号已经连接了账号 %s，请先在网页上解除", providerName(l.Provider), other.Name)
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		err = store.UpsertChatIdentity(ctx, tx, store.ChatIdentity{AccountID: a.ID, Provider: l.Provider, Tenant: l.CandidateTenant,
			UserID: l.CandidateUserID, DisplayName: l.CandidateName})
		if isUniqueViolation(err) {
			return stateErr("这个%s账号已经连接了别的账号，请先在网页上解除", providerName(l.Provider))
		}
		if err != nil {
			return err
		}
		if err := store.FinishLinkRequest(ctx, tx, l.ID, store.LinkLinked,
			fmt.Sprintf("已连接%s用户 %s", providerName(l.Provider), l.CandidateName)); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{Action: ActDingTalkLink, TargetKind: "account", TargetID: idp(a.ID),
			TargetName: a.Name, Detail: map[string]any{"dingtalk_name": l.CandidateName}})
	})
}

// StartChatBind 由工作空间管理员生成群绑定码（第一期只有钉钉用绑定码绑群）。
func (s *Service) StartChatBind(ctx context.Context, act Actor, wsID int64, provider string) (store.ChatBindRequest, error) {
	if provider != ProviderDingTalk {
		return store.ChatBindRequest{}, inputErr("暂只支持用绑定码绑定钉钉群")
	}
	code, err := newCode()
	if err != nil {
		return store.ChatBindRequest{}, err
	}
	var out store.ChatBindRequest
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		ws, err := store.GetWorkspace(ctx, tx, wsID)
		if err != nil {
			return err
		}
		if ws.Notify.Channel != "" && ws.Notify.Channel != provider {
			return stateErr("工作空间已绑定%s群，请先解绑再绑%s群", providerName(ws.Notify.Channel), strings.TrimSpace(providerName(provider)))
		}
		by := int64(0)
		if act.Account != nil {
			by = act.Account.ID
		}
		out, err = store.CreateChatBindRequest(ctx, tx, store.ChatBindRequest{WorkspaceID: wsID, Provider: provider, CreatedBy: by,
			Code: code, ExpiresAt: time.Now().Add(LinkTTL)})
		return err
	})
	return out, err
}

// ChatBindStatus 返回群绑定请求的状态（过期的显示为 expired）。
func (s *Service) ChatBindStatus(ctx context.Context, act Actor, wsID, id int64) (store.ChatBindRequest, error) {
	if err := canAdmin(ctx, s.Pool, act.Account, wsID); err != nil {
		return store.ChatBindRequest{}, err
	}
	r, err := store.GetChatBindRequest(ctx, s.Pool, wsID, id)
	if err == nil && r.Status == store.BindPending && time.Now().After(r.ExpiresAt) {
		r.Status = store.LinkExpired
	}
	return r, err
}

// CompleteChatBind 处理群里 @机器人 发来的群绑定码：把这个群绑到生成码的工作空间。返回工作空间的显示名。
// 群编号由通讯工具平台填写；群编号全局唯一，工作空间群表里不记企业。
func (s *Service) CompleteChatBind(ctx context.Context, provider, chatID, title, code string) (string, error) {
	code = NormalizeCode(code)
	var label string
	var result error
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		r, err := store.ChatBindByCodeForUpdate(ctx, tx, provider, code)
		if errors.Is(err, store.ErrNotFound) {
			result = ErrBindCodeGone
			return nil
		}
		if err != nil {
			return err
		}
		if r.Status != store.BindPending || time.Now().After(r.ExpiresAt) {
			result = ErrBindCodeGone
			return nil
		}
		fail := func(e error) error {
			result = e
			return store.FinishChatBindRequest(ctx, tx, r.ID, store.BindFailed, e.Error())
		}
		creator, err := store.AccountByID(ctx, tx, r.CreatedBy)
		if err != nil {
			return err
		}
		ws, err := bindChat(ctx, tx, ChatActor(creator, provider), r.WorkspaceID, provider, chatID, title, &creator.ID)
		var se *StateError
		if errors.As(err, &se) {
			return fail(err)
		}
		if err != nil {
			return err
		}
		label = ws.Label()
		return store.FinishChatBindRequest(ctx, tx, r.ID, store.BindBound, title)
	})
	if err != nil {
		return "", err
	}
	return label, result
}

// bindChat 把群绑到工作空间并写记录：第一期一个工作空间只绑一个群，一个群只能绑一个工作空间（违反时返回 StateError）。
func bindChat(ctx context.Context, tx pgx.Tx, act Actor, wsID int64, provider, chatID, title string, by *int64) (store.Workspace, error) {
	ws, err := store.LockWorkspace(ctx, tx, wsID)
	if err != nil {
		return ws, err
	}
	if ws.Notify.Channel != "" && ws.Notify.Channel != provider {
		return ws, stateErr("工作空间已绑定%s群，请先解绑", providerName(ws.Notify.Channel))
	}
	if c, err := store.WorkspaceChatOf(ctx, tx, provider, "", chatID); err == nil && c.WorkspaceID != ws.ID {
		other, _ := store.GetWorkspace(ctx, tx, c.WorkspaceID)
		return ws, stateErr("这个群已经绑定了工作空间 %s：一个群只能对应一个工作空间", other.Label())
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return ws, err
	}
	err = store.SetWorkspaceChat(ctx, tx, store.WorkspaceChat{WorkspaceID: ws.ID, Provider: provider, ChatID: chatID, Title: title, BoundBy: by})
	if isUniqueViolation(err) {
		return ws, stateErr("这个群已经绑定了别的工作空间：一个群只能对应一个工作空间")
	}
	if err != nil {
		return ws, err
	}
	return ws, record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(ws.ID), Action: ActWorkspaceChatBind,
		TargetKind: "workspace", TargetID: idp(ws.ID), TargetName: ws.Name,
		Detail: map[string]any{"provider": provider, "chat_title": title}})
}

// BindChat 由工作空间管理员或服务器命令行直接按群编号绑群（钉钉群编号在群里 @机器人 发“群编号”获取）。
func (s *Service) BindChat(ctx context.Context, act Actor, wsID int64, provider, chatID string) error {
	chatID = strings.TrimSpace(chatID)
	if provider != ProviderDingTalk || chatID == "" {
		return inputErr("请给出钉钉群编号（在群里 @机器人 发“群编号”获取）")
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		var by *int64
		if act.Account != nil {
			by = &act.Account.ID
		}
		_, err := bindChat(ctx, tx, act, wsID, provider, chatID, "", by)
		return err
	})
}

// UnbindChat 由工作空间管理员解除工作空间绑定的某种通讯工具的群。
func (s *Service) UnbindChat(ctx context.Context, act Actor, wsID int64, provider string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		ws, err := store.LockWorkspace(ctx, tx, wsID)
		if err != nil {
			return err
		}
		chats, err := store.WorkspaceChats(ctx, tx, wsID)
		if err != nil {
			return err
		}
		var title string
		found := false
		for _, c := range chats {
			if c.Provider == provider {
				found, title = true, c.Title
			}
		}
		if !found {
			return nil
		}
		if err := store.DeleteWorkspaceChats(ctx, tx, wsID, provider); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(wsID), Action: ActWorkspaceChatUnbind,
			TargetKind: "workspace", TargetID: idp(wsID), TargetName: ws.Name,
			Detail: map[string]any{"provider": provider, "chat_title": title}})
	})
}
