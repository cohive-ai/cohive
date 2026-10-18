package notify

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// Core 是机器人用到的业务操作，由 core.Service 实现。
type Core interface {
	CompleteTelegramLink(ctx context.Context, startParam string, tgUserID int64, username string) (core.LinkResult, error)
	DecideSignup(ctx context.Context, act core.Actor, wsID, id int64, approve bool) (core.SignupDecision, error)
	DecideDeviceAuth(ctx context.Context, act core.Actor, wsID, id int64, approve bool) (core.DeviceDecision, error)
	Workspace(ctx context.Context, id int64) (store.Workspace, error)
	// WorkspaceByChat 返回绑定了该群的工作空间（一个群只绑一个工作空间）。
	WorkspaceByChat(ctx context.Context, provider, tenant, chatID string) (store.Workspace, error)
	// AccountByChat 返回连接到该通讯账号的 Cohive 账号。
	AccountByChat(ctx context.Context, provider, tenant, userID string) (store.Account, error)
	ChatIdentities(ctx context.Context, accountIDs []int64) (map[int64][]store.ChatIdentity, error)
	Memberships(ctx context.Context, accountID int64) ([]store.Membership, error)
	RecordBotPost(ctx context.Context, p store.BotPost) error
	BotPosts(ctx context.Context, kind string, refID int64) ([]store.BotPost, error)
	BotPostByMessage(ctx context.Context, provider, chatID, msgRef string) (store.BotPost, error)
	RecordDenied(ctx context.Context, act core.Actor, d core.DeniedAttempt) error
}

// Hub 是与通讯工具无关的机器人逻辑：把注册、加入、设备申请发到工作空间绑定的群里，处理群里的审批按钮，
// 在任一处处理后把各群里的审批消息都改成结果。实现 httpapi.Announcer。
type Hub struct {
	Core     Core
	Channels map[string]Channel // 按通讯工具
	Logf     func(format string, args ...any)
}

func (h *Hub) logf(format string, args ...any) {
	if h.Logf != nil {
		h.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// 机器人在群里的话用群所属工作空间的语言，发给某个人的私聊用他账号的语言；查不到（群没绑定、账号没连接）时
// 用服务器缺省语言。

// wsLang 是工作空间的语言。
func (h *Hub) wsLang(ctx context.Context, wsID int64) i18n.Lang {
	if h.Core == nil {
		return i18n.Default()
	}
	ws, err := h.Core.Workspace(ctx, wsID)
	if err != nil {
		return i18n.Default()
	}
	return i18n.Pick(ws.Language)
}

// chatLang 是绑定了这个群的工作空间的语言。
func (h *Hub) chatLang(ctx context.Context, provider, chatID string) i18n.Lang {
	if h.Core == nil {
		return i18n.Default()
	}
	ws, err := h.Core.WorkspaceByChat(ctx, provider, "", chatID) // 群编号全局唯一，工作空间群表不记企业
	if err != nil {
		return i18n.Default()
	}
	return i18n.Pick(ws.Language)
}

// accountLang 是连接了这个通讯账号的 Cohive 账号的语言。
func (h *Hub) accountLang(ctx context.Context, provider, tenant, userID string) i18n.Lang {
	if h.Core == nil {
		return i18n.Default()
	}
	acc, err := h.Core.AccountByChat(ctx, provider, tenant, userID)
	if err != nil {
		return i18n.Default()
	}
	return i18n.Pick(acc.Language)
}

// groupOf 返回工作空间绑定的群与对应的通讯工具；没绑群或通讯工具没启用时 ch 为空。
func (h *Hub) groupOf(ctx context.Context, wsID int64) (store.Workspace, Channel, string, error) {
	ws, err := h.Core.Workspace(ctx, wsID)
	if err != nil {
		return ws, nil, "", err
	}
	ch := h.Channels[ws.Notify.Channel]
	if ch == nil || ws.Notify.Target == "" {
		return ws, nil, "", nil
	}
	return ws, ch, ws.Notify.Target, nil
}

// people 读取一批成员资格对应的人，带上他们在各通讯工具上的连接。
func (h *Hub) people(ctx context.Context, ms ...store.Member) map[int64]Person {
	ids := make([]int64, 0, len(ms))
	for _, m := range ms {
		if m.AccountID != 0 {
			ids = append(ids, m.AccountID)
		}
	}
	chats, err := h.Core.ChatIdentities(ctx, ids)
	if err != nil {
		h.logf("failed to load chat identities: %v", err)
	}
	out := make(map[int64]Person, len(ms))
	for _, m := range ms {
		out[m.ID] = personOf(m, chats[m.AccountID])
	}
	return out
}

func signupText(w *writer, s store.Signup, wsName string) string {
	d := i18n.D{"Name": w.nameLabel(s.Name, s.DisplayName), "Workspace": w.esc(wsName)}
	if s.Existing {
		return w.lang.T("notify.signup.existing", d)
	}
	return w.lang.T("notify.signup.new", d)
}

func deviceText(w *writer, a store.DeviceAuth, workspaces []string) string {
	sep := w.lang.T("notify.list_sep")
	return w.lang.T("notify.device.request", i18n.D{"Name": w.nameLabel(a.AccountName, a.DisplayName),
		"Device": w.esc(a.DeviceName), "Tools": w.esc(strings.Join(a.Tools, sep)), "Workspaces": w.esc(strings.Join(workspaces, sep))})
}

// post 发一条审批消息并记下，处理后据此把各群里的消息都改成结果。
func (h *Hub) post(ctx context.Context, ch Channel, chat string, a Approval, m Rendered) error {
	ref, err := ch.SendApproval(ctx, chat, m, a)
	if err != nil || ref == "" { // 没有消息编号：通道发的是不带按钮的降级消息，处理后也不用改
		return err
	}
	return h.Core.RecordBotPost(ctx, store.BotPost{Kind: a.Kind, RefID: a.RefID, WorkspaceID: a.WorkspaceID,
		Provider: ch.Provider(), ChatID: chat, MessageID: ref})
}

// AnnounceSignup 在申请所属工作空间的群里发注册或加入申请的审批消息（没配置群时不发）。
func (h *Hub) AnnounceSignup(ctx context.Context, s store.Signup) error {
	ws, ch, chat, err := h.groupOf(ctx, s.WorkspaceID)
	if err != nil || ch == nil {
		return err
	}
	w := newWriter(ch.Format(), ch.Provider(), i18n.Pick(ws.Language))
	m := w.done(w.lang.T("notify.title.signup"), signupText(w, s, ws.Label())+"\n"+w.lang.T("notify.signup.ask"))
	return h.post(ctx, ch, chat, Approval{Kind: "su", WorkspaceID: ws.ID, RefID: s.ID}, m)
}

// AnnounceDevice 在申请人在册所在、配置了群的每个工作空间群里发设备审批消息。
func (h *Hub) AnnounceDevice(ctx context.Context, a store.DeviceAuth) error {
	if a.AccountID == nil {
		return nil
	}
	ms, err := h.Core.Memberships(ctx, *a.AccountID)
	if err != nil {
		return err
	}
	var names []string
	for _, m := range ms {
		names = append(names, m.Label)
	}
	var firstErr error
	for _, m := range ms {
		wsOfGroup, ch, chat, err := h.groupOf(ctx, m.WorkspaceID)
		if err == nil && ch != nil {
			w := newWriter(ch.Format(), ch.Provider(), i18n.Pick(wsOfGroup.Language))
			msg := w.done(w.lang.T("notify.title.device"), deviceText(w, a, names)+"\n"+w.lang.T("notify.device.ask"))
			err = h.post(ctx, ch, chat, Approval{Kind: "da", WorkspaceID: m.WorkspaceID, RefID: a.ID}, msg)
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// resultText 生成某条申请处理后的结果消息。
type resultText func(w *writer) string

func signupResult(d core.SignupDecision, approve bool) resultText {
	return func(w *writer) string {
		return signupText(w, d.Signup, d.Workspace.Label()) + "\n" + verdictLine(w, approve, d.Decider)
	}
}

func deviceResult(d core.DeviceDecision) resultText {
	return func(w *writer) string {
		return deviceText(w, d.Auth, d.Workspaces) + "\n" + verdictLine(w, d.Auth.Status == store.AuthApproved, d.Decider)
	}
}

// verdictLine 是结果消息的最后一行：已批准或已拒绝，以及审批人。钉钉卡片把这一行放进 status 变量。
func verdictLine(w *writer, approve bool, decider string) string {
	return w.lang.T("notify.verdict.line", i18n.D{"Verdict": verdict(w.lang, approve), "Decider": w.esc(decider)})
}

func renderResult(ch Channel, l i18n.Lang, text resultText) Rendered {
	w := newWriter(ch.Format(), ch.Provider(), l)
	return w.done(l.T("notify.title.result"), text(w))
}

// postRef 指一条审批消息。
type postRef struct{ provider, chat, msg string }

// finish 把某条申请在各群里的审批消息都改成结果、去掉按钮；skip 是已由通道自己改好的那条。
func (h *Hub) finish(ctx context.Context, kind string, refID int64, text resultText, skip postRef) error {
	posts, err := h.Core.BotPosts(ctx, kind, refID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, p := range posts {
		if (postRef{p.Provider, p.ChatID, p.MessageID}) == skip {
			continue
		}
		ch := h.Channels[p.Provider]
		if ch == nil {
			continue
		}
		// 每个群的结果消息用它所属工作空间的语言
		if err := ch.FinishApproval(ctx, p.ChatID, p.MessageID, renderResult(ch, h.wsLang(ctx, p.WorkspaceID), text)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// AnnounceSignupDecided 在网页上处理申请后，把群里的审批消息改成结果。
func (h *Hub) AnnounceSignupDecided(ctx context.Context, d core.SignupDecision) error {
	return h.finish(ctx, "su", d.Signup.ID, signupResult(d, d.Signup.Status == store.SignupApproved), postRef{})
}

// AnnounceDeviceDecided 在网页上处理设备申请后，把各群里的审批消息改成结果。
func (h *Hub) AnnounceDeviceDecided(ctx context.Context, d core.DeviceDecision) error {
	return h.finish(ctx, "da", d.Auth.ID, deviceResult(d), postRef{})
}

// AnnounceJoined 在工作空间群里说一声“X 已加入”，并 @ 他（已连接这个通讯工具时）。用于管理员让人加入。
func (h *Hub) AnnounceJoined(ctx context.Context, m store.Member) error {
	return h.announceJoined(ctx, m, true)
}

// AnnounceSelfJoined 是本人加入（凭邀请注册、接受邀请）：操作者是他自己，只写名字不 @。
func (h *Hub) AnnounceSelfJoined(ctx context.Context, m store.Member) error {
	return h.announceJoined(ctx, m, false)
}

func (h *Hub) announceJoined(ctx context.Context, m store.Member, at bool) error {
	ws, ch, chat, err := h.groupOf(ctx, m.WorkspaceID)
	if err != nil || ch == nil {
		return err
	}
	p := h.people(ctx, m)[m.ID]
	w := newWriter(ch.Format(), ch.Provider(), i18n.Pick(ws.Language))
	who := w.nameLabel(m.Name, m.DisplayName)
	if at {
		who = w.mention(p, who, true)
	}
	text := w.lang.T("notify.joined.text", i18n.D{"Who": who, "Workspace": w.esc(ws.Label())})
	return ch.Send(ctx, chat, w.done(w.lang.T("notify.title.joined"), text))
}

// Click 处理群里的审批按钮。审批对象以审批消息记录为准（按通讯工具与消息编号反查），按钮数据只用来核对；
// 工作空间由按钮所在的群反查（一个群只绑一个工作空间）；点击者按他连接的通讯账号找到 Cohive 账号，
// 业务层校验他是否能管理这个工作空间（设备申请还要求申请人在本工作空间在册）。
// 给点击者的提示在群里，用群所属工作空间的语言；还没查到是哪个工作空间时用服务器缺省语言。
func (h *Hub) Click(ctx context.Context, c Click) ClickResult {
	a, chat := c.Approval, c.ChatID
	lang := i18n.Default()
	post, err := h.Core.BotPostByMessage(ctx, c.Provider, c.ChatID, c.MsgRef)
	switch {
	case err == nil:
		lang = h.wsLang(ctx, post.WorkspaceID)
		if a.Kind != "" && (a.Kind != post.Kind || a.RefID != post.RefID || a.WorkspaceID != post.WorkspaceID) {
			return ClickResult{Toast: lang.T("notify.toast.mismatch")}
		}
		a = Approval{Kind: post.Kind, WorkspaceID: post.WorkspaceID, RefID: post.RefID}
		if chat == "" { // 钉钉卡片回调不带群，以发出时的记录为准
			chat = post.ChatID
		}
	case errors.Is(err, store.ErrNotFound):
		if a.Kind == "" { // 没有记录又没有按钮数据（钉钉卡片），无从得知是哪条申请
			return ClickResult{Toast: lang.T("notify.toast.expired")}
		}
		// Telegram 升级前发的审批消息没有记录：按按钮数据处理，下面照样核对群与工作空间
		lang = h.chatLang(ctx, c.Provider, chat)
	default:
		h.logf("failed to read approval message record: %v", err)
		return ClickResult{Toast: lang.T("notify.toast.failed")}
	}
	if a.Kind != "su" && a.Kind != "da" {
		return ClickResult{}
	}
	ws, err := h.Core.WorkspaceByChat(ctx, c.Provider, "", chat)
	if errors.Is(err, store.ErrNotFound) || (err == nil && ws.ID != a.WorkspaceID) {
		return ClickResult{Toast: lang.T("notify.toast.wrong_group")}
	}
	if err != nil {
		h.logf("failed to identify approval group: %v", err)
		return ClickResult{Toast: lang.T("notify.toast.failed")}
	}
	lang = i18n.Pick(ws.Language)
	actor, err := h.Core.AccountByChat(ctx, c.Provider, c.From.Tenant, c.From.UserID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && actor.Disabled()) {
		return ClickResult{Toast: lang.T("notify.toast.not_linked", i18n.D{"Provider": providerLabel(lang, c.Provider)})}
	}
	if err != nil {
		h.logf("failed to identify approver: %v", err)
		return ClickResult{Toast: lang.T("notify.toast.failed")}
	}
	ch := h.Channels[c.Provider]
	if ch == nil {
		return ClickResult{Toast: lang.T("notify.toast.failed")}
	}
	act := core.ChatActor(actor, c.Provider)
	var text resultText
	var joined *store.Member
	switch a.Kind {
	case "su":
		d, err := h.Core.DecideSignup(ctx, act, ws.ID, a.RefID, c.Approve)
		if err != nil {
			h.denied(ctx, act, ws, c.Provider, i18n.T("notify.denied.signup_button"), err)
			return ClickResult{Toast: h.failToast(lang, err)}
		}
		text, joined = signupResult(d, c.Approve), d.Member
	case "da":
		d, err := h.Core.DecideDeviceAuth(ctx, act, ws.ID, a.RefID, c.Approve)
		if err != nil {
			h.denied(ctx, act, ws, c.Provider, i18n.T("notify.denied.device_button"), err)
			return ClickResult{Toast: h.failToast(lang, err)}
		}
		text = deviceResult(d)
	}
	result := renderResult(ch, lang, text)
	return ClickResult{Toast: verdict(lang, c.Approve), Result: &result, After: func(ctx context.Context) {
		if err := h.finish(ctx, a.Kind, a.RefID, text, postRef{c.Provider, chat, c.MsgRef}); err != nil {
			h.logf("failed to update approval message: %v", err)
		}
		if joined != nil {
			if err := h.AnnounceJoined(ctx, *joined); err != nil {
				h.logf("failed to send member-joined message: %v", err)
			}
		}
	}}
}

// ClickLang 是处理这次点击时给点击者的提示用的语言：审批消息所属工作空间的语言，查不到时是服务器缺省语言。
func (h *Hub) ClickLang(ctx context.Context, c Click) i18n.Lang {
	post, err := h.Core.BotPostByMessage(ctx, c.Provider, c.ChatID, c.MsgRef)
	if err != nil {
		return i18n.Default()
	}
	return h.wsLang(ctx, post.WorkspaceID)
}

// Whoami 返回通讯账号连接的 Cohive 账号，以及他在该群对应工作空间里的角色（chatID 为空或群没绑工作空间时 role 为空）。
// 角色按 lang 写成文字。
func (h *Hub) Whoami(ctx context.Context, lang i18n.Lang, who ChatUser, chatID string) (account, wsLabel, role string, ok bool) {
	acc, err := h.Core.AccountByChat(ctx, who.Provider, who.Tenant, who.UserID)
	if err != nil {
		return "", "", "", false
	}
	if chatID == "" {
		return acc.Name, "", "", true
	}
	ws, err := h.Core.WorkspaceByChat(ctx, who.Provider, "", chatID) // 群编号全局唯一，工作空间群表不记企业
	if err != nil {
		return acc.Name, "", "", true
	}
	role = lang.T("notify.role.none")
	if ms, err := h.Core.Memberships(ctx, acc.ID); err == nil {
		for _, m := range ms {
			if m.WorkspaceID == ws.ID {
				role = roleLabel(lang, m.Role)
			}
		}
	}
	return acc.Name, ws.Label(), role, true
}

func roleLabel(l i18n.Lang, role string) string {
	if role == store.RoleAdmin {
		return l.T("notify.role.admin")
	}
	return l.T("notify.role.member")
}

func verdict(l i18n.Lang, approve bool) string {
	if approve {
		return l.T("notify.verdict.approved")
	}
	return l.T("notify.verdict.rejected")
}

func providerLabel(l i18n.Lang, provider string) string {
	switch provider {
	case store.ProviderTelegram:
		return l.T("notify.provider.telegram")
	case store.ProviderDingTalk:
		return l.T("notify.provider.dingtalk")
	}
	return provider
}

// denied 记下已连接通讯账号的人在群里点审批按钮却没有权限（与网页上被拒的管理请求同样记录）。
// button 写进管理操作记录，是存进数据库的文字，用服务器缺省语言。
func (h *Hub) denied(ctx context.Context, act core.Actor, ws store.Workspace, provider, button string, err error) {
	if !errors.Is(err, core.ErrNotAdmin) && !errors.Is(err, core.ErrNotManager) {
		return
	}
	d := core.DeniedAttempt{Workspace: ws.Name, Route: button, Reason: core.DenyNoPermission, Credential: core.ChatCredential(provider)}
	if err := h.Core.RecordDenied(ctx, act, d); err != nil {
		h.logf("failed to record denied approval: %v", err)
	}
}

func (h *Hub) failToast(l i18n.Lang, err error) string {
	switch {
	case errors.Is(err, core.ErrNotAdmin), errors.Is(err, core.ErrNotManager), errors.Is(err, core.ErrDecided),
		errors.Is(err, store.ErrAccountDisabled):
		return i18n.Message(l, err)
	case errors.Is(err, store.ErrNotFound):
		return l.T("notify.toast.not_found")
	}
	h.logf("failed to process approval: %v", err)
	return l.T("notify.toast.failed")
}
