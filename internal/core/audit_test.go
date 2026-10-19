package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
)

// afix 是管理操作记录测试的夹具：pay、ops 两个工作空间（见 people），ann 在 pay 名下有两张待处理单与服务 api，
// pay 里还有 bob。secrets 收集测试过程中出现过的秘密（邀请码、令牌、设备密钥、密码），最后检查记录里没有它们。
type afix struct {
	handoverFixture
	svc     *core.Service
	secrets []string
}

func newAfix(t *testing.T) *afix {
	t.Helper()
	h := newHandover(t)
	return &afix{handoverFixture: h, svc: h.p.e.svc}
}

func (f *afix) keep(s ...string) { f.secrets = append(f.secrets, s...) }

func allEvents(t *testing.T, svc *core.Service) []store.AdminEvent {
	t.Helper()
	es, err := store.ListAdminEvents(context.Background(), svc.Pool, store.AdminEventFilter{Limit: 10000})
	if err != nil {
		t.Fatal(err)
	}
	// 按编号升序
	for i, j := 0, len(es)-1; i < j; i, j = i+1, j-1 {
		es[i], es[j] = es[j], es[i]
	}
	return es
}

// teamSignup 让新人 name 凭团队邀请申请注册，返回申请编号。
func (f *afix) teamSignup(t *testing.T, name string) int64 {
	t.Helper()
	ctx := context.Background()
	inv, err := f.svc.CreateInvite(ctx, core.WebActor(f.p.payAdmin), f.p.pay, core.InviteSpec{Kind: store.InviteTeam})
	if err != nil {
		t.Fatal(err)
	}
	f.keep(inv.Code, "pw-"+name+"-0123456789")
	res, err := f.svc.Register(ctx, core.RegisterInput{Code: inv.Code, Name: name, Password: "pw-" + name + "-0123456789"})
	if err != nil || res.Signup == nil {
		t.Fatalf("团队邀请注册应生成申请：%+v %v", res, err)
	}
	return res.Signup.ID
}

// joinRequest 让已有账号 a 凭团队邀请申请加入 pay，返回申请编号。
func (f *afix) joinRequest(t *testing.T, a store.Account) int64 {
	t.Helper()
	ctx := context.Background()
	inv, err := f.svc.CreateInvite(ctx, core.WebActor(f.p.payAdmin), f.p.pay, core.InviteSpec{Kind: store.InviteTeam})
	if err != nil {
		t.Fatal(err)
	}
	f.keep(inv.Code)
	res, err := f.svc.AcceptInvite(ctx, a, inv.Code)
	if err != nil || res.Signup == nil {
		t.Fatalf("应生成加入申请：%+v %v", res, err)
	}
	return res.Signup.ID
}

// annDevice 给 ann 配对一台设备（首台），返回设备编号。
func (f *afix) annDevice(t *testing.T) int64 {
	t.Helper()
	st, err := f.svc.StartDeviceAuth(context.Background(), []string{"codex"}, "ann-mac")
	if err != nil {
		t.Fatal(err)
	}
	f.keep(st.PollSecret, st.Nonce)
	if _, err := f.svc.AuthorizeDevice(context.Background(), f.p.ann, st.Nonce, true); err != nil {
		t.Fatal(err)
	}
	d, err := f.svc.PollDeviceAuth(context.Background(), st.RequestID, st.PollSecret)
	if err != nil {
		t.Fatal(err)
	}
	f.keep(d.DeviceSecret)
	for _, w := range d.Workspaces {
		for _, tok := range w.Tokens {
			f.keep(tok)
		}
	}
	return d.DeviceID
}

func (f *afix) account(t *testing.T, name string) store.Account {
	t.Helper()
	a, err := store.AccountByName(context.Background(), f.svc.Pool, name)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

type wantEvent struct {
	action, via, ws, kind, target, actor string
	detail                               map[string]any // 必须出现且相等的键（值按 JSON 比较）
}

type auditCase struct {
	name string
	run  func(t *testing.T, f *afix) error
	want wantEvent
}

// auditCases 覆盖每个业务层管理方法的每个分支（计划第 2.2 节去掉入口重复后的全部方法）。
func auditCases() []auditCase {
	ctx := context.Background()
	web := func(a store.Account) core.Actor { return core.WebActor(a) }
	return []auditCase{
		{"批准注册申请", func(t *testing.T, f *afix) error {
			id := f.teamSignup(t, "dora")
			_, err := f.svc.DecideSignup(ctx, web(f.p.payAdmin), f.p.pay, id, true)
			return err
		}, wantEvent{core.ActSignupApprove, core.ViaWeb, "pay", "signup", "dora", "pay-admin", map[string]any{"applicant": "dora"}}},
		{"拒绝注册申请", func(t *testing.T, f *afix) error {
			id := f.teamSignup(t, "dora")
			_, err := f.svc.DecideSignup(ctx, web(f.p.payAdmin), f.p.pay, id, false)
			return err
		}, wantEvent{core.ActSignupReject, core.ViaWeb, "pay", "signup", "dora", "pay-admin", nil}},
		{"群里批准加入申请", func(t *testing.T, f *afix) error {
			id := f.joinRequest(t, f.p.opsAdmin)
			_, err := f.svc.DecideSignup(ctx, core.BotActor(f.p.payAdmin), f.p.pay, id, true)
			return err
		}, wantEvent{core.ActJoinApprove, core.ViaTelegram, "pay", "signup", "ops-admin", "pay-admin", map[string]any{"applicant": "ops-admin"}}},
		{"拒绝加入申请", func(t *testing.T, f *afix) error {
			id := f.joinRequest(t, f.p.opsAdmin)
			_, err := f.svc.DecideSignup(ctx, web(f.p.root), 0, id, false)
			return err
		}, wantEvent{core.ActJoinReject, core.ViaWeb, "pay", "signup", "ops-admin", "root", nil}},
		{"工作空间管理页批准设备", func(t *testing.T, f *afix) error {
			id := f.p.pendingDevice(t)
			_, err := f.svc.DecideDeviceAuth(ctx, web(f.p.payAdmin), f.p.pay, id, true)
			return err
		}, wantEvent{core.ActDeviceApprove, core.ViaWeb, "pay", "device_auth", "new", "pay-admin",
			map[string]any{"owner": "ann", "device_name": "new", "tools": []any{"codex"}}}},
		{"服务器管理页拒绝设备", func(t *testing.T, f *afix) error {
			id := f.p.pendingDevice(t)
			_, err := f.svc.DecideDeviceAuth(ctx, web(f.p.root), 0, id, false)
			return err
		}, wantEvent{core.ActDeviceDeny, core.ViaWeb, "", "device_auth", "new", "root", map[string]any{"owner": "ann"}}},
		{"群里批准设备但申请人已停用", func(t *testing.T, f *afix) error {
			id := f.p.pendingDevice(t)
			if err := store.SetAccountDisabled(ctx, f.svc.Pool, f.p.ann.ID, true, nil); err != nil {
				t.Fatal(err)
			}
			_, err := f.svc.DecideDeviceAuth(ctx, core.BotActor(f.p.root), f.p.pay, id, true)
			return err
		}, wantEvent{core.ActDeviceDeny, core.ViaTelegram, "pay", "device_auth", "new", "root",
			map[string]any{"requested": "approve", "reason": "applicant_disabled"}}},
		{"本人授权第一台设备", func(t *testing.T, f *afix) error {
			st, err := f.svc.StartDeviceAuth(ctx, []string{"claude-code"}, "first-mac")
			if err != nil {
				t.Fatal(err)
			}
			f.keep(st.PollSecret, st.Nonce)
			_, err = f.svc.AuthorizeDevice(ctx, f.p.ann, st.Nonce, true)
			return err
		}, wantEvent{core.ActDeviceSelfApprove, core.ViaWeb, "", "device_auth", "first-mac", "ann", map[string]any{"owner": "ann"}}},
		{"网页改角色", func(t *testing.T, f *afix) error {
			return f.svc.SetRole(ctx, web(f.p.payAdmin), f.p.pay, f.p.annPay.ID, store.RoleAdmin)
		}, wantEvent{core.ActMemberRole, core.ViaWeb, "pay", "member", "ann", "pay-admin", map[string]any{"from": "member", "to": "admin"}}},
		{"命令行改角色", func(t *testing.T, f *afix) error {
			if err := store.SetMemberRole(ctx, f.svc.Pool, f.p.annPay.ID, store.RoleAdmin); err != nil {
				t.Fatal(err)
			}
			return f.svc.SetRole(ctx, core.ServerCLI(), f.p.pay, f.p.payAdminMember.ID, store.RoleMember)
		}, wantEvent{core.ActMemberRole, core.ViaServerCLI, "pay", "member", "pay-admin", "", map[string]any{"from": "admin", "to": "member"}}},
		{"生成邀请", func(t *testing.T, f *afix) error {
			inv, err := f.svc.CreateInvite(ctx, web(f.p.payAdmin), f.p.pay, core.InviteSpec{Kind: store.InvitePersonal, Admin: true})
			f.keep(inv.Code)
			return err
		}, wantEvent{core.ActInviteCreate, core.ViaWeb, "pay", "invite", "", "pay-admin", map[string]any{"kind": "personal", "role": "admin", "max_uses": 1}}},
		{"作废邀请", func(t *testing.T, f *afix) error {
			inv, err := f.svc.CreateInvite(ctx, web(f.p.payAdmin), f.p.pay, core.InviteSpec{Kind: store.InviteTeam})
			if err != nil {
				t.Fatal(err)
			}
			f.keep(inv.Code)
			return f.svc.RevokeInvite(ctx, web(f.p.root), f.p.pay, inv.Invite.ID)
		}, wantEvent{core.ActInviteRevoke, core.ViaWeb, "pay", "invite", "", "root", map[string]any{"kind": "team"}}},
		{"凭个人邀请注册", func(t *testing.T, f *afix) error {
			inv, err := f.svc.CreateInvite(ctx, web(f.p.payAdmin), f.p.pay, core.InviteSpec{Kind: store.InvitePersonal})
			if err != nil {
				t.Fatal(err)
			}
			f.keep(inv.Code, "pw-eve-0123456789")
			_, err = f.svc.Register(ctx, core.RegisterInput{Code: inv.Code, Name: "eve", Password: "pw-eve-0123456789"})
			return err
		}, wantEvent{core.ActInviteUse, core.ViaWeb, "pay", "member", "eve", "eve", map[string]any{"account_created": true, "role": "member"}}},
		{"已有账号接受个人邀请", func(t *testing.T, f *afix) error {
			inv, err := f.svc.CreateInvite(ctx, web(f.p.payAdmin), f.p.pay, core.InviteSpec{Kind: store.InvitePersonal})
			if err != nil {
				t.Fatal(err)
			}
			f.keep(inv.Code)
			_, err = f.svc.AcceptInvite(ctx, f.p.opsAdmin, inv.Code)
			return err
		}, wantEvent{core.ActInviteUse, core.ViaWeb, "pay", "member", "ops-admin", "ops-admin", map[string]any{"role": "member"}}},
		{"管理员吊销设备", func(t *testing.T, f *afix) error {
			id := f.annDevice(t)
			return f.svc.RevokeDevice(ctx, web(f.p.payAdmin), f.p.pay, id)
		}, wantEvent{core.ActDeviceRevoke, core.ViaWeb, "pay", "device", "ann-mac", "pay-admin", map[string]any{"owner": "ann", "self": false}}},
		{"本人在个人中心吊销设备", func(t *testing.T, f *afix) error {
			id := f.annDevice(t)
			return f.svc.RevokeDevice(ctx, web(f.p.ann), 0, id)
		}, wantEvent{core.ActDeviceRevoke, core.ViaWeb, "", "device", "ann-mac", "ann", map[string]any{"self": true}}},
		{"本机命令行吊销旧设备", func(t *testing.T, f *afix) error {
			id := f.annDevice(t)
			return f.svc.RevokeDevice(ctx, core.MemberCLIActor(f.p.ann), 0, id)
		}, wantEvent{core.ActDeviceRevoke, core.ViaMemberCLI, "", "device", "ann-mac", "ann", map[string]any{"self": true}}},
		{"网页直接加入", func(t *testing.T, f *afix) error {
			_, err := f.svc.AddMember(ctx, web(f.p.payAdmin), f.p.pay, "ops-admin", store.RoleMember)
			return err
		}, wantEvent{core.ActMemberAdd, core.ViaWeb, "pay", "member", "ops-admin", "pay-admin", map[string]any{"role": "member"}}},
		{"命令行加入并新建账号", func(t *testing.T, f *afix) error {
			_, _, err := f.svc.AddMemberCLI(ctx, core.ServerCLI(), f.p.ops, "newbie", "新人", store.RoleAdmin)
			return err
		}, wantEvent{core.ActMemberAdd, core.ViaServerCLI, "ops", "member", "newbie", "", map[string]any{"role": "admin", "account_created": true}}},
		{"只转交", func(t *testing.T, f *afix) error {
			return f.svc.ApplyHandover(ctx, web(f.p.payAdmin), f.p.pay, f.p.annPay.ID, f.fullPlan(f.bob.ID), false)
		}, wantEvent{core.ActMemberHandover, core.ViaWeb, "pay", "member", "ann", "pay-admin",
			map[string]any{"services": []any{map[string]any{"name": "api", "to": "bob"}}}}},
		{"网页移出", func(t *testing.T, f *afix) error {
			return f.svc.ApplyHandover(ctx, web(f.p.payAdmin), f.p.pay, f.p.annPay.ID, f.fullPlan(f.bob.ID), true)
		}, wantEvent{core.ActMemberRemove, core.ViaWeb, "pay", "member", "ann", "pay-admin", map[string]any{"tokens_revoked": 1}}},
		{"命令行移出", func(t *testing.T, f *afix) error {
			plan := f.fullPlan(f.bob.ID)
			plan.As = "pay-admin"
			return f.svc.ApplyHandover(ctx, core.ServerCLI(), f.p.pay, f.p.annPay.ID, plan, true)
		}, wantEvent{core.ActMemberRemove, core.ViaServerCLI, "pay", "member", "ann", "", map[string]any{"as": "pay-admin"}}},
		{"停用账号", func(t *testing.T, f *afix) error {
			f.annDevice(t)
			return f.svc.DisableAccount(ctx, web(f.p.root), f.p.ann.ID)
		}, wantEvent{core.ActAccountDisable, core.ViaWeb, "", "account", "ann", "root", map[string]any{"devices_revoked": 1}}},
		{"命令行启用账号", func(t *testing.T, f *afix) error {
			if err := store.SetAccountDisabled(ctx, f.svc.Pool, f.p.ann.ID, true, nil); err != nil {
				t.Fatal(err)
			}
			return f.svc.EnableAccount(ctx, core.ServerCLI(), f.p.ann.ID)
		}, wantEvent{core.ActAccountEnable, core.ViaServerCLI, "", "account", "ann", "", nil}},
		{"授予服务器管理员", func(t *testing.T, f *afix) error {
			return f.svc.SetServerAdmin(ctx, web(f.p.root), f.p.ann.ID, true)
		}, wantEvent{core.ActServerAdminGrant, core.ViaWeb, "", "account", "ann", "root", nil}},
		{"撤销服务器管理员", func(t *testing.T, f *afix) error {
			if err := store.SetServerAdmin(ctx, f.svc.Pool, f.p.ann.ID, true); err != nil {
				t.Fatal(err)
			}
			return f.svc.SetServerAdmin(ctx, core.ServerCLI(), f.p.ann.ID, false)
		}, wantEvent{core.ActServerAdminRevoke, core.ViaServerCLI, "", "account", "ann", "", nil}},
		{"建工作空间", func(t *testing.T, f *afix) error {
			_, err := f.svc.CreateWorkspace(ctx, web(f.p.root), "dev", "ann")
			return err
		}, wantEvent{core.ActWorkspaceCreate, core.ViaWeb, "dev", "workspace", "dev", "root", map[string]any{"admin": "ann"}}},
		{"命令行建工作空间不指定管理员", func(t *testing.T, f *afix) error {
			_, err := f.svc.CreateWorkspace(ctx, core.ServerCLI(), "dev", "")
			return err
		}, wantEvent{core.ActWorkspaceCreate, core.ViaServerCLI, "dev", "workspace", "dev", "", nil}},
		{"删工作空间", func(t *testing.T, f *afix) error {
			if _, err := store.CreateWorkspace(ctx, f.svc.Pool, "tmp"); err != nil {
				t.Fatal(err)
			}
			return f.svc.DeleteWorkspace(ctx, web(f.p.root), "tmp")
		}, wantEvent{core.ActWorkspaceDelete, core.ViaWeb, "tmp", "workspace", "tmp", "root", nil}},
		{"改工作空间设置", func(t *testing.T, f *afix) error {
			return f.svc.UpdateWorkspace(ctx, web(f.p.payAdmin), f.p.pay, core.WorkspaceSettings{
				TelegramChat: "-1001", ShowTitle: true, ShowBrief: false})
		}, wantEvent{core.ActWorkspaceSettings, core.ViaWeb, "pay", "workspace", "pay", "pay-admin",
			map[string]any{"show_title": map[string]any{"from": false, "to": true}}}},
		{"设置页改显示名（群设置不变）", func(t *testing.T, f *afix) error {
			d := " 支付组 "
			return f.svc.UpdateWorkspace(ctx, web(f.p.payAdmin), f.p.pay, core.WorkspaceSettings{
				TelegramChat: "-1001", DisplayName: &d})
		}, wantEvent{core.ActWorkspaceDisplayName, core.ViaWeb, "pay", "workspace", "pay", "pay-admin",
			map[string]any{"display_name": map[string]any{"from": "pay", "to": "支付组"}}}},
		{"命令行改显示名", func(t *testing.T, f *afix) error {
			return f.svc.SetWorkspaceDisplayName(ctx, core.ServerCLI(), f.p.ops, "运维")
		}, wantEvent{core.ActWorkspaceDisplayName, core.ViaServerCLI, "ops", "workspace", "ops", "",
			map[string]any{"display_name": map[string]any{"from": "ops", "to": "运维"}}}},
		{"改对外地址", func(t *testing.T, f *afix) error {
			_, err := f.svc.SetExternalURL(ctx, web(f.p.root), "HTTPS://C.Example.com/")
			return err
		}, wantEvent{core.ActServerSettings, core.ViaWeb, "", "server", "对外地址", "root",
			map[string]any{"external_url": map[string]any{"from": "", "to": "https://c.example.com"}}}},
		{"命令行重置密码", func(t *testing.T, f *afix) error {
			pw, err := f.svc.ResetPassword(ctx, core.ServerCLI(), "ann")
			f.keep(pw)
			return err
		}, wantEvent{core.ActResetPassword, core.ViaServerCLI, "", "account", "ann", "", nil}},
		{"服务器管理员断开别人的 Telegram", func(t *testing.T, f *afix) error {
			tg := int64(9001)
			if err := store.SetAccountTelegram(ctx, f.svc.Pool, f.p.ann.ID, &tg, "anntg"); err != nil {
				t.Fatal(err)
			}
			return f.svc.UnlinkTelegram(ctx, web(f.p.root), f.p.ann.ID)
		}, wantEvent{core.ActTelegramUnlink, core.ViaWeb, "", "account", "ann", "root", map[string]any{"self": false}}},
		{"本人断开 Telegram", func(t *testing.T, f *afix) error {
			tg := int64(9001)
			if err := store.SetAccountTelegram(ctx, f.svc.Pool, f.p.ann.ID, &tg, "anntg"); err != nil {
				t.Fatal(err)
			}
			return f.svc.UnlinkTelegram(ctx, web(f.p.ann), f.p.ann.ID)
		}, wantEvent{core.ActTelegramUnlink, core.ViaWeb, "", "account", "ann", "ann", map[string]any{"self": true}}},
		{"本人连接 Telegram", func(t *testing.T, f *afix) error {
			l, err := f.svc.StartLink(ctx, f.p.ann.ID, core.ProviderTelegram)
			if err != nil {
				t.Fatal(err)
			}
			f.keep(l.Nonce)
			_, err = f.svc.CompleteTelegramLink(ctx, l.Nonce, 9002, "anntg")
			return err
		}, wantEvent{core.ActTelegramLink, core.ViaTelegram, "", "account", "ann", "ann", map[string]any{"telegram_username": "anntg"}}},
		{"本人连接钉钉（单聊发码后网页确认）", func(t *testing.T, f *afix) error {
			l, err := f.svc.StartLink(ctx, f.p.ann.ID, core.ProviderDingTalk)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.SubmitLinkCode(ctx, core.ProviderDingTalk, l.Nonce, core.ChatSender{Tenant: "corp1", UserID: "u-ann", Name: "安"}, true); err != nil {
				t.Fatal(err)
			}
			return f.svc.ConfirmLink(ctx, core.WebActor(f.p.ann), l.ID, true)
		}, wantEvent{core.ActDingTalkLink, core.ViaWeb, "", "account", "ann", "ann", map[string]any{"dingtalk_name": "安"}}},
		{"本人断开钉钉", func(t *testing.T, f *afix) error {
			if err := store.UpsertChatIdentity(ctx, f.svc.Pool, store.ChatIdentity{AccountID: f.p.ann.ID, Provider: store.ProviderDingTalk, UserID: "u-ann"}); err != nil {
				t.Fatal(err)
			}
			return f.svc.Unlink(ctx, core.WebActor(f.p.ann), f.p.ann.ID, core.ProviderDingTalk)
		}, wantEvent{core.ActDingTalkUnlink, core.ViaWeb, "", "account", "ann", "ann", map[string]any{"self": true}}},
		{"本人连接 Slack（私聊发码后网页确认）", func(t *testing.T, f *afix) error {
			l, err := f.svc.StartLink(ctx, f.p.ann.ID, core.ProviderSlack)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.svc.SubmitLinkCode(ctx, core.ProviderSlack, l.Nonce, core.ChatSender{Tenant: "T1", UserID: "U-ANN", Name: "ann.s"}, true); err != nil {
				t.Fatal(err)
			}
			return f.svc.ConfirmLink(ctx, core.WebActor(f.p.ann), l.ID, true)
		}, wantEvent{core.ActLinkOf(core.ProviderSlack), core.ViaWeb, "", "account", "ann", "ann", map[string]any{"slack_name": "ann.s"}}},
		{"管理员断开别人的 Slack", func(t *testing.T, f *afix) error {
			if err := store.UpsertChatIdentity(ctx, f.svc.Pool, store.ChatIdentity{AccountID: f.p.ann.ID, Provider: store.ProviderSlack, Tenant: "T1", UserID: "U-ANN"}); err != nil {
				t.Fatal(err)
			}
			return f.svc.Unlink(ctx, core.WebActor(f.p.root), f.p.ann.ID, core.ProviderSlack)
		}, wantEvent{core.ActUnlinkOf(core.ProviderSlack), core.ViaWeb, "", "account", "ann", "root", map[string]any{"self": false}}},
		{"Slack 频道里 @机器人 绑定频道", func(t *testing.T, f *afix) error {
			if err := store.DeleteWorkspaceChats(ctx, f.svc.Pool, f.p.pay, ""); err != nil {
				t.Fatal(err)
			}
			b, err := f.svc.StartChatBind(ctx, core.WebActor(f.p.payAdmin), f.p.pay, core.ProviderSlack)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.svc.CompleteChatBind(ctx, core.ProviderSlack, "C-PAY", "#pay", b.Code)
			return err
		}, wantEvent{core.ActWorkspaceChatBind, "slack", "pay", "workspace", "pay", "pay-admin", map[string]any{"chat_title": "#pay"}}},
		{"群里 @机器人 绑定钉钉群", func(t *testing.T, f *afix) error {
			if err := store.DeleteWorkspaceChats(ctx, f.svc.Pool, f.p.pay, ""); err != nil { // 夹具里 pay 绑了 Telegram 群
				t.Fatal(err)
			}
			b, err := f.svc.StartChatBind(ctx, core.WebActor(f.p.payAdmin), f.p.pay, core.ProviderDingTalk)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.svc.CompleteChatBind(ctx, core.ProviderDingTalk, "cid-pay", "支付群", b.Code)
			return err
		}, wantEvent{core.ActWorkspaceChatBind, core.ViaDingTalk, "pay", "workspace", "pay", "pay-admin", map[string]any{"chat_title": "支付群"}}},
		{"管理页解绑钉钉群", func(t *testing.T, f *afix) error {
			if err := store.SetWorkspaceChat(ctx, f.svc.Pool, store.WorkspaceChat{WorkspaceID: f.p.pay, Provider: store.ProviderDingTalk, ChatID: "cid-pay", Title: "支付群"}); err != nil {
				t.Fatal(err)
			}
			return f.svc.UnbindChat(ctx, core.WebActor(f.p.payAdmin), f.p.pay, core.ProviderDingTalk)
		}, wantEvent{core.ActWorkspaceChatUnbind, core.ViaWeb, "pay", "workspace", "pay", "pay-admin", map[string]any{"chat_title": "支付群"}}},
		{"本人改密码", func(t *testing.T, f *afix) error {
			f.keep("new-password-0123")
			return f.svc.ChangePassword(ctx, f.p.ann, "", "", "new-password-0123")
		}, wantEvent{core.ActPasswordChange, core.ViaWeb, "", "account", "ann", "ann", nil}},
		{"命令行修改账号资料", func(t *testing.T, f *afix) error {
			d, tg, id := "Ann Lee", "anntg", int64(9003)
			return f.svc.UpdateAccount(ctx, core.ServerCLI(), "ann", core.AccountUpdate{DisplayName: &d, TelegramUsername: &tg, TelegramUserID: &id})
		}, wantEvent{core.ActAccountUpdate, core.ViaServerCLI, "", "account", "ann", "",
			map[string]any{"display_name": map[string]any{"from": "", "to": "Ann Lee"}, "telegram_user_id": map[string]any{"from": nil, "to": 9003}}}},
		{"命令行签发令牌", func(t *testing.T, f *afix) error {
			tok, err := f.svc.IssueToken(ctx, core.ServerCLI(), f.p.pay, "ann", "codex")
			f.keep(tok.Plain)
			return err
		}, wantEvent{core.ActTokenIssue, core.ViaServerCLI, "pay", "token", "ann", "", map[string]any{"member": "ann", "tool": "codex"}}},
		{"命令行吊销令牌", func(t *testing.T, f *afix) error {
			plain, id, err := store.IssueToken(ctx, f.svc.Pool, f.p.annPay.ID, "codex")
			if err != nil {
				t.Fatal(err)
			}
			f.keep(plain)
			return f.svc.RevokeToken(ctx, core.ServerCLI(), f.p.pay, id)
		}, wantEvent{core.ActTokenRevoke, core.ViaServerCLI, "pay", "token", "ann", "", map[string]any{"member": "ann", "tool": "codex"}}},
		{"被拒的管理请求", func(t *testing.T, f *afix) error {
			return f.svc.RecordDenied(ctx, core.AgentActor(f.p.ann), core.DeniedAttempt{Workspace: "pay",
				Route: "POST /api/v1/w/{ws}/admin/invites", Reason: core.DenyNeedPassword, Credential: core.CredToken})
		}, wantEvent{core.ActAccessDenied, core.ViaAgent, "pay", "route", "POST /api/v1/w/{ws}/admin/invites", "ann",
			map[string]any{"reason": "need_password_login", "credential": "token"}}},
		{"命令行登记服务负责人", func(t *testing.T, f *afix) error {
			_, err := f.svc.SetServiceOwner(ctx, core.ServerCLI(), f.p.pay, "api", "bob")
			return err
		}, wantEvent{core.ActServiceOwner, core.ViaServerCLI, "pay", "service", "api", "", map[string]any{"from": "ann", "to": "bob"}}},
		{"管理员在网页上作废别人的单", func(t *testing.T, f *afix) error {
			m := f.payAdminMember
			who := store.Identity{AccountID: m.AccountID, MemberID: m.ID, WorkspaceID: f.p.pay, MemberName: m.Name, Tool: "web"}
			_, err := f.svc.CancelIssue(ctx, core.WebActor(f.p.payAdmin), who, core.CancelInput{IssueID: f.asOwner.ID,
				ExpectedVersion: f.asOwner.Version, Reason: "重复提交"})
			return err
		}, wantEvent{core.ActIssueCancel, core.ViaWeb, "pay", "issue", "给 ann 的单", "pay-admin", map[string]any{"from": "OPEN"}}},
	}
}

func jsonEq(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func checkEvent(t *testing.T, got store.AdminEvent, w wantEvent) {
	t.Helper()
	if got.Action != w.action || got.Via != w.via || got.WorkspaceName != w.ws || got.TargetKind != w.kind ||
		got.TargetName != w.target || got.ActorName != w.actor {
		t.Errorf("记录不对：得到 动作=%s 入口=%s 工作空间=%q 对象=%s/%q 操作人=%q，应为 %+v", got.Action, got.Via,
			got.WorkspaceName, got.TargetKind, got.TargetName, got.ActorName, w)
	}
	if (w.ws == "") != (got.WorkspaceID == nil) {
		t.Errorf("工作空间编号 %v 与名字 %q 不一致", got.WorkspaceID, got.WorkspaceName)
	}
	if (w.actor == "") != (got.ActorAccountID == nil) {
		t.Errorf("操作人编号 %v 与名字 %q 不一致", got.ActorAccountID, got.ActorName)
	}
	for k, v := range w.detail {
		if !jsonEq(got.Detail[k], v) {
			t.Errorf("附加信息 %s：得到 %v，应为 %v（全部：%v）", k, got.Detail[k], v, got.Detail)
		}
	}
	if core.EventText(i18n.Default(), got) == "" || core.ActionLabel(i18n.Default(), got.Action) == "" {
		t.Errorf("应能写成人话：%+v", got)
	}
}

// 每个管理方法的一次成功调用恰好留一条记录，且记录里没有任何秘密。
func TestEveryAdminMethodRecords(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range auditCases() {
		seen[c.want.action] = true
		t.Run(c.name, func(t *testing.T) {
			f := newAfix(t)
			before := len(allEvents(t, f.svc))
			// 准备步骤也可能写记录：在 run 内部先跑准备，所以只看 run 之后的最后一条，并确认 run 本身只多写了准备与操作
			if err := c.run(t, f); err != nil {
				t.Fatalf("操作失败：%v", err)
			}
			es := allEvents(t, f.svc)
			if len(es) <= before {
				t.Fatalf("没有留下记录")
			}
			got := es[len(es)-1]
			checkEvent(t, got, c.want)
			if n := countAction(es[before:], c.want.action); n != 1 && !strings.HasPrefix(c.want.action, "invite.") {
				t.Errorf("动作 %s 应恰好一条，得到 %d 条", c.want.action, n)
			}
			assertNoSecrets(t, es, f.secrets)
		})
	}
	// 动作表里的每个动作都有用例（初始管理员另有用例）
	for _, a := range core.Actions() {
		if !seen[a] && a != core.ActAccountCreate && !core.ReservedActions[a] {
			t.Errorf("动作 %s 没有用例", a)
		}
		if seen[a] && core.ReservedActions[a] {
			t.Errorf("动作 %s 已有业务用例，从 ReservedActions 里删掉", a)
		}
	}
}

func countAction(es []store.AdminEvent, action string) int {
	n := 0
	for _, e := range es {
		if e.Action == action {
			n++
		}
	}
	return n
}

// assertNoSecrets：记录的对象名与附加信息里不出现任何秘密，也不出现密码哈希；附加信息的键不带秘密字样。
func assertNoSecrets(t *testing.T, es []store.AdminEvent, secrets []string) {
	t.Helper()
	for _, e := range es {
		raw, _ := json.Marshal(e.Detail)
		text := string(raw) + "\x00" + e.TargetName + "\x00" + e.ActorName + "\x00" + core.EventText(i18n.ZhCN, e)
		for _, s := range secrets {
			if s != "" && strings.Contains(text, s) {
				t.Errorf("记录 %s 里出现了秘密 %q：%s", e.Action, s, text)
			}
		}
		for _, h := range []string{"$2a$", "$2b$", "$2y$"} {
			if strings.Contains(text, h) {
				t.Errorf("记录 %s 里出现了密码哈希：%s", e.Action, text)
			}
		}
		for k := range e.Detail {
			if strings.HasSuffix(k, "_revoked") { // 吊销条数
				continue
			}
			for _, bad := range []string{"password", "token", "secret", "code", "hash", "nonce"} {
				if strings.Contains(strings.ToLower(k), bad) {
					t.Errorf("记录 %s 的附加信息键 %q 带秘密字样", e.Action, k)
				}
			}
		}
	}
}

// 首次启动建初始管理员：入口为服务器自动，记录里没有初始密码。
func TestInitialAdminRecorded(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	svc := &core.Service{Pool: pool, DataDir: t.TempDir()}
	path, err := svc.EnsureInitialAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	es := allEvents(t, svc)
	if len(es) != 1 {
		t.Fatalf("应有一条记录：%+v", es)
	}
	checkEvent(t, es[0], wantEvent{core.ActAccountCreate, core.ViaSystem, "", "account", "admin", "", map[string]any{"initial_admin": true}})
	assertNoSecrets(t, es, []string{initialPw(t, path)})
}

// 业务失败（含权限不足、约束不满足、状态冲突）不留任何记录。
func TestFailedOperationLeavesNoRecord(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		run  func(t *testing.T, f *afix) error
	}{
		{"降级最后一名管理员", func(t *testing.T, f *afix) error {
			return f.svc.SetRole(ctx, core.WebActor(f.p.payAdmin), f.p.pay, f.p.payAdminMember.ID, store.RoleMember)
		}},
		{"移出最后一名管理员", func(t *testing.T, f *afix) error {
			return f.svc.ApplyHandover(ctx, core.WebActor(f.p.root), f.p.pay, f.p.payAdminMember.ID, core.HandoverPlan{}, true)
		}},
		{"转交清单不完整", func(t *testing.T, f *afix) error {
			plan := f.fullPlan(f.bob.ID)
			plan.Issues = plan.Issues[:1]
			return f.svc.ApplyHandover(ctx, core.WebActor(f.p.payAdmin), f.p.pay, f.p.annPay.ID, plan, true)
		}},
		{"停用最后一名服务器管理员", func(t *testing.T, f *afix) error {
			return f.svc.DisableAccount(ctx, core.ServerCLI(), f.p.root.ID)
		}},
		{"撤销最后一名服务器管理员", func(t *testing.T, f *afix) error {
			return f.svc.SetServerAdmin(ctx, core.ServerCLI(), f.p.root.ID, false)
		}},
		{"删除有问题单的工作空间", func(t *testing.T, f *afix) error {
			return f.svc.DeleteWorkspace(ctx, core.WebActor(f.p.root), "pay")
		}},
		{"非管理员改角色", func(t *testing.T, f *afix) error {
			return f.svc.SetRole(ctx, core.WebActor(f.p.ann), f.p.pay, f.p.annPay.ID, store.RoleAdmin)
		}},
		{"非管理员生成邀请", func(t *testing.T, f *afix) error {
			_, err := f.svc.CreateInvite(ctx, core.WebActor(f.p.ann), f.p.pay, core.InviteSpec{Kind: store.InviteTeam})
			return err
		}},
		{"作废不存在的邀请", func(t *testing.T, f *afix) error {
			return f.svc.RevokeInvite(ctx, core.WebActor(f.p.payAdmin), f.p.pay, 99999)
		}},
		{"重复审批", func(t *testing.T, f *afix) error {
			id := f.teamSignup(t, "dora")
			if _, err := f.svc.DecideSignup(ctx, core.WebActor(f.p.payAdmin), f.p.pay, id, true); err != nil {
				t.Fatal(err)
			}
			_, err := f.svc.DecideSignup(ctx, core.WebActor(f.p.payAdmin), f.p.pay, id, false)
			return err
		}},
		{"群绑定冲突", func(t *testing.T, f *afix) error {
			return f.svc.UpdateWorkspace(ctx, core.WebActor(f.p.opsAdmin), f.p.ops, core.WorkspaceSettings{TelegramChat: "-1001"})
		}},
		{"重置不存在的账号", func(t *testing.T, f *afix) error {
			_, err := f.svc.ResetPassword(ctx, core.ServerCLI(), "nobody")
			return err
		}},
		{"网页上的非服务器管理员重置密码", func(t *testing.T, f *afix) error {
			_, err := f.svc.ResetPassword(ctx, core.WebActor(f.p.payAdmin), "ann")
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newAfix(t)
			// 准备步骤可能写记录，用一个前置的“准备完成”计数：重复审批的第一步会留一条
			if err := c.run(t, f); err == nil {
				t.Fatal("操作应失败")
			}
			for _, e := range allEvents(t, f.svc) {
				// 准备阶段（生成邀请、第一次审批）的记录允许存在，失败的那一步不能留
				switch e.Action {
				case core.ActInviteCreate, core.ActSignupApprove:
					continue
				}
				t.Errorf("失败的操作留下了记录：%s %s", e.Action, core.EventText(i18n.ZhCN, e))
			}
			if c.name == "重复审批" && countAction(allEvents(t, f.svc), core.ActSignupApprove) != 1 {
				t.Error("重复审批只应留第一次的记录")
			}
		})
	}
}

// 没有实际变化的请求不留记录。
func TestNoopLeavesNoRecord(t *testing.T) {
	ctx := context.Background()
	f := newAfix(t)
	ws, err := store.GetWorkspace(ctx, f.svc.Pool, f.p.pay)
	if err != nil {
		t.Fatal(err)
	}
	steps := []func() error{
		func() error { return f.svc.SetServerAdmin(ctx, core.WebActor(f.p.root), f.p.root.ID, true) },
		func() error {
			return f.svc.SetRole(ctx, core.WebActor(f.p.payAdmin), f.p.pay, f.p.annPay.ID, store.RoleMember)
		},
		func() error {
			return f.svc.UpdateWorkspace(ctx, core.WebActor(f.p.payAdmin), f.p.pay, core.WorkspaceSettings{
				TelegramChat: ws.Notify.Target, ShowTitle: ws.Notify.ShowTitle, ShowBrief: ws.Notify.ShowBrief})
		},
		func() error { return f.svc.UnlinkTelegram(ctx, core.WebActor(f.p.ann), f.p.ann.ID) },
		func() error {
			_, err := f.svc.SetServiceOwner(ctx, core.ServerCLI(), f.p.pay, "api", "ann")
			return err
		},
		func() error {
			d := ""
			return f.svc.UpdateAccount(ctx, core.ServerCLI(), "ann", core.AccountUpdate{DisplayName: &d})
		},
	}
	for i, s := range steps {
		if err := s(); err != nil {
			t.Fatalf("第 %d 步：%v", i+1, err)
		}
	}
	if es := allEvents(t, f.svc); len(es) != 0 {
		t.Fatalf("没有变化的请求不应留记录：%s", core.EventText(i18n.ZhCN, es[0]))
	}
}

// 入口为空时操作失败，业务也不生效。
func TestRecordRejectsUnknownVia(t *testing.T) {
	ctx := context.Background()
	f := newAfix(t)
	err := f.svc.SetRole(ctx, core.Actor{Account: &f.p.payAdmin}, f.p.pay, f.p.annPay.ID, store.RoleAdmin)
	if err == nil || !strings.Contains(err.Error(), "入口") {
		t.Fatalf("应报缺少入口：%v", err)
	}
	// 编程工具令牌的身份不能做任何管理操作，只能出现在被拒的请求里
	err = f.svc.SetRole(ctx, core.AgentActor(f.p.payAdmin), f.p.pay, f.p.annPay.ID, store.RoleAdmin)
	if err == nil || !strings.Contains(err.Error(), "编程工具令牌") {
		t.Fatalf("编程工具令牌应被拒：%v", err)
	}
	m, err := store.MemberByID(ctx, f.svc.Pool, f.p.annPay.ID)
	if err != nil || m.Role != store.RoleMember {
		t.Fatalf("角色不应改变：%+v %v", m, err)
	}
}

// 两名管理员同时审批同一条申请：只有一个成功，只有一条记录。
func TestConcurrentDecideRecordsOnce(t *testing.T) {
	ctx := context.Background()
	f := newAfix(t)
	signup := f.teamSignup(t, "dora")
	device := f.p.pendingDevice(t)
	run := func(fns ...func() error) int {
		var wg sync.WaitGroup
		var mu sync.Mutex
		ok := 0
		for _, fn := range fns {
			wg.Add(1)
			go func(fn func() error) {
				defer wg.Done()
				if err := fn(); err == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				} else if !errors.Is(err, core.ErrDecided) {
					t.Errorf("后到的应报已处理：%v", err)
				}
			}(fn)
		}
		wg.Wait()
		return ok
	}
	if n := run(
		func() error {
			_, err := f.svc.DecideSignup(ctx, core.WebActor(f.p.payAdmin), f.p.pay, signup, true)
			return err
		},
		func() error {
			_, err := f.svc.DecideSignup(ctx, core.BotActor(f.p.root), f.p.pay, signup, false)
			return err
		},
	); n != 1 {
		t.Fatalf("注册申请应只有一个审批成功，得到 %d", n)
	}
	if n := run(
		func() error {
			_, err := f.svc.DecideDeviceAuth(ctx, core.WebActor(f.p.payAdmin), f.p.pay, device, true)
			return err
		},
		func() error {
			_, err := f.svc.DecideDeviceAuth(ctx, core.BotActor(f.p.root), f.p.pay, device, false)
			return err
		},
	); n != 1 {
		t.Fatalf("设备申请应只有一个审批成功，得到 %d", n)
	}
	es := allEvents(t, f.svc)
	if n := countAction(es, core.ActSignupApprove) + countAction(es, core.ActSignupReject); n != 1 {
		t.Errorf("注册申请应只有一条审批记录，得到 %d", n)
	}
	if n := countAction(es, core.ActDeviceApprove) + countAction(es, core.ActDeviceDeny); n != 1 {
		t.Errorf("设备申请应只有一条审批记录，得到 %d", n)
	}
}

// 已决的注册申请不再保留密码哈希。
func TestDecidedSignupDropsPasswordHash(t *testing.T) {
	ctx := context.Background()
	f := newAfix(t)
	rejected := f.teamSignup(t, "dora")
	approved := f.teamSignup(t, "carl")
	if _, err := f.svc.DecideSignup(ctx, core.WebActor(f.p.payAdmin), f.p.pay, rejected, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.DecideSignup(ctx, core.WebActor(f.p.payAdmin), f.p.pay, approved, true); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := f.svc.Pool.QueryRow(ctx, `SELECT count(*) FROM signups WHERE password_hash IS NOT NULL`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("已决申请的密码哈希应清掉，还剩 %d 条（%v）", n, err)
	}
	// 批准的人仍能用注册时的密码登录（哈希已复制进账号表）
	if _, err := f.svc.Login(ctx, "carl", "pw-carl-0123456789"); err != nil {
		t.Fatalf("批准后应能登录：%v", err)
	}
}

// 每个动作在每种入口下都能写成人话，有中文名和类别；类别筛选覆盖全部动作。
func TestEventTextCoversEveryAction(t *testing.T) {
	inCat := map[string]bool{}
	for _, c := range core.Categories {
		for _, a := range core.CategoryActions(c.ID) {
			inCat[a] = true
		}
	}
	id := int64(1)
	for _, a := range core.Actions() {
		if core.ActionLabel(i18n.ZhCN, a) == "" || !inCat[a] {
			t.Errorf("动作 %s 缺中文名或类别", a)
		}
		for _, via := range core.ViaIDs {
			for _, actor := range []string{"lee", ""} {
				e := store.AdminEvent{Action: a, Via: via, ActorName: actor, TargetName: "ann", TargetID: &id, Detail: map[string]any{}}
				txt := core.EventText(i18n.ZhCN, e)
				if txt == "" || strings.Contains(txt, "%!") || strings.Contains(txt, a) {
					t.Errorf("%s/%s 的人话不对：%q", a, via, txt)
				}
			}
		}
	}
	e := store.AdminEvent{Action: core.ActSignupApprove, Via: core.ViaTelegram, ActorName: "lee", Detail: map[string]any{"applicant": "tester-d"}}
	if got := core.EventText(i18n.ZhCN, e); got != "lee 在群里批准了 tester-d 的注册申请" {
		t.Errorf("人话：%q", got)
	}
	e = store.AdminEvent{Action: core.ActAccessDenied, Via: core.ViaAgent, ActorName: "lee", TargetName: "POST /api/v1/w/{ws}/admin/invites",
		Detail: map[string]any{"reason": core.DenyNeedPassword}}
	if got := core.EventText(i18n.ZhCN, e); got != "lee 用编程工具令牌调用管理接口 POST /api/v1/w/{ws}/admin/invites，被拒绝：管理操作只认账号密码登录的网页" {
		t.Errorf("人话：%q", got)
	}
	// 预留给 M6-d 的两个动作
	e = store.AdminEvent{Action: core.ActWorkspaceDisplayName, Via: core.ViaWeb, ActorName: "lee", TargetName: "pay",
		Detail: map[string]any{"display_name": map[string]any{"from": "支付", "to": "支付组"}}}
	if got := core.EventText(i18n.ZhCN, e); got != "lee 在网页上把工作空间 pay 的显示名从“支付”改为“支付组”" {
		t.Errorf("人话：%q", got)
	}
	e = store.AdminEvent{Action: core.ActServerSettings, Via: core.ViaWeb, ActorName: "lee",
		Detail: map[string]any{"external_url": map[string]any{"from": "", "to": "https://c.example"}}}
	if got := core.EventText(i18n.ZhCN, e); got != "lee 在网页上修改了服务器设置：对外地址" {
		t.Errorf("人话：%q", got)
	}
	e = store.AdminEvent{Action: core.ActResetPassword, Via: core.ViaServerCLI, TargetName: "tester-a"}
	if got := core.EventText(i18n.ZhCN, e); got != "服务器命令行重置了 tester-a 的密码" {
		t.Errorf("人话：%q", got)
	}
}
