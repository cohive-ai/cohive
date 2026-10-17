package notify_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
)

// 群里的审批按钮（注册、加入、设备 × 批准、拒绝）与私聊连接 Telegram 都留记录，入口为群按钮，
// 审批的工作空间为群绑定的工作空间。用真实数据库与真实业务层，只把 Telegram 接口换成假的。
func TestGroupButtonsRecord(t *testing.T) {
	ctx := context.Background()
	type setup struct {
		svc *core.Service
		ws  int64
		ann store.Account
	}
	newSetup := func(t *testing.T) setup {
		t.Helper()
		pool := testutil.NewSchemaPool(t)
		if err := db.Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
		svc := &core.Service{Pool: pool}
		ws, err := store.CreateWorkspace(ctx, pool, "pay")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetWorkspaceNotify(ctx, pool, ws.ID, store.NotifyConfig{Channel: "telegram", Target: "-1001"}, ""); err != nil {
			t.Fatal(err)
		}
		lee, err := store.CreateAccount(ctx, pool, "lee", "")
		if err != nil {
			t.Fatal(err)
		}
		tg := int64(900)
		if err := store.SetAccountTelegram(ctx, pool, lee.ID, &tg, "leetg"); err != nil {
			t.Fatal(err)
		}
		if _, err := store.AddMembership(ctx, pool, lee.ID, ws.ID, store.RoleAdmin, nil); err != nil {
			t.Fatal(err)
		}
		ann, err := store.CreateAccount(ctx, pool, "ann", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AddMembership(ctx, pool, ann.ID, ws.ID, store.RoleMember, nil); err != nil {
			t.Fatal(err)
		}
		return setup{svc: svc, ws: ws.ID, ann: ann}
	}
	signup := func(t *testing.T, s setup) int64 {
		inv, err := store.CreateInvite(ctx, s.svc.Pool, store.Invite{WorkspaceID: s.ws, Kind: store.InviteTeam, Role: store.RoleMember,
			MaxUses: 5, ExpiresAt: farFuture()}, []byte("h"))
		if err != nil {
			t.Fatal(err)
		}
		sg, err := store.CreateSignup(ctx, s.svc.Pool, store.Signup{InviteID: inv.ID, WorkspaceID: s.ws, Name: "dora", PasswordHash: "h"})
		if err != nil {
			t.Fatal(err)
		}
		return sg.ID
	}
	join := func(t *testing.T, s setup) int64 {
		bob, err := store.CreateAccount(ctx, s.svc.Pool, "bob", "")
		if err != nil {
			t.Fatal(err)
		}
		inv, err := store.CreateInvite(ctx, s.svc.Pool, store.Invite{WorkspaceID: s.ws, Kind: store.InviteTeam, Role: store.RoleMember,
			MaxUses: 5, ExpiresAt: farFuture()}, []byte("h2"))
		if err != nil {
			t.Fatal(err)
		}
		sg, err := store.CreateJoinRequest(ctx, s.svc.Pool, inv.ID, s.ws, bob.ID)
		if err != nil {
			t.Fatal(err)
		}
		return sg.ID
	}
	device := func(t *testing.T, s setup) int64 {
		if _, err := store.CreateDevice(ctx, s.svc.Pool, s.ann.ID, "old"); err != nil {
			t.Fatal(err)
		}
		st, err := s.svc.StartDeviceAuth(ctx, []string{"codex"}, "new")
		if err != nil {
			t.Fatal(err)
		}
		a, err := s.svc.AuthorizeDevice(ctx, s.ann, st.Nonce, true)
		if err != nil {
			t.Fatal(err)
		}
		return a.ID
	}
	cases := []struct {
		name, kind, yn, action string
		mk                     func(*testing.T, setup) int64
	}{
		{"批准注册", "su", "y", core.ActSignupApprove, signup},
		{"拒绝注册", "su", "n", core.ActSignupReject, signup},
		{"批准加入", "su", "y", core.ActJoinApprove, join},
		{"拒绝加入", "su", "n", core.ActJoinReject, join},
		{"批准设备", "da", "y", core.ActDeviceApprove, device},
		{"拒绝设备", "da", "n", core.ActDeviceDeny, device},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newSetup(t)
			id := c.mk(t, s)
			api := &fakeAPI{}
			bot := &notify.Bot{Core: s.svc, API: api, Name: "CohiveBot"}
			bot.Handle(ctx, callbackIn(fmt.Sprintf("%s:%d:%d:%s", c.kind, s.ws, id, c.yn), 900, -1001))
			es, err := store.ListAdminEvents(ctx, s.svc.Pool, store.AdminEventFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(es) != 1 {
				t.Fatalf("应留一条记录，得到 %d 条（回应：%v）", len(es), api.answers)
			}
			e := es[0]
			if e.Action != c.action || e.Via != core.ViaTelegram || e.WorkspaceName != "pay" || e.ActorName != "lee" {
				t.Fatalf("记录：%s 入口 %s 工作空间 %q 操作人 %q（%s）", e.Action, e.Via, e.WorkspaceName, e.ActorName, core.EventText(e))
			}
		})
	}

	t.Run("没有权限的人点审批按钮", func(t *testing.T) {
		s := newSetup(t)
		id := signup(t, s)
		tg := int64(901)
		if err := store.SetAccountTelegram(ctx, s.svc.Pool, s.ann.ID, &tg, "anntg"); err != nil {
			t.Fatal(err)
		}
		api := &fakeAPI{}
		(&notify.Bot{Core: s.svc, API: api, Name: "CohiveBot"}).Handle(ctx, callbackIn(fmt.Sprintf("su:%d:%d:y", s.ws, id), 901, -1001))
		es, err := store.ListAdminEvents(ctx, s.svc.Pool, store.AdminEventFilter{})
		if err != nil || len(es) != 1 {
			t.Fatalf("应留一条被拒记录：%+v %v", es, err)
		}
		e := es[0]
		if e.Action != core.ActAccessDenied || e.Via != core.ViaTelegram || e.ActorName != "ann" || e.WorkspaceName != "pay" ||
			e.Detail["reason"] != core.DenyNoPermission {
			t.Fatalf("记录：%+v（%s）", e, core.EventText(e))
		}
		sg, err := store.GetSignupForUpdate(ctx, s.svc.Pool, id)
		if err != nil || sg.Status != store.SignupPending {
			t.Fatalf("申请不应被处理：%+v %v", sg, err)
		}
	})

	t.Run("私聊连接 Telegram", func(t *testing.T) {
		s := newSetup(t)
		l, err := s.svc.StartLink(ctx, s.ann.ID, core.ProviderTelegram)
		if err != nil {
			t.Fatal(err)
		}
		(&notify.Bot{Core: s.svc, API: &fakeAPI{}, Name: "CohiveBot"}).Handle(ctx, msg("/start "+l.Nonce, "private", 42, "anntg"))
		es, err := store.ListAdminEvents(ctx, s.svc.Pool, store.AdminEventFilter{})
		if err != nil || len(es) != 1 || es[0].Action != core.ActTelegramLink || es[0].Via != core.ViaTelegram || es[0].ActorName != "ann" {
			t.Fatalf("连接 Telegram 应留一条群按钮入口的记录：%+v %v", es, err)
		}
	})
}

func farFuture() time.Time { return time.Now().Add(24 * time.Hour) }
