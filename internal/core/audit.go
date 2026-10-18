package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// 管理操作记录（计划 docs/superpowers/plans/2026-10-17-phase1-m6c-admin-audit.md）。
// 每个管理操作在业务全部成功之后、事务提交之前调用 record，与业务一起提交或一起回滚。
// 附加信息只放非秘密的值：编号、名字、角色、类别；绝不放密码、哈希、令牌、邀请码、设备密钥。

// 入口。
const (
	ViaWeb       = "web"
	ViaTelegram  = "telegram"
	ViaDingTalk  = "dingtalk"
	ViaServerCLI = "server_cli"
	ViaMemberCLI = "member_cli"
	ViaAgent     = "agent" // 编程工具令牌：只会出现在被拒的管理请求里
	ViaSystem    = "system"
	ViaUnknown   = "unknown" // 只用于补历史
)

// ViaIDs 按显示顺序列出全部入口。
var ViaIDs = []string{ViaWeb, ViaTelegram, ViaDingTalk, ViaServerCLI, ViaMemberCLI, ViaAgent, ViaSystem, ViaUnknown}

// ViaLabel 是入口按语言的名字；不认识的入口原样返回。
func ViaLabel(l i18n.Lang, via string) string {
	if !validVia(via) {
		return via
	}
	return l.T("audit.via." + via) // 键 audit.via.<入口>，TestAuditKeysComplete 遍历 ViaIDs 确认齐全
}

func validVia(via string) bool {
	for _, v := range ViaIDs {
		if v == via {
			return true
		}
	}
	return false
}

// Actor 是管理操作的操作人与入口。Account 为空表示服务器命令行或系统，视为最高权限。
type Actor struct {
	Account *store.Account
	Via     string
}

// WebActor 是网页上的操作人。
func WebActor(a store.Account) Actor { return Actor{Account: &a, Via: ViaWeb} }

// BotActor 是在 Telegram 群里点审批按钮的人。
func BotActor(a store.Account) Actor { return Actor{Account: &a, Via: ViaTelegram} }

// ChatActor 是在某种通讯工具的群里操作（点审批按钮、绑定群）的人。
func ChatActor(a store.Account, provider string) Actor {
	if provider == store.ProviderDingTalk {
		return Actor{Account: &a, Via: ViaDingTalk}
	}
	return BotActor(a)
}

// MemberCLIActor 是用设备密钥在本机命令行操作的人。
func MemberCLIActor(a store.Account) Actor { return Actor{Account: &a, Via: ViaMemberCLI} }

// AgentActor 是用编程工具令牌调用接口的人（管理接口一律拒绝，只用于记下被拒的请求）。
func AgentActor(a store.Account) Actor { return Actor{Account: &a, Via: ViaAgent} }

// ServerCLI 是服务器命令行。
func ServerCLI() Actor { return Actor{Via: ViaServerCLI} }

func systemActor() Actor { return Actor{Via: ViaSystem} }

// 动作。
const (
	ActSignupApprove       = "signup.approve"
	ActSignupReject        = "signup.reject"
	ActJoinApprove         = "join.approve"
	ActJoinReject          = "join.reject"
	ActDeviceApprove       = "device.approve"
	ActDeviceDeny          = "device.deny"
	ActDeviceSelfApprove   = "device.self_approve"
	ActMemberRole          = "member.role"
	ActInviteCreate        = "invite.create"
	ActInviteRevoke        = "invite.revoke"
	ActInviteUse           = "invite.use"
	ActDeviceRevoke        = "device.revoke"
	ActMemberAdd           = "member.add"
	ActMemberHandover      = "member.handover"
	ActMemberRemove        = "member.remove"
	ActAccountDisable      = "account.disable"
	ActAccountEnable       = "account.enable"
	ActServerAdminGrant    = "account.server_admin_grant"
	ActServerAdminRevoke   = "account.server_admin_revoke"
	ActResetPassword       = "account.reset_password"
	ActAccountUpdate       = "account.update"
	ActTelegramUnlink      = "account.telegram_unlink"
	ActTelegramLink        = "account.telegram_link"
	ActDingTalkLink        = "account.dingtalk_link"
	ActDingTalkUnlink      = "account.dingtalk_unlink"
	ActWorkspaceChatBind   = "workspace.chat_bind"
	ActWorkspaceChatUnbind = "workspace.chat_unbind"
	ActPasswordChange      = "account.password_change"
	ActAccountCreate       = "account.create"
	ActWorkspaceCreate     = "workspace.create"
	ActWorkspaceDelete     = "workspace.delete"
	ActWorkspaceSettings   = "workspace.settings"
	ActTokenIssue          = "token.issue"
	ActTokenRevoke         = "token.revoke"
	ActServiceOwner        = "service.owner"
	ActIssueCancel         = "issue.cancel"
	ActAccessDenied        = "access.denied"

	ActServerSettings       = "server.settings"
	ActWorkspaceDisplayName = "workspace.display_name"
)

// ReservedActions 是已登记、但还没有业务入口的动作。实现业务时从这里删掉，并补上用例。
var ReservedActions = map[string]bool{}

// 动作类别（查看时按类别筛选）。
const (
	CatApproval     = "approval"
	CatRole         = "role"
	CatInvite       = "invite"
	CatDevice       = "device"
	CatMember       = "member"
	CatAccount      = "account"
	CatWorkspace    = "workspace"
	CatTokenService = "token_service"
	CatDenied       = "denied"
	CatServer       = "server"
	CatIssue        = "issue"
)

// Category 是一个动作类别。
type Category struct {
	ID string
}

// Label 是类别按语言的名字。
func (c Category) Label(l i18n.Lang) string {
	return l.T("audit.category." + c.ID) // 键 audit.category.<类别>，TestAuditKeysComplete 遍历 Categories 确认齐全
}

// Categories 按显示顺序列出动作类别。
var Categories = []Category{
	{CatApproval}, {CatRole}, {CatInvite}, {CatDevice}, {CatMember},
	{CatAccount}, {CatWorkspace}, {CatIssue}, {CatTokenService}, {CatServer},
	{CatDenied},
}

type actionInfo struct {
	category string
	verb     func(l i18n.Lang, e store.AdminEvent) string // 谓语部分：“批准了 tester-d 的注册申请”
}

// 谓语的文案键都写成字面量，方便静态检查；动作名里的点在键里写成下划线（signup.approve → audit.verb.signup_approve）。
var actions = map[string]actionInfo{
	ActSignupApprove: {CatApproval, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.signup_approve", i18n.D{"Applicant": str(e, "applicant")})
	}},
	ActSignupReject: {CatApproval, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.signup_reject", i18n.D{"Applicant": str(e, "applicant")})
	}},
	ActJoinApprove: {CatApproval, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.join_approve", i18n.D{"Applicant": str(e, "applicant")})
	}},
	ActJoinReject: {CatApproval, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.join_reject", i18n.D{"Applicant": str(e, "applicant")})
	}},
	ActDeviceApprove: {CatApproval, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.device_approve", i18n.D{"Owner": str(e, "owner"), "Device": deviceLabel(l, str(e, "device_name"))})
	}},
	ActDeviceDeny: {CatApproval, func(l i18n.Lang, e store.AdminEvent) string {
		key := "audit.verb.device_deny"
		if str(e, "reason") == "applicant_disabled" {
			key = "audit.verb.device_deny_disabled"
		}
		return l.T(key, i18n.D{"Owner": str(e, "owner"), "Device": deviceLabel(l, str(e, "device_name"))})
	}},
	ActDeviceSelfApprove: {CatApproval, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.device_self_approve", i18n.D{"Device": deviceLabel(l, str(e, "device_name"))})
	}},
	ActMemberRole: {CatRole, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.member_role", i18n.D{"Target": e.TargetName, "From": roleName(l, str(e, "from")), "To": roleName(l, str(e, "to"))})
	}},
	ActInviteCreate: {CatInvite, func(l i18n.Lang, e store.AdminEvent) string {
		var parts []string
		if str(e, "kind") == store.InviteTeam {
			parts = append(parts, l.T("audit.invite.uses", i18n.D{"Count": int(num(e, "max_uses"))}))
		} else {
			parts = append(parts, l.T("audit.invite.joins_as", i18n.D{"Role": roleName(l, str(e, "role"))}))
		}
		if t, err := time.Parse(time.RFC3339, str(e, "expires_at")); err == nil {
			parts = append(parts, l.T("audit.invite.expires", i18n.D{"At": t.Local().Format("2006-01-02 15:04")}))
		}
		return l.T("audit.verb.invite_create", i18n.D{"Kind": inviteKindName(l, str(e, "kind")), "Parts": strings.Join(parts, l.T("audit.sep.clause"))})
	}},
	ActInviteRevoke: {CatInvite, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.invite_revoke", i18n.D{"Kind": inviteKindName(l, str(e, "kind")), "ID": targetID(e)})
	}},
	ActInviteUse: {CatInvite, func(l i18n.Lang, e store.AdminEvent) string {
		key := "audit.verb.invite_use"
		if b(e, "account_created") {
			key = "audit.verb.invite_use_created"
		}
		return l.T(key, i18n.D{"Role": roleName(l, str(e, "role"))})
	}},
	ActDeviceRevoke: {CatDevice, func(l i18n.Lang, e store.AdminEvent) string {
		if b(e, "self") {
			return l.T("audit.verb.device_revoke_self", i18n.D{"Device": deviceLabel(l, str(e, "device_name"))})
		}
		return l.T("audit.verb.device_revoke", i18n.D{"Owner": str(e, "owner"), "Device": deviceLabel(l, str(e, "device_name"))})
	}},
	ActMemberAdd: {CatMember, func(l i18n.Lang, e store.AdminEvent) string {
		key := "audit.verb.member_add"
		if b(e, "account_created") {
			key = "audit.verb.member_add_created"
		}
		return l.T(key, i18n.D{"Target": e.TargetName, "Role": roleName(l, str(e, "role"))})
	}},
	ActMemberHandover: {CatMember, func(l i18n.Lang, e store.AdminEvent) string {
		return strings.TrimSpace(l.T("audit.verb.member_handover", i18n.D{"Target": e.TargetName, "Summary": handoverSummary(l, e)}))
	}},
	ActMemberRemove: {CatMember, func(l i18n.Lang, e store.AdminEvent) string {
		if h := handoverSummary(l, e); h != "" {
			return l.T("audit.verb.member_remove_handover", i18n.D{"Target": e.TargetName, "Summary": h})
		}
		return l.T("audit.verb.member_remove", i18n.D{"Target": e.TargetName})
	}},
	ActAccountDisable: {CatAccount, func(l i18n.Lang, e store.AdminEvent) string {
		if n := num(e, "devices_revoked"); n > 0 {
			return l.T("audit.verb.account_disable_devices", i18n.D{"Target": e.TargetName, "Count": int(n)})
		}
		return l.T("audit.verb.account_disable", i18n.D{"Target": e.TargetName})
	}},
	ActAccountEnable: {CatAccount, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.account_enable", i18n.D{"Target": e.TargetName})
	}},
	ActServerAdminGrant: {CatAccount, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.account_server_admin_grant", i18n.D{"Target": e.TargetName})
	}},
	ActServerAdminRevoke: {CatAccount, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.account_server_admin_revoke", i18n.D{"Target": e.TargetName})
	}},
	ActResetPassword: {CatAccount, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.account_reset_password", i18n.D{"Target": e.TargetName})
	}},
	ActAccountUpdate: {CatAccount, func(l i18n.Lang, e store.AdminEvent) string {
		var fields []string
		for _, k := range []string{"display_name", "telegram_username", "telegram_user_id"} {
			if _, ok := e.Detail[k]; ok {
				fields = append(fields, detailLabel(l, k))
			}
		}
		return l.T("audit.verb.account_update", i18n.D{"Target": e.TargetName, "Fields": strings.Join(fields, l.T("audit.sep.item"))})
	}},
	ActTelegramUnlink: {CatAccount, func(l i18n.Lang, e store.AdminEvent) string {
		if b(e, "self") {
			return l.T("audit.verb.account_telegram_unlink_self")
		}
		return l.T("audit.verb.account_telegram_unlink", i18n.D{"Target": e.TargetName})
	}},
	ActTelegramLink: {CatAccount, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.account_telegram_link")
	}},
	ActDingTalkLink: {CatAccount, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.account_dingtalk_link", i18n.D{"Name": anyText(e.Detail["dingtalk_name"])})
	}},
	ActDingTalkUnlink: {CatAccount, func(l i18n.Lang, e store.AdminEvent) string {
		if b(e, "self") {
			return l.T("audit.verb.account_dingtalk_unlink_self")
		}
		return l.T("audit.verb.account_dingtalk_unlink", i18n.D{"Target": e.TargetName})
	}},
	ActWorkspaceChatBind: {CatWorkspace, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.workspace_chat_bind", i18n.D{"Target": e.TargetName, "Provider": providerLabel(l, str(e, "provider")), "Title": anyText(e.Detail["chat_title"])})
	}},
	ActWorkspaceChatUnbind: {CatWorkspace, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.workspace_chat_unbind", i18n.D{"Target": e.TargetName, "Provider": providerLabel(l, str(e, "provider")), "Title": anyText(e.Detail["chat_title"])})
	}},
	ActPasswordChange: {CatAccount, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.account_password_change")
	}},
	ActAccountCreate: {CatAccount, func(l i18n.Lang, e store.AdminEvent) string {
		key := "audit.verb.account_create"
		if b(e, "initial_admin") {
			key = "audit.verb.account_create_initial"
		}
		return l.T(key, i18n.D{"Target": e.TargetName})
	}},
	ActWorkspaceCreate: {CatWorkspace, func(l i18n.Lang, e store.AdminEvent) string {
		if a := str(e, "admin"); a != "" {
			return l.T("audit.verb.workspace_create_admin", i18n.D{"Target": e.TargetName, "Admin": a})
		}
		return l.T("audit.verb.workspace_create", i18n.D{"Target": e.TargetName})
	}},
	ActWorkspaceDelete: {CatWorkspace, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.workspace_delete", i18n.D{"Target": e.TargetName})
	}},
	ActWorkspaceSettings: {CatWorkspace, func(l i18n.Lang, e store.AdminEvent) string {
		var fields []string
		for _, k := range []string{"public_url", "telegram_chat", "show_title", "show_brief", "remind_open_hours", "remind_verify_hours", "language"} {
			if _, ok := e.Detail[k]; ok {
				fields = append(fields, detailLabel(l, k))
			}
		}
		return l.T("audit.verb.workspace_settings", i18n.D{"Target": e.TargetName, "Fields": strings.Join(fields, l.T("audit.sep.item"))})
	}},
	ActTokenIssue: {CatTokenService, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.token_issue", i18n.D{"Member": str(e, "member"), "Tool": str(e, "tool"), "ID": targetID(e)})
	}},
	ActTokenRevoke: {CatTokenService, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.token_revoke", i18n.D{"Member": str(e, "member"), "Tool": str(e, "tool"), "ID": targetID(e)})
	}},
	ActServiceOwner: {CatTokenService, func(l i18n.Lang, e store.AdminEvent) string {
		if from := str(e, "from"); from != "" {
			return l.T("audit.verb.service_owner_change", i18n.D{"Target": e.TargetName, "From": from, "To": str(e, "to")})
		}
		return l.T("audit.verb.service_owner", i18n.D{"Target": e.TargetName, "To": str(e, "to")})
	}},
	ActIssueCancel: {CatIssue, func(l i18n.Lang, e store.AdminEvent) string {
		return l.T("audit.verb.issue_cancel", i18n.D{"ID": targetID(e), "Target": e.TargetName})
	}},
	ActServerSettings: {CatServer, func(l i18n.Lang, e store.AdminEvent) string {
		var fields []string
		for _, k := range []string{"external_url"} {
			if _, ok := e.Detail[k]; ok {
				fields = append(fields, detailLabel(l, k))
			}
		}
		if len(fields) == 0 {
			return l.T("audit.verb.server_settings")
		}
		return l.T("audit.verb.server_settings_fields", i18n.D{"Fields": strings.Join(fields, l.T("audit.sep.item"))})
	}},
	ActWorkspaceDisplayName: {CatWorkspace, func(l i18n.Lang, e store.AdminEvent) string {
		if m, ok := e.Detail["display_name"].(map[string]any); ok {
			return l.T("audit.verb.workspace_display_name_change", i18n.D{"Target": e.TargetName, "From": anyText(m["from"]), "To": anyText(m["to"])})
		}
		return l.T("audit.verb.workspace_display_name", i18n.D{"Target": e.TargetName})
	}},
	ActAccessDenied: {CatDenied, func(l i18n.Lang, e store.AdminEvent) string {
		key := "audit.verb.access_denied_api"
		if e.Via == ViaTelegram || e.Via == ViaDingTalk {
			key = "audit.verb.access_denied_button"
		}
		return l.T(key, i18n.D{"Target": e.TargetName, "Reason": deniedReason(l, str(e, "reason"))})
	}},
}

// 被拒的原因。
const (
	DenyNeedPassword = "need_password_login" // 编程工具令牌、设备密钥、cohive web 免密会话调只认密码会话的接口
	DenyNoPermission = "no_permission"       // 密码会话或群按钮，但不是管理员
)

// deniedReason 是被拒原因按语言的说明；不认识的原因返回空串。
func deniedReason(l i18n.Lang, reason string) string {
	switch reason {
	case DenyNeedPassword:
		return l.T("audit.denied.need_password_login")
	case DenyNoPermission:
		return l.T("audit.denied.no_permission")
	}
	return ""
}

// 被拒请求用的凭据。
const (
	CredPassword  = "password"   // 账号密码登录的网页会话
	CredDeviceWeb = "device_web" // cohive web 免密会话
	CredToken     = "token"      // 编程工具令牌
	CredDevice    = "device"     // 设备密钥
	CredTelegram  = "telegram"   // 群按钮
	CredDingTalk  = "dingtalk"   // 钉钉群按钮
)

// ChatCredential 是在某种通讯工具的群里点按钮时用的凭据。
func ChatCredential(provider string) string {
	if provider == store.ProviderDingTalk {
		return CredDingTalk
	}
	return CredTelegram
}

// credentialName 是被拒请求用的凭据按语言的名字；不认识时返回空串。
func credentialName(l i18n.Lang, c string) string {
	switch c {
	case CredPassword:
		return l.T("audit.credential.password")
	case CredDeviceWeb:
		return l.T("audit.credential.device_web")
	case CredToken:
		return l.T("audit.credential.token")
	case CredDevice:
		return l.T("audit.credential.device")
	case CredTelegram:
		return l.T("audit.credential.telegram")
	case CredDingTalk:
		return l.T("audit.credential.dingtalk")
	}
	return ""
}

// DeniedAttempt 是一次被拒的管理请求。Route 是接口模板（不含路径里的邀请码、授权参数等实际值）。
type DeniedAttempt struct {
	Workspace  string // 请求里的工作空间名；不存在时忽略
	Route      string
	Reason     string
	Credential string
}

// RecordDenied 记下已认证的身份调管理接口被拒（台账 D7 的威胁模型：智能体能读本机令牌、会被提示注入）。
// 业务事务已回滚或根本没开始，所以单独开一个事务写。未认证的请求不记。
func (s *Service) RecordDenied(ctx context.Context, act Actor, d DeniedAttempt) error {
	if act.Account == nil {
		return nil
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		ev := store.AdminEvent{Action: ActAccessDenied, TargetKind: "route", TargetName: d.Route,
			Detail: map[string]any{"reason": d.Reason, "credential": d.Credential}}
		if d.Workspace != "" {
			if ws, err := store.WorkspaceByName(ctx, tx, d.Workspace); err == nil {
				ev.WorkspaceID, ev.WorkspaceName = &ws.ID, ws.Name
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		return record(ctx, tx, act, ev)
	})
}

// Actions 按名字列出全部动作。
func Actions() []string {
	out := make([]string, 0, len(actions))
	for k := range actions {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ActionLabel 返回动作按语言的名字；没有登记的动作返回空串。
func ActionLabel(l i18n.Lang, action string) string {
	if _, ok := actions[action]; !ok {
		return ""
	}
	return l.T("audit.action." + strings.ReplaceAll(action, ".", "_")) // TestAuditKeysComplete 遍历 Actions() 确认齐全
}

// ActionCategory 返回动作的类别。
func ActionCategory(action string) string { return actions[action].category }

// CategoryActions 返回某类别的全部动作；类别不存在时返回空。
func CategoryActions(cat string) []string {
	var out []string
	for _, a := range Actions() {
		if actions[a].category == cat {
			out = append(out, a)
		}
	}
	return out
}

// record 写一条管理操作记录。只接受事务；入口或动作不在取值表里时报错，让整个操作回滚。
func record(ctx context.Context, tx pgx.Tx, act Actor, e store.AdminEvent) error {
	switch act.Via {
	case ViaWeb, ViaTelegram, ViaDingTalk, ViaServerCLI, ViaMemberCLI, ViaSystem:
	case ViaAgent:
		if e.Action != ActAccessDenied {
			return i18n.Errorf("audit.err.agent_forbidden", i18n.D{"Action": e.Action})
		}
	default:
		return i18n.Errorf("audit.err.via_missing", i18n.D{"Via": strconv.Quote(act.Via)})
	}
	if _, ok := actions[e.Action]; !ok {
		return i18n.Errorf("audit.err.action_unregistered", i18n.D{"Action": strconv.Quote(e.Action)})
	}
	e.Via = act.Via
	if act.Account != nil {
		id := act.Account.ID
		e.ActorAccountID, e.ActorName = &id, act.Account.Name
	}
	if e.WorkspaceID != nil && e.WorkspaceName == "" {
		ws, err := store.GetWorkspace(ctx, tx, *e.WorkspaceID)
		if err != nil {
			return err
		}
		e.WorkspaceName = ws.Name
	}
	return store.InsertAdminEvent(ctx, tx, e)
}

func idp(id int64) *int64 { return &id }

// wsp 把“0 表示没有”的工作空间编号转成指针。
func wsp(wsID int64) *int64 {
	if wsID == 0 {
		return nil
	}
	return &wsID
}

// ---- 人话 ----

// EventText 把一条记录按语言写成人话：“lee 在群里批准了 tester-d 的注册申请”。
func EventText(l i18n.Lang, e store.AdminEvent) string {
	info, ok := actions[e.Action]
	verb := e.Action
	if ok {
		verb = info.verb(l, e)
	}
	who := e.ActorName
	data := i18n.D{"Who": who, "Verb": verb}
	switch e.Via {
	case ViaWeb:
		return l.T("audit.text.web", data)
	case ViaTelegram:
		return l.T("audit.text.telegram", data)
	case ViaDingTalk:
		return l.T("audit.text.dingtalk", data)
	case ViaMemberCLI:
		return l.T("audit.text.member_cli", data)
	case ViaAgent:
		return l.T("audit.text.agent", data)
	case ViaServerCLI:
		if who != "" {
			return l.T("audit.text.server_cli", data)
		}
		return l.T("audit.text.server_cli_anon", data)
	case ViaSystem:
		return l.T("audit.text.system", data)
	default:
		if who == "" {
			data["Who"] = l.T("audit.text.unknown_actor")
		}
		return l.T("audit.text.unknown", data)
	}
}

// DetailItem 是附加信息的一项（名称与文本值，已按语言渲染）。
type DetailItem struct {
	Label string
	Value string
}

// detailKeys 是附加信息里有名称的键；名称在语言包的 audit.detail.<键>。
var detailKeys = []string{
	"applicant", "owner", "device_name", "tools", "requested", "reason", "from", "to", "kind", "role", "max_uses",
	"expires_at", "self", "account_created", "issues", "services", "tokens_revoked", "devices_revoked",
	"display_name", "telegram_username", "telegram_user_id", "admin", "public_url", "telegram_chat", "show_title",
	"remind_open_hours", "remind_verify_hours", "show_brief", "member", "tool", "as", "invite_id",
	"initial_admin", "backfilled", "time_approx", "credential", "external_url", "language",
}

// detailLabel 是附加信息键按语言的名称；没有登记的键原样返回。
func detailLabel(l i18n.Lang, k string) string {
	for _, d := range detailKeys {
		if d == k {
			return l.T("audit.detail." + k) // TestAuditKeysComplete 遍历 detailKeys 确认齐全
		}
	}
	return k
}

// EventDetail 把附加信息按语言转成名称与文本值，按键名排序。
func EventDetail(l i18n.Lang, e store.AdminEvent) []DetailItem {
	keys := make([]string, 0, len(e.Detail))
	for k := range e.Detail {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]DetailItem, 0, len(keys))
	for _, k := range keys {
		out = append(out, DetailItem{Label: detailLabel(l, k), Value: detailValue(l, k, e.Detail[k])})
	}
	return out
}

func detailValue(l i18n.Lang, key string, v any) string {
	switch x := v.(type) {
	case nil:
		return l.T("audit.value.empty")
	case bool:
		if x {
			return l.T("audit.value.yes")
		}
		return l.T("audit.value.no")
	case string:
		switch key {
		case "role", "from", "to":
			if x == store.RoleAdmin || x == store.RoleMember {
				return roleName(l, x)
			}
		case "kind":
			return inviteKindName(l, x)
		case "reason":
			if r := deniedReason(l, x); r != "" {
				return r
			}
		case "credential":
			if c := credentialName(l, x); c != "" {
				return c
			}
		case "expires_at":
			if t, err := time.Parse(time.RFC3339, x); err == nil {
				return t.Local().Format("2006-01-02 15:04")
			}
		}
		if x == "" {
			return l.T("audit.value.empty")
		}
		return x
	case float64:
		return fmt.Sprintf("%d", int64(x))
	case map[string]any:
		// {from, to}
		return detailValue(l, key, x["from"]) + " → " + detailValue(l, key, x["to"])
	case []any:
		var parts []string
		for _, it := range x {
			m, ok := it.(map[string]any)
			if !ok {
				parts = append(parts, fmt.Sprint(it))
				continue
			}
			switch key {
			case "issues":
				as := l.T("audit.value.as_owner")
				if m["as"] == store.AsSource {
					as = l.T("audit.value.as_reporter")
				}
				parts = append(parts, l.T("audit.value.handover_issue", i18n.D{"ID": int64(numOf(m["id"])), "As": as, "To": anyText(m["to"])}))
			case "services":
				parts = append(parts, fmt.Sprintf("%v → %v", m["name"], m["to"]))
			default:
				raw, _ := json.Marshal(m)
				parts = append(parts, string(raw))
			}
		}
		if len(parts) == 0 {
			return l.T("audit.value.none")
		}
		return strings.Join(parts, l.T("audit.sep.entry"))
	default:
		raw, _ := json.Marshal(x)
		return string(raw)
	}
}

// anyText 把附加信息里的值写成文字，没有值时为空串（避免模板里出现 <no value>）。
func anyText(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

func str(e store.AdminEvent, k string) string {
	s, _ := e.Detail[k].(string)
	return s
}

func b(e store.AdminEvent, k string) bool {
	v, _ := e.Detail[k].(bool)
	return v
}

func numOf(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case int64:
		return float64(x)
	}
	return 0
}

func num(e store.AdminEvent, k string) int64 { return int64(numOf(e.Detail[k])) }

func listLen(e store.AdminEvent, k string) int {
	switch x := e.Detail[k].(type) {
	case []any:
		return len(x)
	case []map[string]any:
		return len(x)
	}
	return 0
}

func targetID(e store.AdminEvent) int64 {
	if e.TargetID == nil {
		return 0
	}
	return *e.TargetID
}

// handoverSummary 是转交内容的摘要（“3 张问题单、2 个服务”），没有内容时为空。
func handoverSummary(l i18n.Lang, e store.AdminEvent) string {
	var parts []string
	if n := listLen(e, "issues"); n > 0 {
		parts = append(parts, l.T("audit.handover.issues", i18n.D{"Count": n}))
	}
	if n := listLen(e, "services"); n > 0 {
		parts = append(parts, l.T("audit.handover.services", i18n.D{"Count": n}))
	}
	return strings.Join(parts, l.T("audit.sep.item"))
}

func roleName(l i18n.Lang, r string) string {
	if r == store.RoleAdmin {
		return l.T("audit.role.admin")
	}
	return l.T("audit.role.member")
}

func inviteKindName(l i18n.Lang, k string) string {
	if k == store.InviteTeam {
		return l.T("audit.invite_kind.team")
	}
	return l.T("audit.invite_kind.personal")
}

func deviceLabel(l i18n.Lang, name string) string {
	if name == "" {
		return l.T("audit.device_unnamed")
	}
	return name
}

// providerLabel 是通讯工具按语言的名字；不认识的原样返回。
func providerLabel(l i18n.Lang, provider string) string {
	switch provider {
	case store.ProviderTelegram:
		return l.T("audit.provider.telegram")
	case store.ProviderDingTalk:
		return l.T("audit.provider.dingtalk")
	}
	return provider
}

// ---- 查看 ----

// EventQuery 是查看记录的条件（字符串形式，来自接口与命令行）。
type EventQuery struct {
	Workspace string // 只用于服务器级查看
	Category  string
	Actor     string // 操作人登录名
	Via       string
	Before    int64
	Limit     int
}

// MaxEventPage 是一页最多返回的记录数。
const MaxEventPage = 200

func (s *Service) eventFilter(ctx context.Context, q EventQuery) (store.AdminEventFilter, bool, error) {
	f := store.AdminEventFilter{Before: q.Before, Limit: q.Limit}
	if q.Limit < 0 || q.Limit > MaxEventPage {
		return f, false, inputErr("audit.err.limit_range", i18n.D{"Max": MaxEventPage})
	}
	if q.Before < 0 {
		return f, false, inputErr("audit.err.before_invalid")
	}
	if q.Category != "" {
		if f.Actions = CategoryActions(q.Category); len(f.Actions) == 0 {
			return f, false, inputErr("audit.err.category_unknown", i18n.D{"Category": q.Category})
		}
	}
	if q.Via != "" {
		if !validVia(q.Via) {
			return f, false, inputErr("audit.err.via_unknown", i18n.D{"Via": q.Via})
		}
		f.Via = q.Via
	}
	if name := strings.TrimSpace(q.Actor); name != "" {
		a, err := store.AccountByName(ctx, s.Pool, name)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return f, true, nil // 查无此人：空列表
			}
			return f, false, err
		}
		f.ActorAccountID = &a.ID
	}
	return f, false, nil
}

// EventPage 是一页记录；NextBefore 为 0 表示没有更早的。
type EventPage struct {
	Items      []store.AdminEvent
	NextBefore int64
}

func (s *Service) listEvents(ctx context.Context, f store.AdminEventFilter) (EventPage, error) {
	limit := f.Limit
	if limit == 0 {
		limit = 50
	}
	f.Limit = limit + 1
	items, err := store.ListAdminEvents(ctx, s.Pool, f)
	if err != nil {
		return EventPage{}, err
	}
	p := EventPage{Items: items}
	if len(items) > limit {
		p.Items = items[:limit]
		p.NextBefore = p.Items[limit-1].ID
	}
	return p, nil
}

// WorkspaceEvents 由工作空间管理员查看本空间的记录（只含本空间编号的，不含服务器级的）。
func (s *Service) WorkspaceEvents(ctx context.Context, actor *store.Account, wsID int64, q EventQuery) (EventPage, error) {
	if err := canAdmin(ctx, s.Pool, actor, wsID); err != nil {
		return EventPage{}, err
	}
	f, empty, err := s.eventFilter(ctx, q)
	if err != nil || empty {
		return EventPage{}, err
	}
	f.WorkspaceID = &wsID
	return s.listEvents(ctx, f)
}

// ServerEvents 由服务器管理员（或服务器命令行，actor 为空）查看全部记录，可按工作空间名筛选；
// 已删除的工作空间按记录里的名字快照筛选。
func (s *Service) ServerEvents(ctx context.Context, actor *store.Account, q EventQuery) (EventPage, error) {
	if err := requireServerAdmin(ctx, s.Pool, actor); err != nil {
		return EventPage{}, err
	}
	f, empty, err := s.eventFilter(ctx, q)
	if err != nil || empty {
		return EventPage{}, err
	}
	if name := strings.TrimSpace(q.Workspace); name != "" {
		ws, err := store.WorkspaceByName(ctx, s.Pool, name)
		switch {
		case err == nil:
			f.WorkspaceID = &ws.ID
		case errors.Is(err, store.ErrNotFound):
			f.WorkspaceName = name
		default:
			return EventPage{}, err
		}
	}
	return s.listEvents(ctx, f)
}
