package core

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// 用码绑定的通讯工具（钉钉、Slack……，见 UsesCode）：账号连接（私聊发一次性码 + 网页确认）与群绑定（网页生成绑定码，群里 @机器人 发出）。

const (
	ProviderDingTalk = store.ProviderDingTalk
	// CodeLen 是绑定码长度：8 位、不含容易看错的字符，约 40 比特，10 分钟有效、只能用一次。
	CodeLen  = 8
	codeChar = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

var (
	// ErrLinkCodeGone 表示绑定码不存在、已用过或已过期。
	ErrLinkCodeGone = i18n.Errorf("core.err.link_code_gone")
	// ErrLinkCodeLeaked 表示账号绑定码出现在群里：码可能被别人看到，立即作废。
	ErrLinkCodeLeaked = i18n.Errorf("core.err.link_code_leaked")
	// ErrBindCodeGone 表示群绑定码不存在、已用过或已过期。
	ErrBindCodeGone = i18n.Errorf("core.err.bind_code_gone")
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
	return strings.NewReplacer(" ", "", "-", "", "　", "").Replace(s) // i18n-ignore 全角空格，用户输入的码里可能夹带
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
	Tenant string // 钉钉 corpId；Slack 工作区编号
	UserID string // 钉钉 userId（senderStaffId）；Slack 用户编号
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
				return store.FinishLinkRequest(ctx, tx, l.ID, store.LinkFailed, i18n.T("core.link.detail_leaked"))
			}
			return nil
		}
		if !private {
			result = ErrLinkCodeLeaked
			return store.FinishLinkRequest(ctx, tx, l.ID, store.LinkFailed, i18n.T("core.link.detail_leaked"))
		}
		if who.UserID == "" {
			result = inputErr("core.err.chat_account_unrecognized", i18n.D{"Provider": providerKey(provider)})
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
			result = inputErr("core.err.chat_account_linked", i18n.D{"Provider": providerKey(provider), "Name": other.Name})
			return store.FinishLinkRequest(ctx, tx, l.ID, store.LinkFailed, result.Error())
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		res = LinkSubmit{Account: a.Name}
		return store.SetLinkCandidate(ctx, tx, l.ID, who.Tenant, who.UserID, who.Name,
			// 进展说明存进数据库，用服务器缺省语言写
			i18n.T("core.link.detail_candidate", i18n.D{"Provider": providerNameIn(i18n.Default(), provider), "Name": who.Name}))
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
			return stateErr("core.err.link_request_expired", i18n.D{"Provider": providerKey(l.Provider)})
		}
		if !accept {
			return store.FinishLinkRequest(ctx, tx, l.ID, store.LinkFailed, i18n.T("core.link.detail_cancelled"))
		}
		a, err := store.LockAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if a.Disabled() {
			return store.ErrAccountDisabled
		}
		if other, err := store.AccountByChatIdentity(ctx, tx, l.Provider, l.CandidateTenant, l.CandidateUserID); err == nil && other.ID != a.ID {
			return stateErr("core.err.chat_account_linked", i18n.D{"Provider": providerKey(l.Provider), "Name": other.Name})
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			return err
		}
		err = store.UpsertChatIdentity(ctx, tx, store.ChatIdentity{AccountID: a.ID, Provider: l.Provider, Tenant: l.CandidateTenant,
			UserID: l.CandidateUserID, DisplayName: l.CandidateName})
		if isUniqueViolation(err) {
			return stateErr("core.err.chat_account_linked_elsewhere", i18n.D{"Provider": providerKey(l.Provider)})
		}
		if err != nil {
			return err
		}
		if err := store.FinishLinkRequest(ctx, tx, l.ID, store.LinkLinked,
			i18n.T("core.link.detail_linked", i18n.D{"Provider": providerNameIn(i18n.Default(), l.Provider), "Name": l.CandidateName})); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{Action: ActLinkOf(l.Provider), TargetKind: "account", TargetID: idp(a.ID),
			TargetName: a.Name, Detail: map[string]any{l.Provider + "_name": l.CandidateName}})
	})
}

// StartChatBind 由工作空间管理员生成群绑定码（只限用码绑定的通讯工具；Telegram 群在网页填群编号）。
func (s *Service) StartChatBind(ctx context.Context, act Actor, wsID int64, provider string) (store.ChatBindRequest, error) {
	if !UsesCode(provider) {
		return store.ChatBindRequest{}, inputErr("core.err.bind_by_code_unsupported", i18n.D{"Provider": providerKey(provider)})
	}
	code, err := newCode()
	if err != nil {
		return store.ChatBindRequest{}, err
	}
	var out store.ChatBindRequest
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := writable(ctx, tx, wsID); err != nil {
			return err
		}
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		ws, err := store.GetWorkspace(ctx, tx, wsID)
		if err != nil {
			return err
		}
		if ws.Notify.Channel != "" && ws.Notify.Channel != provider {
			return stateErr("core.err.ws_bound_unbind_rebind", i18n.D{"Provider": providerKey(ws.Notify.Channel), "Target": providerKey(provider)})
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
		// 守卫要先于锁绑定请求：先不加锁地读出是哪个工作空间。已归档就把这条请求标为失败，群里会收到原因
		peek, err := store.ChatBindByCode(ctx, tx, provider, code)
		if errors.Is(err, store.ErrNotFound) {
			result = ErrBindCodeGone
			return nil
		}
		if err != nil {
			return err
		}
		// 后面的 bindChat 要排他锁住工作空间，所以这里一步到位用排他锁，免得两个共享锁持有者互相等着升级
		if _, err := lockWritable(ctx, tx, peek.WorkspaceID); err != nil {
			var ae *ArchivedError
			if errors.As(err, &ae) && peek.Status == store.BindPending {
				result = err
				return store.FinishChatBindRequest(ctx, tx, peek.ID, store.BindFailed, err.Error())
			}
			return err
		}
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
		return ws, stateErr("core.err.ws_bound_unbind", i18n.D{"Provider": providerKey(ws.Notify.Channel)})
	}
	if c, err := store.WorkspaceChatOf(ctx, tx, provider, "", chatID); err == nil && c.WorkspaceID != ws.ID {
		other, _ := store.GetWorkspace(ctx, tx, c.WorkspaceID)
		return ws, stateErr("core.err.chat_bound_other_ws", i18n.D{"Name": other.Label()})
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return ws, err
	}
	err = store.SetWorkspaceChat(ctx, tx, store.WorkspaceChat{WorkspaceID: ws.ID, Provider: provider, ChatID: chatID, Title: title, BoundBy: by})
	if isUniqueViolation(err) {
		return ws, stateErr("core.err.chat_bound_elsewhere")
	}
	if err != nil {
		return ws, err
	}
	return ws, record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(ws.ID), Action: ActWorkspaceChatBind,
		TargetKind: "workspace", TargetID: idp(ws.ID), TargetName: ws.Name,
		Detail: map[string]any{"provider": provider, "chat_title": title}})
}

// BindChat 由工作空间管理员或服务器命令行直接按群编号绑用码绑定的通讯工具的群（群编号在群里 @机器人 发“群编号”获取）。
func (s *Service) BindChat(ctx context.Context, act Actor, wsID int64, provider, chatID string) error {
	chatID = strings.TrimSpace(chatID)
	if !UsesCode(provider) || chatID == "" {
		return inputErr("core.err.chat_id_required")
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockWritable(ctx, tx, wsID); err != nil {
			return err
		}
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
		if _, err := lockWritable(ctx, tx, wsID); err != nil {
			return err
		}
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
