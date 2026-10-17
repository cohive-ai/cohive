package notify

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/cohive-ai/cohive/internal/core"
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
		h.logf("读取通讯账号失败：%v", err)
	}
	out := make(map[int64]Person, len(ms))
	for _, m := range ms {
		out[m.ID] = personOf(m, chats[m.AccountID])
	}
	return out
}

func signupText(w *writer, s store.Signup, wsName string) string {
	if s.Existing {
		return fmt.Sprintf("%s（已有账号）申请加入工作空间 %s。", w.nameLabel(s.Name, s.DisplayName), w.esc(wsName))
	}
	return fmt.Sprintf("新成员 %s 申请加入工作空间 %s。", w.nameLabel(s.Name, s.DisplayName), w.esc(wsName))
}

func deviceText(w *writer, a store.DeviceAuth, workspaces []string) string {
	return fmt.Sprintf("%s 申请添加新设备 %s（工具：%s）。\n批准后这台电脑可用于他所在的全部工作空间：%s。",
		w.nameLabel(a.AccountName, a.DisplayName), w.esc(a.DeviceName), w.esc(strings.Join(a.Tools, "、")),
		w.esc(strings.Join(workspaces, "、")))
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
	w := newWriter(ch.Format(), ch.Provider())
	m := w.done("注册申请", signupText(w, s, ws.Label())+"\n请管理员审批（也可以在网页“管理 → 待审”里处理）：")
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
		_, ch, chat, err := h.groupOf(ctx, m.WorkspaceID)
		if err == nil && ch != nil {
			w := newWriter(ch.Format(), ch.Provider())
			msg := w.done("新设备申请", deviceText(w, a, names)+"\n请任一工作空间的管理员审批（也可以在网页“管理 → 待审”里处理）：")
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
		return signupText(w, d.Signup, d.Workspace.Label()) + fmt.Sprintf("\n%s（审批人 %s）", verdict(approve), w.esc(d.Decider))
	}
}

func deviceResult(d core.DeviceDecision) resultText {
	return func(w *writer) string {
		return deviceText(w, d.Auth, d.Workspaces) + fmt.Sprintf("\n%s（审批人 %s）", verdict(d.Auth.Status == store.AuthApproved),
			w.esc(d.Decider))
	}
}

func renderResult(ch Channel, text resultText) Rendered {
	w := newWriter(ch.Format(), ch.Provider())
	return w.done("审批结果", text(w))
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
		if err := ch.FinishApproval(ctx, p.ChatID, p.MessageID, renderResult(ch, text)); err != nil && firstErr == nil {
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

// AnnounceJoined 在工作空间群里说一声“X 已加入”，并 @ 他（已连接这个通讯工具时）。
func (h *Hub) AnnounceJoined(ctx context.Context, m store.Member) error {
	ws, ch, chat, err := h.groupOf(ctx, m.WorkspaceID)
	if err != nil || ch == nil {
		return err
	}
	p := h.people(ctx, m)[m.ID]
	w := newWriter(ch.Format(), ch.Provider())
	text := fmt.Sprintf("%s 已加入工作空间 %s。", w.mention(p, w.nameLabel(m.Name, m.DisplayName), true), w.esc(ws.Label()))
	return ch.Send(ctx, chat, w.done("新成员加入", text))
}

// Click 处理群里的审批按钮。审批对象以审批消息记录为准（按通讯工具与消息编号反查），按钮数据只用来核对；
// 工作空间由按钮所在的群反查（一个群只绑一个工作空间）；点击者按他连接的通讯账号找到 Cohive 账号，
// 业务层校验他是否能管理这个工作空间（设备申请还要求申请人在本工作空间在册）。
func (h *Hub) Click(ctx context.Context, c Click) ClickResult {
	a, chat := c.Approval, c.ChatID
	post, err := h.Core.BotPostByMessage(ctx, c.Provider, c.ChatID, c.MsgRef)
	switch {
	case err == nil:
		if a.Kind != "" && (a.Kind != post.Kind || a.RefID != post.RefID || a.WorkspaceID != post.WorkspaceID) {
			return ClickResult{Toast: "这条审批消息的内容不符，请在网页上处理"}
		}
		a = Approval{Kind: post.Kind, WorkspaceID: post.WorkspaceID, RefID: post.RefID}
		if chat == "" { // 钉钉卡片回调不带群，以发出时的记录为准
			chat = post.ChatID
		}
	case errors.Is(err, store.ErrNotFound):
		if a.Kind == "" { // 没有记录又没有按钮数据（钉钉卡片），无从得知是哪条申请
			return ClickResult{Toast: "这条审批消息已失效，请在网页上处理"}
		}
		// Telegram 升级前发的审批消息没有记录：按按钮数据处理，下面照样核对群与工作空间
	default:
		h.logf("读取审批消息记录失败：%v", err)
		return ClickResult{Toast: "处理失败，请稍后再试"}
	}
	if a.Kind != "su" && a.Kind != "da" {
		return ClickResult{}
	}
	ws, err := h.Core.WorkspaceByChat(ctx, c.Provider, "", chat)
	if errors.Is(err, store.ErrNotFound) || (err == nil && ws.ID != a.WorkspaceID) {
		return ClickResult{Toast: "这条审批消息与本群绑定的工作空间不符，请在网页上处理"}
	}
	if err != nil {
		h.logf("识别审批群失败：%v", err)
		return ClickResult{Toast: "处理失败，请稍后再试"}
	}
	actor, err := h.Core.AccountByChat(ctx, c.Provider, c.From.Tenant, c.From.UserID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && actor.Disabled()) {
		return ClickResult{Toast: fmt.Sprintf("你的%s账号还没有连接到 Cohive，或没有审批权限", providerLabel(c.Provider))}
	}
	if err != nil {
		h.logf("识别审批人失败：%v", err)
		return ClickResult{Toast: "处理失败，请稍后再试"}
	}
	ch := h.Channels[c.Provider]
	if ch == nil {
		return ClickResult{Toast: "处理失败，请稍后再试"}
	}
	act := core.ChatActor(actor, c.Provider)
	var text resultText
	var joined *store.Member
	switch a.Kind {
	case "su":
		d, err := h.Core.DecideSignup(ctx, act, ws.ID, a.RefID, c.Approve)
		if err != nil {
			h.denied(ctx, act, ws, c.Provider, "注册或加入申请的审批按钮", err)
			return ClickResult{Toast: h.failToast(err)}
		}
		text, joined = signupResult(d, c.Approve), d.Member
	case "da":
		d, err := h.Core.DecideDeviceAuth(ctx, act, ws.ID, a.RefID, c.Approve)
		if err != nil {
			h.denied(ctx, act, ws, c.Provider, "新设备的审批按钮", err)
			return ClickResult{Toast: h.failToast(err)}
		}
		text = deviceResult(d)
	}
	result := renderResult(ch, text)
	return ClickResult{Toast: verdict(c.Approve), Result: &result, After: func(ctx context.Context) {
		if err := h.finish(ctx, a.Kind, a.RefID, text, postRef{c.Provider, chat, c.MsgRef}); err != nil {
			h.logf("更新审批消息失败：%v", err)
		}
		if joined != nil {
			if err := h.AnnounceJoined(ctx, *joined); err != nil {
				h.logf("发送新成员加入消息失败：%v", err)
			}
		}
	}}
}

// Whoami 返回通讯账号连接的 Cohive 账号，以及他在该群对应工作空间里的角色（chatID 为空或群没绑工作空间时 role 为空）。
func (h *Hub) Whoami(ctx context.Context, who ChatUser, chatID string) (account, wsLabel, role string, ok bool) {
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
	role = "不是成员"
	if ms, err := h.Core.Memberships(ctx, acc.ID); err == nil {
		for _, m := range ms {
			if m.WorkspaceID == ws.ID {
				role = roleLabel(m.Role)
			}
		}
	}
	return acc.Name, ws.Label(), role, true
}

func roleLabel(role string) string {
	if role == store.RoleAdmin {
		return "管理员"
	}
	return "成员"
}

func verdict(approve bool) string {
	if approve {
		return "已批准"
	}
	return "已拒绝"
}

func providerLabel(provider string) string {
	switch provider {
	case store.ProviderTelegram:
		return " Telegram "
	case store.ProviderDingTalk:
		return "钉钉"
	}
	return provider
}

// denied 记下已连接通讯账号的人在群里点审批按钮却没有权限（与网页上被拒的管理请求同样记录）。
func (h *Hub) denied(ctx context.Context, act core.Actor, ws store.Workspace, provider, button string, err error) {
	if !errors.Is(err, core.ErrNotAdmin) && !errors.Is(err, core.ErrNotManager) {
		return
	}
	d := core.DeniedAttempt{Workspace: ws.Name, Route: button, Reason: core.DenyNoPermission, Credential: core.ChatCredential(provider)}
	if err := h.Core.RecordDenied(ctx, act, d); err != nil {
		h.logf("记录被拒的审批失败：%v", err)
	}
}

func (h *Hub) failToast(err error) string {
	switch {
	case errors.Is(err, core.ErrNotAdmin), errors.Is(err, core.ErrNotManager), errors.Is(err, core.ErrDecided),
		errors.Is(err, store.ErrAccountDisabled):
		return err.Error()
	case errors.Is(err, store.ErrNotFound):
		return "这条申请不存在"
	}
	h.logf("处理审批失败：%v", err)
	return "处理失败，请稍后再试"
}
