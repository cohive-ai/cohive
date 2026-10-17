package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

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
	ViaServerCLI = "server_cli"
	ViaMemberCLI = "member_cli"
	ViaAgent     = "agent" // 编程工具令牌：只会出现在被拒的管理请求里
	ViaSystem    = "system"
	ViaUnknown   = "unknown" // 只用于补历史
)

// ViaLabels 是入口的中文名。
var ViaLabels = map[string]string{
	ViaWeb:       "网页",
	ViaTelegram:  "群按钮",
	ViaServerCLI: "服务器命令行",
	ViaMemberCLI: "成员本机命令行",
	ViaAgent:     "编程工具令牌",
	ViaSystem:    "服务器自动",
	ViaUnknown:   "未知（功能上线前）",
}

// Actor 是管理操作的操作人与入口。Account 为空表示服务器命令行或系统，视为最高权限。
type Actor struct {
	Account *store.Account
	Via     string
}

// WebActor 是网页上的操作人。
func WebActor(a store.Account) Actor { return Actor{Account: &a, Via: ViaWeb} }

// BotActor 是在群里点审批按钮的人。
func BotActor(a store.Account) Actor { return Actor{Account: &a, Via: ViaTelegram} }

// MemberCLIActor 是用设备密钥在本机命令行操作的人。
func MemberCLIActor(a store.Account) Actor { return Actor{Account: &a, Via: ViaMemberCLI} }

// AgentActor 是用编程工具令牌调用接口的人（管理接口一律拒绝，只用于记下被拒的请求）。
func AgentActor(a store.Account) Actor { return Actor{Account: &a, Via: ViaAgent} }

// ServerCLI 是服务器命令行。
func ServerCLI() Actor { return Actor{Via: ViaServerCLI} }

func systemActor() Actor { return Actor{Via: ViaSystem} }

// 动作。
const (
	ActSignupApprove     = "signup.approve"
	ActSignupReject      = "signup.reject"
	ActJoinApprove       = "join.approve"
	ActJoinReject        = "join.reject"
	ActDeviceApprove     = "device.approve"
	ActDeviceDeny        = "device.deny"
	ActDeviceSelfApprove = "device.self_approve"
	ActMemberRole        = "member.role"
	ActInviteCreate      = "invite.create"
	ActInviteRevoke      = "invite.revoke"
	ActInviteUse         = "invite.use"
	ActDeviceRevoke      = "device.revoke"
	ActMemberAdd         = "member.add"
	ActMemberHandover    = "member.handover"
	ActMemberRemove      = "member.remove"
	ActAccountDisable    = "account.disable"
	ActAccountEnable     = "account.enable"
	ActServerAdminGrant  = "account.server_admin_grant"
	ActServerAdminRevoke = "account.server_admin_revoke"
	ActResetPassword     = "account.reset_password"
	ActAccountUpdate     = "account.update"
	ActTelegramUnlink    = "account.telegram_unlink"
	ActTelegramLink      = "account.telegram_link"
	ActPasswordChange    = "account.password_change"
	ActAccountCreate     = "account.create"
	ActWorkspaceCreate   = "workspace.create"
	ActWorkspaceDelete   = "workspace.delete"
	ActWorkspaceSettings = "workspace.settings"
	ActTokenIssue        = "token.issue"
	ActTokenRevoke       = "token.revoke"
	ActServiceOwner      = "service.owner"
	ActAccessDenied      = "access.denied"

	// 预留给 M6-d（对外地址改为服务器级、工作空间拆路径与显示名），业务还没实现
	ActServerSettings       = "server.settings"
	ActWorkspaceDisplayName = "workspace.display_name"
)

// ReservedActions 是已登记、但还没有业务入口的动作。实现业务时从这里删掉，并补上用例。
var ReservedActions = map[string]bool{ActServerSettings: true, ActWorkspaceDisplayName: true}

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
)

// Category 是一个动作类别。
type Category struct {
	ID    string
	Label string
}

// Categories 按显示顺序列出动作类别。
var Categories = []Category{
	{CatApproval, "审批"}, {CatRole, "角色"}, {CatInvite, "邀请"}, {CatDevice, "设备"}, {CatMember, "成员"},
	{CatAccount, "账号"}, {CatWorkspace, "工作空间"}, {CatTokenService, "令牌与服务"}, {CatServer, "服务器设置"},
	{CatDenied, "被拒的请求"},
}

type actionInfo struct {
	label    string
	category string
	verb     func(e store.AdminEvent) string // 谓语部分：“批准了 tester-d 的注册申请”
}

var actions = map[string]actionInfo{
	ActSignupApprove: {"批准注册申请", CatApproval, func(e store.AdminEvent) string { return "批准了 " + str(e, "applicant") + " 的注册申请" }},
	ActSignupReject:  {"拒绝注册申请", CatApproval, func(e store.AdminEvent) string { return "拒绝了 " + str(e, "applicant") + " 的注册申请" }},
	ActJoinApprove:   {"批准加入申请", CatApproval, func(e store.AdminEvent) string { return "批准了 " + str(e, "applicant") + " 的加入申请" }},
	ActJoinReject:    {"拒绝加入申请", CatApproval, func(e store.AdminEvent) string { return "拒绝了 " + str(e, "applicant") + " 的加入申请" }},
	ActDeviceApprove: {"批准新设备", CatApproval, func(e store.AdminEvent) string {
		return fmt.Sprintf("批准了 %s 的新设备 %s", str(e, "owner"), deviceLabel(str(e, "device_name")))
	}},
	ActDeviceDeny: {"拒绝新设备", CatApproval, func(e store.AdminEvent) string {
		s := fmt.Sprintf("拒绝了 %s 的新设备 %s", str(e, "owner"), deviceLabel(str(e, "device_name")))
		if str(e, "reason") == "applicant_disabled" {
			s += "（申请人已停用，按拒绝处理）"
		}
		return s
	}},
	ActDeviceSelfApprove: {"授权第一台设备", CatApproval, func(e store.AdminEvent) string {
		return "授权了自己的第一台设备 " + deviceLabel(str(e, "device_name"))
	}},
	ActMemberRole: {"改角色", CatRole, func(e store.AdminEvent) string {
		return fmt.Sprintf("把 %s 的角色从%s改为%s", e.TargetName, roleName(str(e, "from")), roleName(str(e, "to")))
	}},
	ActInviteCreate: {"生成邀请", CatInvite, func(e store.AdminEvent) string {
		s := "生成了一个" + inviteKindName(str(e, "kind"))
		var parts []string
		if str(e, "kind") == store.InviteTeam {
			parts = append(parts, fmt.Sprintf("可用 %d 次", num(e, "max_uses")))
		} else {
			parts = append(parts, "加入后为"+roleName(str(e, "role")))
		}
		if t, err := time.Parse(time.RFC3339, str(e, "expires_at")); err == nil {
			parts = append(parts, t.Local().Format("2006-01-02 15:04")+" 到期")
		}
		return s + "（" + strings.Join(parts, "，") + "）"
	}},
	ActInviteRevoke: {"作废邀请", CatInvite, func(e store.AdminEvent) string {
		return fmt.Sprintf("作废了%s #%d", inviteKindName(str(e, "kind")), targetID(e))
	}},
	ActInviteUse: {"凭邀请加入", CatInvite, func(e store.AdminEvent) string {
		if b(e, "account_created") {
			return "凭个人邀请注册了账号并加入工作空间（" + roleName(str(e, "role")) + "）"
		}
		return "凭个人邀请加入了工作空间（" + roleName(str(e, "role")) + "）"
	}},
	ActDeviceRevoke: {"吊销设备", CatDevice, func(e store.AdminEvent) string {
		if b(e, "self") {
			return "吊销了自己的设备 " + deviceLabel(str(e, "device_name"))
		}
		return fmt.Sprintf("吊销了 %s 的设备 %s", str(e, "owner"), deviceLabel(str(e, "device_name")))
	}},
	ActMemberAdd: {"加入成员", CatMember, func(e store.AdminEvent) string {
		s := fmt.Sprintf("把 %s 加入了工作空间（%s）", e.TargetName, roleName(str(e, "role")))
		if b(e, "account_created") {
			s += "，并新建了这个账号"
		}
		return s
	}},
	ActMemberHandover: {"转交", CatMember, func(e store.AdminEvent) string {
		return fmt.Sprintf("转交了 %s 名下的%s", e.TargetName, handoverSummary(e))
	}},
	ActMemberRemove: {"移出成员", CatMember, func(e store.AdminEvent) string {
		s := fmt.Sprintf("把 %s 移出了工作空间", e.TargetName)
		if h := handoverSummary(e); h != "" {
			s += "，转交" + h
		}
		return s
	}},
	ActAccountDisable: {"停用账号", CatAccount, func(e store.AdminEvent) string {
		s := "停用了账号 " + e.TargetName
		if n := num(e, "devices_revoked"); n > 0 {
			s += fmt.Sprintf("，吊销 %d 台设备", n)
		}
		return s
	}},
	ActAccountEnable:     {"启用账号", CatAccount, func(e store.AdminEvent) string { return "启用了账号 " + e.TargetName }},
	ActServerAdminGrant:  {"授予服务器管理员", CatAccount, func(e store.AdminEvent) string { return "授予 " + e.TargetName + " 服务器管理员" }},
	ActServerAdminRevoke: {"撤销服务器管理员", CatAccount, func(e store.AdminEvent) string { return "撤销了 " + e.TargetName + " 的服务器管理员" }},
	ActResetPassword:     {"重置密码", CatAccount, func(e store.AdminEvent) string { return "重置了 " + e.TargetName + " 的密码" }},
	ActAccountUpdate: {"修改账号资料", CatAccount, func(e store.AdminEvent) string {
		var fields []string
		for _, k := range []string{"display_name", "telegram_username", "telegram_user_id"} {
			if _, ok := e.Detail[k]; ok {
				fields = append(fields, detailLabels[k])
			}
		}
		return fmt.Sprintf("修改了 %s 的%s", e.TargetName, strings.Join(fields, "、"))
	}},
	ActTelegramUnlink: {"断开 Telegram", CatAccount, func(e store.AdminEvent) string {
		if b(e, "self") {
			return "断开了自己的 Telegram 连接"
		}
		return "断开了 " + e.TargetName + " 的 Telegram 连接"
	}},
	ActTelegramLink:   {"连接 Telegram", CatAccount, func(e store.AdminEvent) string { return "连接了自己的 Telegram 账号" }},
	ActPasswordChange: {"修改密码", CatAccount, func(e store.AdminEvent) string { return "修改了自己的密码" }},
	ActAccountCreate: {"建账号", CatAccount, func(e store.AdminEvent) string {
		if b(e, "initial_admin") {
			return "建了初始管理员账号 " + e.TargetName
		}
		return "建了账号 " + e.TargetName
	}},
	ActWorkspaceCreate: {"建工作空间", CatWorkspace, func(e store.AdminEvent) string {
		s := "建了工作空间 " + e.TargetName
		if a := str(e, "admin"); a != "" {
			s += "，首位管理员 " + a
		}
		return s
	}},
	ActWorkspaceDelete: {"删除工作空间", CatWorkspace, func(e store.AdminEvent) string { return "删除了工作空间 " + e.TargetName }},
	ActWorkspaceSettings: {"修改工作空间设置", CatWorkspace, func(e store.AdminEvent) string {
		var fields []string
		for _, k := range []string{"public_url", "telegram_chat", "show_title", "show_brief"} {
			if _, ok := e.Detail[k]; ok {
				fields = append(fields, detailLabels[k])
			}
		}
		return fmt.Sprintf("修改了工作空间 %s 的设置：%s", e.TargetName, strings.Join(fields, "、"))
	}},
	ActTokenIssue: {"签发令牌", CatTokenService, func(e store.AdminEvent) string {
		return fmt.Sprintf("为 %s 的 %s 签发了令牌 #%d", str(e, "member"), str(e, "tool"), targetID(e))
	}},
	ActTokenRevoke: {"吊销令牌", CatTokenService, func(e store.AdminEvent) string {
		return fmt.Sprintf("吊销了 %s 的 %s 令牌 #%d", str(e, "member"), str(e, "tool"), targetID(e))
	}},
	ActServiceOwner: {"登记服务负责人", CatTokenService, func(e store.AdminEvent) string {
		if from := str(e, "from"); from != "" {
			return fmt.Sprintf("把服务 %s 的负责人从 %s 改为 %s", e.TargetName, from, str(e, "to"))
		}
		return fmt.Sprintf("登记了服务 %s，负责人 %s", e.TargetName, str(e, "to"))
	}},
	ActServerSettings: {"修改服务器设置", CatServer, func(e store.AdminEvent) string {
		var fields []string
		for _, k := range []string{"external_url"} {
			if _, ok := e.Detail[k]; ok {
				fields = append(fields, detailLabels[k])
			}
		}
		if len(fields) == 0 {
			return "修改了服务器设置"
		}
		return "修改了服务器设置：" + strings.Join(fields, "、")
	}},
	ActWorkspaceDisplayName: {"修改工作空间显示名", CatWorkspace, func(e store.AdminEvent) string {
		if m, ok := e.Detail["display_name"].(map[string]any); ok {
			return fmt.Sprintf("把工作空间 %s 的显示名从“%v”改为“%v”", e.TargetName, m["from"], m["to"])
		}
		return "修改了工作空间 " + e.TargetName + " 的显示名"
	}},
	ActAccessDenied: {"被拒的管理请求", CatDenied, func(e store.AdminEvent) string {
		what := "调用管理接口 " + e.TargetName
		if e.Via == ViaTelegram {
			what = "点了" + e.TargetName
		}
		return what + "，被拒绝：" + deniedReasons[str(e, "reason")]
	}},
}

// 被拒的原因。
const (
	DenyNeedPassword = "need_password_login" // 编程工具令牌、设备密钥、cohive web 免密会话调只认密码会话的接口
	DenyNoPermission = "no_permission"       // 密码会话或群按钮，但不是管理员
)

var deniedReasons = map[string]string{
	DenyNeedPassword: "管理操作只认账号密码登录的网页",
	DenyNoPermission: "没有权限",
}

// 被拒请求用的凭据。
const (
	CredPassword  = "password"   // 账号密码登录的网页会话
	CredDeviceWeb = "device_web" // cohive web 免密会话
	CredToken     = "token"      // 编程工具令牌
	CredDevice    = "device"     // 设备密钥
	CredTelegram  = "telegram"   // 群按钮
)

var credentialNames = map[string]string{
	CredPassword: "账号密码登录的网页", CredDeviceWeb: "cohive web 免密网页", CredToken: "编程工具令牌",
	CredDevice: "设备密钥", CredTelegram: "Telegram 群按钮",
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

// ActionLabel 返回动作的中文名。
func ActionLabel(action string) string { return actions[action].label }

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
	case ViaWeb, ViaTelegram, ViaServerCLI, ViaMemberCLI, ViaSystem:
	case ViaAgent:
		if e.Action != ActAccessDenied {
			return fmt.Errorf("编程工具令牌不能做管理操作（%s）", e.Action)
		}
	default:
		return fmt.Errorf("管理操作缺少入口（%q）", act.Via)
	}
	if _, ok := actions[e.Action]; !ok {
		return fmt.Errorf("未登记的管理操作 %q", e.Action)
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

// EventText 把一条记录写成人话：“lee 在群里批准了 tester-d 的注册申请”。
func EventText(e store.AdminEvent) string {
	info, ok := actions[e.Action]
	verb := e.Action
	if ok {
		verb = info.verb(e)
	}
	who := e.ActorName
	switch e.Via {
	case ViaWeb:
		return who + " 在网页上" + verb
	case ViaTelegram:
		return who + " 在群里" + verb
	case ViaMemberCLI:
		return who + " 在本机命令行" + verb
	case ViaAgent:
		return who + " 用编程工具令牌" + verb
	case ViaServerCLI:
		if who != "" {
			return who + " 在服务器命令行" + verb
		}
		return "服务器命令行" + verb
	case ViaSystem:
		return "服务器首次启动时" + verb
	default:
		if who == "" {
			who = "（操作人未知）"
		}
		return who + " " + verb + "（功能上线前补记，入口未知）"
	}
}

// DetailItem 是附加信息的一项（中文名与文本值）。
type DetailItem struct {
	Label string
	Value string
}

var detailLabels = map[string]string{
	"applicant": "申请人", "owner": "设备主人", "device_name": "设备名", "tools": "编程工具", "requested": "请求",
	"reason": "原因", "from": "原来", "to": "改为", "kind": "邀请类型", "role": "角色", "max_uses": "可用次数",
	"expires_at": "到期时间", "self": "本人操作", "account_created": "新建账号", "issues": "转交的问题单",
	"services": "转交的服务", "tokens_revoked": "吊销的令牌数", "devices_revoked": "吊销的设备数",
	"display_name": "显示名", "telegram_username": "Telegram 用户名", "telegram_user_id": "Telegram 用户编号",
	"admin": "首位管理员", "public_url": "公网地址", "telegram_chat": "Telegram 群", "show_title": "群消息显示标题",
	"show_brief": "群消息显示简述", "member": "成员", "tool": "工具", "as": "命令行记作", "invite_id": "邀请编号",
	"initial_admin": "初始管理员", "backfilled": "上线前补记", "time_approx": "时间为近似值",
	"credential": "凭据", "external_url": "对外地址",
}

// EventDetail 把附加信息转成中文名与文本值，按键名排序。
func EventDetail(e store.AdminEvent) []DetailItem {
	keys := make([]string, 0, len(e.Detail))
	for k := range e.Detail {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]DetailItem, 0, len(keys))
	for _, k := range keys {
		label := detailLabels[k]
		if label == "" {
			label = k
		}
		out = append(out, DetailItem{Label: label, Value: detailValue(k, e.Detail[k])})
	}
	return out
}

func detailValue(key string, v any) string {
	switch x := v.(type) {
	case nil:
		return "（空）"
	case bool:
		if x {
			return "是"
		}
		return "否"
	case string:
		switch key {
		case "role", "from", "to":
			if x == store.RoleAdmin || x == store.RoleMember {
				return roleName(x)
			}
		case "kind":
			return inviteKindName(x)
		case "reason":
			if r, ok := deniedReasons[x]; ok {
				return r
			}
		case "credential":
			if c, ok := credentialNames[x]; ok {
				return c
			}
		case "expires_at":
			if t, err := time.Parse(time.RFC3339, x); err == nil {
				return t.Local().Format("2006-01-02 15:04")
			}
		}
		if x == "" {
			return "（空）"
		}
		return x
	case float64:
		return fmt.Sprintf("%d", int64(x))
	case map[string]any:
		// {from, to}
		return detailValue(key, x["from"]) + " → " + detailValue(key, x["to"])
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
				as := "负责人"
				if m["as"] == store.AsSource {
					as = "发起人"
				}
				parts = append(parts, fmt.Sprintf("#%d（%s）→ %v", int64(numOf(m["id"])), as, m["to"]))
			case "services":
				parts = append(parts, fmt.Sprintf("%v → %v", m["name"], m["to"]))
			default:
				raw, _ := json.Marshal(m)
				parts = append(parts, string(raw))
			}
		}
		if len(parts) == 0 {
			return "（无）"
		}
		return strings.Join(parts, "；")
	default:
		raw, _ := json.Marshal(x)
		return string(raw)
	}
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

func handoverSummary(e store.AdminEvent) string {
	var parts []string
	if n := listLen(e, "issues"); n > 0 {
		parts = append(parts, fmt.Sprintf("%d 张问题单", n))
	}
	if n := listLen(e, "services"); n > 0 {
		parts = append(parts, fmt.Sprintf("%d 个服务", n))
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, "、")
}

func roleName(r string) string {
	if r == store.RoleAdmin {
		return "管理员"
	}
	return "成员"
}

func inviteKindName(k string) string {
	if k == store.InviteTeam {
		return "团队邀请"
	}
	return "个人邀请"
}

func deviceLabel(name string) string {
	if name == "" {
		return "（未命名）"
	}
	return name
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
		return f, false, inputErr("limit 应为 1 到 %d", MaxEventPage)
	}
	if q.Before < 0 {
		return f, false, inputErr("before 应为记录编号")
	}
	if q.Category != "" {
		if f.Actions = CategoryActions(q.Category); len(f.Actions) == 0 {
			return f, false, inputErr("没有这个动作类别：%s", q.Category)
		}
	}
	if q.Via != "" {
		if _, ok := ViaLabels[q.Via]; !ok {
			return f, false, inputErr("没有这个入口：%s", q.Via)
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
