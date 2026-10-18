package server_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/httpapi"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// auditWeb 是网页入口测试的夹具：lee-codex 是服务器管理员兼 default 的管理员，lee-claude 是 default 的成员，
// 两人都设了密码并用密码登录。
type auditWeb struct {
	f             fixture
	admin, member *browser
	claude, codex store.Account
}

func newAuditWeb(t *testing.T) *auditWeb {
	t.Helper()
	ctx := context.Background()
	f := newFixture(t, builtWeb)
	q := f.svc.Pool
	if err := store.SetMemberRole(ctx, q, f.b.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := store.SetServerAdmin(ctx, q, f.b.AccountID, true); err != nil {
		t.Fatal(err)
	}
	setPassword(t, f, f.b.AccountID, "admin-password-1")
	setPassword(t, f, f.a.AccountID, "member-password-1")
	w := &auditWeb{f: f, admin: loginAs(t, f, "lee-codex", "admin-password-1"), member: loginAs(t, f, "lee-claude", "member-password-1")}
	w.claude, _ = store.AccountByID(ctx, q, f.a.AccountID)
	w.codex, _ = store.AccountByID(ctx, q, f.b.AccountID)
	return w
}

func lastEvent(t *testing.T, svc *core.Service) (store.AdminEvent, int) {
	t.Helper()
	es, err := store.ListAdminEvents(context.Background(), svc.Pool, store.AdminEventFilter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(es) == 0 {
		return store.AdminEvent{}, 0
	}
	return es[0], len(es)
}

// pairDevice 给账号配对一台首台设备，返回设备编号与设备密钥。
func (w *auditWeb) pairDevice(t *testing.T, a store.Account, name string) (int64, string) {
	t.Helper()
	ctx := context.Background()
	st, err := w.f.svc.StartDeviceAuth(ctx, []string{"codex"}, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.f.svc.AuthorizeDevice(ctx, a, st.Nonce, true); err != nil {
		t.Fatal(err)
	}
	d, err := w.f.svc.PollDeviceAuth(ctx, st.RequestID, st.PollSecret)
	if err != nil {
		t.Fatal(err)
	}
	return d.DeviceID, d.DeviceSecret
}

// pendingDevice 让 lee-claude 申请第二台设备，返回申请编号。
func (w *auditWeb) pendingDevice(t *testing.T) int64 {
	t.Helper()
	ctx := context.Background()
	w.pairDevice(t, w.claude, "old")
	st, err := w.f.svc.StartDeviceAuth(ctx, []string{"codex"}, "new")
	if err != nil {
		t.Fatal(err)
	}
	a, err := w.f.svc.AuthorizeDevice(ctx, w.claude, st.Nonce, true)
	if err != nil || a.Status != store.AuthAwaitingAdmin {
		t.Fatalf("应等管理员批准：%+v %v", a, err)
	}
	return a.ID
}

func (w *auditWeb) invite(t *testing.T, kind string) core.CreatedInvite {
	t.Helper()
	inv, err := w.f.svc.CreateInvite(context.Background(), core.WebActor(w.codex), w.f.a.WorkspaceID, core.InviteSpec{Kind: kind})
	if err != nil {
		t.Fatal(err)
	}
	return inv
}

// pendingIssue 给 lee-claude 派一张待接单的问题单，返回转交清单。
func (w *auditWeb) handoverPlan(t *testing.T) map[string]any {
	t.Helper()
	ctx := context.Background()
	d, err := w.f.svc.Draft(ctx, w.f.b, core.DraftInput{Title: "给 claude 的单", Owner: "lee-claude"})
	if err != nil {
		t.Fatal(err)
	}
	is, err := w.f.svc.Act(ctx, w.f.b, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{"issues": []any{map[string]any{"id": is.ID, "as": "owner", "to_member_id": w.f.b.MemberID,
		"expected_version": is.Version}}, "services": []any{}}
}

type webCase struct {
	pattern string // 注册的接口，用于检查每个写接口都有归类
	name    string
	// do 发请求，返回状态码
	do     func(t *testing.T, w *auditWeb) int
	action string
	via    string
	ws     string
	actor  string
}

func webCases() []webCase {
	ctx := context.Background()
	id := func(n int64) string { return fmt.Sprint(n) }
	const wa = "/api/v1/w/default/admin"
	return []webCase{
		{"POST /api/v1/w/{ws}/admin/signups/{id}", "工作空间管理页批准注册申请", func(t *testing.T, w *auditWeb) int {
			inv := w.invite(t, store.InviteTeam)
			res, err := w.f.svc.Register(ctx, core.RegisterInput{Code: inv.Code, Name: "dora", Password: "dora-password-1"})
			if err != nil {
				t.Fatal(err)
			}
			code, _ := w.admin.do("POST", wa+"/signups/"+id(res.Signup.ID), map[string]any{"approve": true}, "")
			return code
		}, core.ActSignupApprove, core.ViaWeb, "default", "lee-codex"},
		{"POST /api/v1/w/{ws}/admin/device-auths/{id}", "工作空间管理页批准设备", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", wa+"/device-auths/"+id(w.pendingDevice(t)), map[string]any{"approve": true}, "")
			return code
		}, core.ActDeviceApprove, core.ViaWeb, "default", "lee-codex"},
		{"POST /api/v1/server/device-auths/{id}", "服务器管理页拒绝设备", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", "/api/v1/server/device-auths/"+id(w.pendingDevice(t)), map[string]any{"approve": false}, "")
			return code
		}, core.ActDeviceDeny, core.ViaWeb, "", "lee-codex"},
		{"POST /api/v1/w/{ws}/admin/members/{id}", "改角色", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", wa+"/members/"+id(w.f.a.MemberID), map[string]any{"role": "admin"}, "")
			return code
		}, core.ActMemberRole, core.ViaWeb, "default", "lee-codex"},
		{"POST /api/v1/w/{ws}/admin/invites", "生成邀请", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", wa+"/invites", map[string]any{"kind": "team"}, "")
			return code
		}, core.ActInviteCreate, core.ViaWeb, "default", "lee-codex"},
		{"POST /api/v1/w/{ws}/admin/invites/{id}/revoke", "作废邀请", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", wa+"/invites/"+id(w.invite(t, store.InviteTeam).Invite.ID)+"/revoke", map[string]any{}, "")
			return code
		}, core.ActInviteRevoke, core.ViaWeb, "default", "lee-codex"},
		{"POST /api/v1/w/{ws}/admin/devices/{id}/revoke", "工作空间管理页吊销设备", func(t *testing.T, w *auditWeb) int {
			dev, _ := w.pairDevice(t, w.claude, "mac")
			code, _ := w.admin.do("POST", wa+"/devices/"+id(dev)+"/revoke", map[string]any{}, "")
			return code
		}, core.ActDeviceRevoke, core.ViaWeb, "default", "lee-codex"},
		{"POST /api/v1/me/devices/{id}/revoke", "个人中心吊销自己的设备", func(t *testing.T, w *auditWeb) int {
			dev, _ := w.pairDevice(t, w.claude, "mac")
			code, _ := w.member.do("POST", "/api/v1/me/devices/"+id(dev)+"/revoke", map[string]any{}, "")
			return code
		}, core.ActDeviceRevoke, core.ViaWeb, "", "lee-claude"},
		{"POST /api/v1/me/devices/{id}/revoke", "本机命令行用设备密钥吊销", func(t *testing.T, w *auditWeb) int {
			dev, secret := w.pairDevice(t, w.claude, "mac")
			b := newBrowser(t, w.f.url)
			b.origin = ""
			code, _ := b.do("POST", "/api/v1/me/devices/"+id(dev)+"/revoke", map[string]any{}, secret)
			return code
		}, core.ActDeviceRevoke, core.ViaMemberCLI, "", "lee-claude"},
		{"POST /api/v1/w/{ws}/admin/members", "直接加入", func(t *testing.T, w *auditWeb) int {
			if _, err := store.CreateAccount(ctx, w.f.svc.Pool, "ann", ""); err != nil {
				t.Fatal(err)
			}
			code, _ := w.admin.do("POST", wa+"/members", map[string]any{"account": "ann"}, "")
			return code
		}, core.ActMemberAdd, core.ViaWeb, "default", "lee-codex"},
		{"POST /api/v1/w/{ws}/admin/members/{id}/handover", "只转交", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", wa+"/members/"+id(w.f.a.MemberID)+"/handover", w.handoverPlan(t), "")
			return code
		}, core.ActMemberHandover, core.ViaWeb, "default", "lee-codex"},
		{"POST /api/v1/w/{ws}/admin/members/{id}/remove", "移出", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", wa+"/members/"+id(w.f.a.MemberID)+"/remove", w.handoverPlan(t), "")
			return code
		}, core.ActMemberRemove, core.ViaWeb, "default", "lee-codex"},
		{"POST /api/v1/server/accounts/{name}/disable", "停用", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", "/api/v1/server/accounts/lee-claude/disable", map[string]any{}, "")
			return code
		}, core.ActAccountDisable, core.ViaWeb, "", "lee-codex"},
		{"POST /api/v1/server/accounts/{name}/enable", "启用", func(t *testing.T, w *auditWeb) int {
			if err := store.SetAccountDisabled(ctx, w.f.svc.Pool, w.claude.ID, true, nil); err != nil {
				t.Fatal(err)
			}
			code, _ := w.admin.do("POST", "/api/v1/server/accounts/lee-claude/enable", map[string]any{}, "")
			return code
		}, core.ActAccountEnable, core.ViaWeb, "", "lee-codex"},
		{"POST /api/v1/server/accounts/{name}", "授予服务器管理员", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", "/api/v1/server/accounts/lee-claude", map[string]any{"server_admin": true}, "")
			return code
		}, core.ActServerAdminGrant, core.ViaWeb, "", "lee-codex"},
		{"POST /api/v1/server/workspaces", "建工作空间", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", "/api/v1/server/workspaces", map[string]any{"name": "ops"}, "")
			return code
		}, core.ActWorkspaceCreate, core.ViaWeb, "ops", "lee-codex"},
		{"DELETE /api/v1/server/workspaces/{name}", "删工作空间", func(t *testing.T, w *auditWeb) int {
			if _, err := store.CreateWorkspace(ctx, w.f.svc.Pool, "tmp"); err != nil {
				t.Fatal(err)
			}
			code, _ := w.admin.do("DELETE", "/api/v1/server/workspaces/tmp", nil, "")
			return code
		}, core.ActWorkspaceDelete, core.ViaWeb, "tmp", "lee-codex"},
		{"POST /api/v1/w/{ws}/admin/workspace", "改工作空间设置", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", wa+"/workspace", map[string]any{"show_title": true}, "")
			return code
		}, core.ActWorkspaceSettings, core.ViaWeb, "default", "lee-codex"},
		{"POST /api/v1/w/{ws}/admin/workspace", "改工作空间显示名", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", wa+"/workspace", map[string]any{"display_name": "默认组"}, "")
			return code
		}, core.ActWorkspaceDisplayName, core.ViaWeb, "default", "lee-codex"},
		{"POST /api/v1/server/settings", "改对外地址", func(t *testing.T, w *auditWeb) int {
			code, _ := w.admin.do("POST", "/api/v1/server/settings", map[string]any{"external_url": "https://c.example.com"}, "")
			return code
		}, core.ActServerSettings, core.ViaWeb, "", "lee-codex"},
		{"DELETE /api/v1/server/accounts/{name}/telegram", "服务器管理页断开 Telegram", func(t *testing.T, w *auditWeb) int {
			tg := int64(77)
			if err := store.SetAccountTelegram(ctx, w.f.svc.Pool, w.claude.ID, &tg, "claudetg"); err != nil {
				t.Fatal(err)
			}
			code, _ := w.admin.do("DELETE", "/api/v1/server/accounts/lee-claude/telegram", nil, "")
			return code
		}, core.ActTelegramUnlink, core.ViaWeb, "", "lee-codex"},
		{"DELETE /api/v1/server/accounts/{name}/links/{provider}", "服务器管理页断开通讯工具", func(t *testing.T, w *auditWeb) int {
			tg := int64(78)
			if err := store.SetAccountTelegram(ctx, w.f.svc.Pool, w.claude.ID, &tg, "claudetg"); err != nil {
				t.Fatal(err)
			}
			code, _ := w.admin.do("DELETE", "/api/v1/server/accounts/lee-claude/links/telegram", nil, "")
			return code
		}, core.ActTelegramUnlink, core.ViaWeb, "", "lee-codex"},
		{"POST /api/v1/me/links/{id}/confirm", "个人中心确认连接钉钉", func(t *testing.T, w *auditWeb) int {
			l, err := w.f.svc.StartLink(ctx, w.claude.ID, core.ProviderDingTalk)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.f.svc.SubmitLinkCode(ctx, core.ProviderDingTalk, l.Nonce, core.ChatSender{Tenant: "corp1", UserID: "u-claude", Name: "克"}, true); err != nil {
				t.Fatal(err)
			}
			code, _ := w.member.do("POST", fmt.Sprintf("/api/v1/me/links/%d/confirm", l.ID), map[string]any{"accept": true}, "")
			return code
		}, core.ActDingTalkLink, core.ViaWeb, "", "lee-claude"},
		{"DELETE /api/v1/w/{ws}/admin/chats/{provider}", "管理页解绑钉钉群", func(t *testing.T, w *auditWeb) int {
			ws, _ := store.DefaultWorkspace(ctx, w.f.svc.Pool)
			if err := store.SetWorkspaceChat(ctx, w.f.svc.Pool, store.WorkspaceChat{WorkspaceID: ws, Provider: store.ProviderDingTalk, ChatID: "cid-d", Title: "群"}); err != nil {
				t.Fatal(err)
			}
			code, _ := w.admin.do("DELETE", "/api/v1/w/default/admin/chats/dingtalk", nil, "")
			return code
		}, core.ActWorkspaceChatUnbind, core.ViaWeb, "default", "lee-codex"},
		{"DELETE /api/v1/me/links/{provider}", "个人中心断开 Telegram", func(t *testing.T, w *auditWeb) int {
			tg := int64(77)
			if err := store.SetAccountTelegram(ctx, w.f.svc.Pool, w.claude.ID, &tg, "claudetg"); err != nil {
				t.Fatal(err)
			}
			code, _ := w.member.do("DELETE", "/api/v1/me/links/telegram", nil, "")
			return code
		}, core.ActTelegramUnlink, core.ViaWeb, "", "lee-claude"},
		{"POST /api/v1/device/auth/{nonce}", "本人授权第一台设备", func(t *testing.T, w *auditWeb) int {
			st, err := w.f.svc.StartDeviceAuth(ctx, []string{"codex"}, "first")
			if err != nil {
				t.Fatal(err)
			}
			code, _ := w.member.do("POST", "/api/v1/device/auth/"+st.Nonce, map[string]any{"approve": true}, "")
			return code
		}, core.ActDeviceSelfApprove, core.ViaWeb, "", "lee-claude"},
		{"POST /api/v1/auth/password", "本人改密码", func(t *testing.T, w *auditWeb) int {
			code, _ := w.member.do("POST", "/api/v1/auth/password", map[string]any{"old_password": "member-password-1",
				"new_password": "member-password-2"}, "")
			return code
		}, core.ActPasswordChange, core.ViaWeb, "", "lee-claude"},
		{"POST /api/v1/invites/{code}/accept", "已有账号接受个人邀请", func(t *testing.T, w *auditWeb) int {
			if _, err := w.f.svc.CreateWorkspace(ctx, core.WebActor(w.codex), "ops", ""); err != nil {
				t.Fatal(err)
			}
			ops, _ := store.WorkspaceByName(ctx, w.f.svc.Pool, "ops")
			inv, err := w.f.svc.CreateInvite(ctx, core.WebActor(w.codex), ops.ID, core.InviteSpec{Kind: store.InvitePersonal})
			if err != nil {
				t.Fatal(err)
			}
			code, _ := w.member.do("POST", "/api/v1/invites/"+inv.Code+"/accept", map[string]any{}, "")
			return code
		}, core.ActInviteUse, core.ViaWeb, "ops", "lee-claude"},
		{"POST /api/v1/register", "凭个人邀请注册", func(t *testing.T, w *auditWeb) int {
			inv := w.invite(t, store.InvitePersonal)
			code, _ := newBrowser(t, w.f.url).do("POST", "/api/v1/register", map[string]any{"code": inv.Code, "name": "eve",
				"password": "eve-password-1"}, "")
			return code
		}, core.ActInviteUse, core.ViaWeb, "default", "eve"},
		{"POST /api/v1/w/{ws}/issues/{id}/cancel", "管理员作废已接单的单", func(t *testing.T, w *auditWeb) int {
			d, err := w.f.svc.Draft(ctx, w.f.a, core.DraftInput{Title: "下单接口 500", Owner: "lee-codex", Actual: "x"})
			if err != nil {
				t.Fatal(err)
			}
			is, err := w.f.svc.Act(ctx, w.f.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit, ExpectedVersion: d.Version})
			if err == nil {
				is, err = w.f.svc.Act(ctx, w.f.b, core.ActInput{IssueID: d.ID, Action: issue.ActAccept, ExpectedVersion: is.Version})
			}
			if err != nil {
				t.Fatal(err)
			}
			code, _ := w.admin.do("POST", fmt.Sprintf("/api/v1/w/default/issues/%d/cancel", d.ID),
				map[string]any{"expected_version": is.Version, "reason": "重复提交"}, "")
			return code
		}, core.ActIssueCancel, core.ViaWeb, "default", "lee-codex"},
	}
}

// notAdminWrites 是不属于管理操作、不留记录的写接口，各附理由。新增写接口必须归到上表或这里。
var notAdminWrites = map[string]string{
	"POST /api/v1/auth/login":              "登录：不改变权限，量大",
	"POST /api/v1/auth/logout":             "登出：同上",
	"POST /api/v1/me/language":             "改本人账号语言：个人偏好，不改变权限",
	"POST /api/v1/auth/web-login":          "cohive web 换一次性登录码：本人已配对的电脑免密打开网页，不改变权限",
	"POST /api/v1/device/start":            "终端发起设备授权：还没有身份，授权与审批另有记录",
	"POST /api/v1/device/poll":             "终端轮询领取：领取的是已批准（已记录）的授权",
	"POST /api/v1/device/tokens":           "设备密钥为本人已加入的工作空间补签令牌：设备授权与加入时已记录",
	"POST /api/v1/me/links":                "发起通讯工具连接：Telegram 连接完成时在机器人那边记录，钉钉在网页确认时记录",
	"POST /api/v1/w/{ws}/admin/chat-binds": "生成钉钉群绑定码：绑定完成时（群里 @机器人）记录",
	"DELETE /api/v1/w/{ws}/issues/{id}":    "删除本人草稿：草稿只有本人看得见，不是管理操作",
}

// 网页上每个管理入口都留记录，入口、工作空间、操作人都对。
func TestEveryWebAdminEndpointRecords(t *testing.T) {
	for _, c := range webCases() {
		t.Run(c.name, func(t *testing.T) {
			w := newAuditWeb(t)
			if code := c.do(t, w); code >= 300 {
				t.Fatalf("请求失败：%d", code)
			}
			e, _ := lastEvent(t, w.f.svc)
			if e.Action != c.action || e.Via != c.via || e.WorkspaceName != c.ws || e.ActorName != c.actor {
				t.Fatalf("最后一条记录：%s 入口 %s 工作空间 %q 操作人 %q，应为 %s %s %q %q（%s）", e.Action, e.Via, e.WorkspaceName,
					e.ActorName, c.action, c.via, c.ws, c.actor, core.EventText(i18n.Default(), e))
			}
		})
	}
}

// 每个写接口要么在上表（必须留记录），要么在 notAdminWrites（附理由）。以后新增接口不归类就红。
func TestNoUnlistedWriteEndpoint(t *testing.T) {
	newFixture(t, builtWeb) // 注册全部接口
	listed := map[string]bool{}
	for _, c := range webCases() {
		listed[c.pattern] = true
	}
	n := 0
	for _, p := range httpapi.Routes() {
		if strings.HasPrefix(p, "GET ") || strings.HasPrefix(p, "HEAD ") {
			continue
		}
		n++
		if !listed[p] && notAdminWrites[p] == "" {
			t.Errorf("写接口 %s 没有归类：管理操作要写记录并加进 webCases，否则加进 notAdminWrites 并写明理由", p)
		}
	}
	if n < 25 {
		t.Fatalf("只找到 %d 个写接口，接口登记可能失效", n)
	}
	for p := range listed {
		found := false
		for _, r := range httpapi.Routes() {
			found = found || r == p
		}
		if !found {
			t.Errorf("表里的接口 %s 没有注册（写错了？）", p)
		}
	}
}

// 已认证的身份调管理接口被拒要记（台账 D7 的威胁模型）：编程工具令牌、设备密钥、cohive web 免密会话被挡，
// 密码会话但没有权限；记接口模板，不记路径里的实际值；未认证的不记；同一身份同一接口一分钟内只记一条。
func TestDeniedAdminRequestsRecorded(t *testing.T) {
	ctx := context.Background()
	w := newAuditWeb(t)
	f := w.f
	_, before := lastEvent(t, f.svc)
	expect := func(name, via, cred, reason, route, ws, actor string) {
		t.Helper()
		e, n := lastEvent(t, f.svc)
		if n != before+1 {
			t.Fatalf("%s：应新增一条记录，得到 %d 条", name, n-before)
		}
		before = n
		if e.Action != core.ActAccessDenied || e.Via != via || e.Detail["credential"] != cred || e.Detail["reason"] != reason ||
			e.TargetName != route || e.WorkspaceName != ws || e.ActorName != actor {
			t.Fatalf("%s：%+v（%s）", name, e, core.EventText(i18n.Default(), e))
		}
	}
	none := func(name string) {
		t.Helper()
		if _, n := lastEvent(t, f.svc); n != before {
			t.Fatalf("%s：不应留记录", name)
		}
	}

	// 编程工具令牌（lee-claude 的）想生成邀请
	tok := newBrowser(t, f.url)
	tok.origin = ""
	tok.do("POST", "/api/v1/w/default/admin/invites", map[string]any{"kind": "team"}, f.tokenA)
	expect("令牌", core.ViaAgent, core.CredToken, core.DenyNeedPassword, "POST /api/v1/w/{ws}/admin/invites", "default", "lee-claude")

	// 令牌想替人接受邀请：记接口模板，路径里的邀请码不进记录
	inv := w.invite(t, store.InvitePersonal)
	_, before = lastEvent(t, f.svc)
	tok.do("POST", "/api/v1/invites/"+inv.Code+"/accept", map[string]any{}, f.tokenA)
	expect("令牌接受邀请", core.ViaAgent, core.CredToken, core.DenyNeedPassword, "POST /api/v1/invites/{code}/accept", "", "lee-claude")
	if e, _ := lastEvent(t, f.svc); strings.Contains(fmt.Sprint(e), inv.Code) {
		t.Fatal("邀请码进了记录")
	}

	// 设备密钥调服务器管理接口
	_, secret := w.pairDevice(t, w.claude, "mac")
	_, before = lastEvent(t, f.svc)
	tok.do("GET", "/api/v1/server/accounts", nil, secret)
	expect("设备密钥", core.ViaMemberCLI, core.CredDevice, core.DenyNeedPassword, "GET /api/v1/server/accounts", "", "lee-claude")

	// cohive web 免密会话想改角色
	once, err := f.svc.CreateWebLoginCode(ctx, f.b.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	dev := newBrowser(t, f.url)
	if resp, err := dev.client.Get(f.url + "/auth/once/" + once); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	_, before = lastEvent(t, f.svc)
	dev.do("POST", "/api/v1/w/default/admin/members/1", map[string]any{"role": "admin"}, "")
	expect("免密会话", core.ViaWeb, core.CredDeviceWeb, core.DenyNeedPassword, "POST /api/v1/w/{ws}/admin/members/{id}", "default", "lee-codex")

	// 密码会话但不是管理员
	w.member.do("POST", "/api/v1/w/default/admin/invites", map[string]any{"kind": "team"}, "")
	expect("非管理员", core.ViaWeb, core.CredPassword, core.DenyNoPermission, "POST /api/v1/w/{ws}/admin/invites", "default", "lee-claude")

	// 不记：未认证、不是成员的工作空间（404）、管理员正常操作、普通接口被拒
	newBrowser(t, f.url).do("POST", "/api/v1/w/default/admin/invites", map[string]any{"kind": "team"}, "")
	none("未认证")
	w.member.do("GET", "/api/v1/w/nope/admin/events", nil, "")
	none("不存在的工作空间")
	w.admin.do("GET", "/api/v1/w/default/admin/members", nil, "")
	none("管理员读成员列表")

	// 限频：同一身份同一接口一分钟内只记一条；换接口或换凭据照记
	_, before = lastEvent(t, f.svc)
	for i := 0; i < 5; i++ {
		tok.do("GET", "/api/v1/w/default/admin/members", nil, f.tokenA)
	}
	expect("同一接口第一次", core.ViaAgent, core.CredToken, core.DenyNeedPassword, "GET /api/v1/w/{ws}/admin/members", "default", "lee-claude")
	tok.do("GET", "/api/v1/w/default/admin/members", nil, f.tokenA)
	none("同一身份同一接口一分钟内的重复")
	tok.do("GET", "/api/v1/w/default/admin/devices", nil, f.tokenA)
	expect("换接口", core.ViaAgent, core.CredToken, core.DenyNeedPassword, "GET /api/v1/w/{ws}/admin/devices", "default", "lee-claude")
	w.member.do("GET", "/api/v1/w/default/admin/members", nil, "")
	expect("同一账号换凭据", core.ViaWeb, core.CredPassword, core.DenyNoPermission, "GET /api/v1/w/{ws}/admin/members", "default", "lee-claude")
}
