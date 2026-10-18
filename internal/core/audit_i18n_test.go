package core

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// 动态拼出的文案键（动作、入口、类别、附加信息名）必须在两种语言里都有。
func TestAuditKeysComplete(t *testing.T) {
	has := func(key string) {
		t.Helper()
		for _, l := range []i18n.Lang{i18n.EN, i18n.ZhCN} {
			if !l.Has(key) {
				t.Errorf("语言包 %s 缺键 %s", l, key)
			}
		}
	}
	for _, a := range Actions() {
		has("audit.action." + strings.ReplaceAll(a, ".", "_"))
	}
	for _, v := range ViaIDs {
		has("audit.via." + v)
	}
	for _, c := range Categories {
		has("audit.category." + c.ID)
	}
	for _, k := range detailKeys {
		has("audit.detail." + k)
	}
}

// 每个动作在每种语言、每种入口下渲染出的文字里不应漏出键名、模板残留或格式化错误。
func TestAuditTextNoLeaks(t *testing.T) {
	id := int64(7)
	detail := map[string]any{
		"applicant": "tester-d", "owner": "lee", "device_name": "mac-mini", "member": "ann", "tool": "claude", "to": "bob", "from": "ann",
		"role": "admin", "kind": "team", "max_uses": 3.0, "expires_at": "2026-10-18T10:00:00Z", "provider": "telegram",
		"chat_title": "ops", "dingtalk_name": "李", "admin": "lee", "devices_revoked": 2.0, "display_name": map[string]any{"from": "a", "to": "b"},
		"issues": []any{map[string]any{"id": 1.0, "as": store.AsSource, "to": "bob"}}, "services": []any{map[string]any{"name": "api", "to": "bob"}},
		"reason": DenyNeedPassword, "public_url": "x", "language": "en",
	}
	for _, l := range []i18n.Lang{i18n.EN, i18n.ZhCN} {
		for _, a := range Actions() {
			for _, via := range ViaIDs {
				for _, d := range []map[string]any{detail, {}} {
					e := store.AdminEvent{Action: a, Via: via, ActorName: "lee", TargetName: "ann", TargetID: &id, Detail: d}
					txt := EventText(l, e)
					for _, bad := range []string{"audit.", "%!", "<no value>", "{{"} {
						if strings.Contains(txt, bad) {
							t.Errorf("%s/%s/%s 的文字里有 %q：%s", l, a, via, bad, txt)
						}
					}
				}
			}
		}
	}
}

func TestEventTextEnglish(t *testing.T) {
	id := int64(3)
	cases := []struct {
		e    store.AdminEvent
		want string
	}{
		{store.AdminEvent{Action: ActSignupApprove, Via: ViaTelegram, ActorName: "lee", Detail: map[string]any{"applicant": "tester-d"}},
			"lee approved the signup request from tester-d in the chat group"},
		{store.AdminEvent{Action: ActDeviceApprove, Via: ViaWeb, ActorName: "lee", Detail: map[string]any{"owner": "ann", "device_name": "mac-mini"}},
			"lee approved device mac-mini for ann on the web"},
		{store.AdminEvent{Action: ActDeviceApprove, Via: ViaWeb, ActorName: "lee", Detail: map[string]any{"owner": "ann"}},
			"lee approved device (unnamed) for ann on the web"},
		{store.AdminEvent{Action: ActMemberRole, Via: ViaWeb, ActorName: "lee", TargetName: "ann", Detail: map[string]any{"from": "member", "to": "admin"}},
			"lee changed ann's role from member to admin on the web"},
		{store.AdminEvent{Action: ActInviteCreate, Via: ViaWeb, ActorName: "lee", Detail: map[string]any{"kind": store.InviteTeam, "max_uses": 1.0}},
			"lee created a team invite (1 use) on the web"},
		{store.AdminEvent{Action: ActInviteCreate, Via: ViaWeb, ActorName: "lee", Detail: map[string]any{"kind": store.InviteTeam, "max_uses": 5.0}},
			"lee created a team invite (5 uses) on the web"},
		{store.AdminEvent{Action: ActInviteRevoke, Via: ViaWeb, ActorName: "lee", TargetID: &id, Detail: map[string]any{"kind": store.InvitePersonal}},
			"lee revoked personal invite #3 on the web"},
		{store.AdminEvent{Action: ActMemberRemove, Via: ViaWeb, ActorName: "lee", TargetName: "ann",
			Detail: map[string]any{"issues": []any{map[string]any{}}, "services": []any{map[string]any{}, map[string]any{}}}},
			"lee removed ann from the workspace and transferred 1 issue, 2 services on the web"},
		{store.AdminEvent{Action: ActAccountDisable, Via: ViaServerCLI, ActorName: "lee", TargetName: "ann", Detail: map[string]any{"devices_revoked": 1.0}},
			"lee deactivated account ann and revoked 1 device in the server CLI"},
		{store.AdminEvent{Action: ActWorkspaceSettings, Via: ViaWeb, ActorName: "lee", TargetName: "pay", Detail: map[string]any{"language": map[string]any{"from": "", "to": "en"}, "show_title": true}},
			"lee changed settings of workspace pay: Show title in group messages, Group message language on the web"},
		{store.AdminEvent{Action: ActAccessDenied, Via: ViaAgent, ActorName: "lee", TargetName: "POST /api/v1/w/{ws}/admin/invites", Detail: map[string]any{"reason": DenyNeedPassword}},
			"lee (coding tool token) called admin API POST /api/v1/w/{ws}/admin/invites and was denied: Admin actions require a web session signed in with a password"},
		{store.AdminEvent{Action: ActResetPassword, Via: ViaServerCLI, TargetName: "tester-a"}, "Server CLI reset the password of tester-a"},
		{store.AdminEvent{Action: ActAccountCreate, Via: ViaSystem, TargetName: "lee", Detail: map[string]any{"initial_admin": true}},
			"Server created initial admin account lee on first start"},
		{store.AdminEvent{Action: ActPasswordChange, Via: ViaUnknown}, "(unknown actor) changed their own password (backfilled, entry point unknown)"},
		{store.AdminEvent{Action: ActWorkspaceChatBind, Via: ViaDingTalk, ActorName: "lee", TargetName: "pay", Detail: map[string]any{"provider": store.ProviderDingTalk, "chat_title": "Ops"}},
			"lee bound workspace pay to the DingTalk group \"Ops\" in the DingTalk group"},
	}
	for _, c := range cases {
		if got := EventText(i18n.EN, c.e); got != c.want {
			t.Errorf("%s：\n得到 %q\n应为 %q", c.e.Action, got, c.want)
		}
	}
}

func TestAuditLabelsEnglish(t *testing.T) {
	if got := ViaLabel(i18n.EN, ViaTelegram); got != "Chat button" {
		t.Errorf("入口名：%q", got)
	}
	if got := ViaLabel(i18n.ZhCN, ViaTelegram); got != "群按钮" {
		t.Errorf("入口名：%q", got)
	}
	if got := ViaLabel(i18n.EN, "no-such-via"); got != "no-such-via" {
		t.Errorf("不认识的入口应原样返回：%q", got)
	}
	if got := ActionLabel(i18n.EN, ActDeviceApprove); got != "Approved new device" {
		t.Errorf("动作名：%q", got)
	}
	if got := ActionLabel(i18n.EN, "no.such"); got != "" {
		t.Errorf("不认识的动作应为空：%q", got)
	}
	if got := Categories[0].Label(i18n.EN); got != "Approvals" {
		t.Errorf("类别名：%q", got)
	}
}

func TestEventDetailEnglish(t *testing.T) {
	e := store.AdminEvent{Detail: map[string]any{
		"self": true, "role": "admin", "kind": store.InviteTeam, "reason": DenyNoPermission, "credential": CredToken,
		"device_name": "", "tools": nil, "language": map[string]any{"from": "", "to": "en"},
		"issues":  []any{map[string]any{"id": 4.0, "as": store.AsSource, "to": "bob"}, map[string]any{"id": 5.0, "as": "assignee", "to": "ann"}},
		"mystery": "keep",
	}}
	got := map[string]string{}
	for _, d := range EventDetail(i18n.EN, e) {
		got[d.Label] = d.Value
	}
	want := map[string]string{
		"Own action": "Yes", "Role": "admin", "Invite type": "team invite", "Reason": "No permission", "Credential": "Coding tool token",
		"Device name": "(empty)", "Coding tools": "(empty)", "Group message language": "(empty) → en",
		"Transferred issues": "#4 (reporter) → bob; #5 (owner) → ann", "mystery": "keep",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("附加信息 %q：得到 %q，应为 %q", k, got[k], v)
		}
	}
	// 中文保持不变
	zh := map[string]string{}
	for _, d := range EventDetail(i18n.ZhCN, e) {
		zh[d.Label] = d.Value
	}
	if zh["本人操作"] != "是" || zh["角色"] != "管理员" || zh["邀请类型"] != "团队邀请" || zh["转交的问题单"] != "#4（发起人）→ bob；#5（负责人）→ ann" {
		t.Errorf("中文附加信息：%v", zh)
	}
}

func TestEventFilterErrorsLocalized(t *testing.T) {
	s := &Service{}
	_, _, err := s.eventFilter(context.Background(), EventQuery{Limit: MaxEventPage + 1})
	ie, ok := err.(*InputError)
	if !ok {
		t.Fatalf("应为 InputError：%v", err)
	}
	if got := ie.Localize(i18n.EN); got != "limit must be between 1 and 200" {
		t.Errorf("英文：%q", got)
	}
	if got := ie.Localize(i18n.ZhCN); got != "limit 应为 1 到 200" {
		t.Errorf("中文：%q", got)
	}
	_, _, err = s.eventFilter(context.Background(), EventQuery{Via: "nope"})
	if got := i18n.Message(i18n.EN, err); got != "Unknown entry point: nope" {
		t.Errorf("英文：%q", got)
	}
	_, _, err = s.eventFilter(context.Background(), EventQuery{Category: "nope"})
	if got := i18n.Message(i18n.ZhCN, err); got != "没有这个动作类别：nope" {
		t.Errorf("中文：%q", got)
	}
}
