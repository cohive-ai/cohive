package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

const groupChat = "-1001234"

// joinEnv 在默认工作空间上配置群，并把 lee-codex 设为管理员、绑定 Telegram 编号 900。
func joinEnv(t *testing.T) env {
	t.Helper()
	ctx := context.Background()
	e := newEnv(t)
	if err := store.SetWorkspaceNotify(ctx, e.svc.Pool, e.a.WorkspaceID,
		store.NotifyConfig{Channel: "telegram", Target: groupChat, ShowTitle: true, ShowBrief: true}, ""); err != nil {
		t.Fatal(err)
	}
	admin := int64(900)
	if err := store.SetMemberTelegramUserID(ctx, e.svc.Pool, e.b.MemberID, &admin); err != nil {
		t.Fatal(err)
	}
	if err := store.SetMemberRole(ctx, e.svc.Pool, e.b.MemberID, store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestJoinNeedsApproval(t *testing.T) {
	ctx := context.Background()
	e := joinEnv(t)

	res, err := e.svc.RequestJoin(ctx, groupChat, 501, "Ann_Dev", "Ann")
	if err != nil {
		t.Fatal(err)
	}
	if res.Request == nil || res.Member != nil || res.Workspace.Name != "default" || res.NoAdmin {
		t.Fatalf("默认需要审批，应只建申请：%+v", res)
	}
	again, err := e.svc.RequestJoin(ctx, groupChat, 501, "Ann_Dev", "Ann")
	if err != nil || again.Request == nil || again.Request.ID != res.Request.ID {
		t.Fatalf("重复申请应返回同一条待审申请：%+v %v", again, err)
	}

	if _, err := e.svc.DecideJoin(ctx, res.Request.ID, 501, true); !errors.Is(err, core.ErrNotAdmin) {
		t.Fatalf("非管理员不能审批，得到 %v", err)
	}
	d, err := e.svc.DecideJoin(ctx, res.Request.ID, 900, true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Member == nil || d.Member.Name != "ann_dev" || d.Decider.Name != "lee-codex" {
		t.Fatalf("批准后应建成员 ann_dev：%+v", d)
	}
	m, err := store.MemberByName(ctx, e.svc.Pool, e.a.WorkspaceID, "ann_dev")
	if err != nil || m.TelegramUserID == nil || *m.TelegramUserID != 501 || m.TelegramUsername != "Ann_Dev" || m.Role != store.RoleMember {
		t.Fatalf("新成员应绑定 Telegram 编号与用户名：%+v %v", m, err)
	}
	if _, err := e.svc.DecideJoin(ctx, res.Request.ID, 900, true); !errors.Is(err, core.ErrJoinDecided) {
		t.Fatalf("已处理的申请不能再处理，得到 %v", err)
	}

	already, err := e.svc.RequestJoin(ctx, groupChat, 501, "Ann_Dev", "Ann")
	if err != nil || already.AlreadyMember == nil || already.AlreadyMember.Name != "ann_dev" {
		t.Fatalf("已是成员时应直接提示：%+v %v", already, err)
	}
}

func TestJoinRejected(t *testing.T) {
	ctx := context.Background()
	e := joinEnv(t)
	res, err := e.svc.RequestJoin(ctx, groupChat, 502, "bob", "Bob")
	if err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.DecideJoin(ctx, res.Request.ID, 900, false)
	if err != nil || d.Member != nil {
		t.Fatalf("拒绝时不应建成员：%+v %v", d, err)
	}
	if _, err := store.MemberByName(ctx, e.svc.Pool, e.a.WorkspaceID, "bob"); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("被拒绝的人不应成为成员")
	}
	// 被拒绝后可以再次申请
	again, err := e.svc.RequestJoin(ctx, groupChat, 502, "bob", "Bob")
	if err != nil || again.Request == nil || again.Request.ID == res.Request.ID {
		t.Fatalf("被拒绝后应能重新申请：%+v %v", again, err)
	}
}

func TestJoinWithoutApproval(t *testing.T) {
	ctx := context.Background()
	e := joinEnv(t)
	if err := store.SetJoinApproval(ctx, e.svc.Pool, e.a.WorkspaceID, false); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.RequestJoin(ctx, groupChat, 503, "", "Carol")
	if err != nil {
		t.Fatal(err)
	}
	if res.Member == nil || res.Member.Name != "tg503" {
		t.Fatalf("关闭审批时应直接加入；没有用户名的人用 tg 加编号做成员名：%+v", res)
	}
}

func TestJoinNameCollision(t *testing.T) {
	ctx := context.Background()
	e := joinEnv(t)
	if err := store.SetJoinApproval(ctx, e.svc.Pool, e.a.WorkspaceID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMember(ctx, e.svc.Pool, e.a.WorkspaceID, "dave", ""); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.RequestJoin(ctx, groupChat, 504, "Dave", "Dave")
	if err != nil || res.Member == nil || res.Member.Name != "dave-2" {
		t.Fatalf("成员名冲突时应加后缀：%+v %v", res, err)
	}
}

func TestJoinUnknownGroup(t *testing.T) {
	e := joinEnv(t)
	if _, err := e.svc.RequestJoin(context.Background(), "-999", 505, "x_user", "X"); !errors.Is(err, core.ErrNoWorkspaceForChat) {
		t.Fatalf("没有对应工作空间的群应报错，得到 %v", err)
	}
}

func TestJoinWithoutAdminAndManualAdd(t *testing.T) {
	ctx := context.Background()
	e := joinEnv(t)
	if err := store.SetMemberRole(ctx, e.svc.Pool, e.b.MemberID, store.RoleMember); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.RequestJoin(ctx, groupChat, 777, "gina", "Gina")
	if err != nil || !res.NoAdmin {
		t.Fatalf("没有管理员时应标明：%+v %v", res, err)
	}
	// 申请期间管理员已手工建好成员并绑定：批准时复用，不再新建
	m, err := store.CreateMember(ctx, e.svc.Pool, e.a.WorkspaceID, "gina-manual", "")
	if err != nil {
		t.Fatal(err)
	}
	uid := int64(777)
	if err := store.SetMemberTelegramUserID(ctx, e.svc.Pool, m.ID, &uid); err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.DecideJoinByOperator(ctx, res.Request.ID, true)
	if err != nil || d.Member == nil || d.Member.ID != m.ID || d.Decider.Name != "服务器管理员" {
		t.Fatalf("应复用手工加入的成员：%+v %v", d, err)
	}
	if _, err := store.MemberByName(ctx, e.svc.Pool, e.a.WorkspaceID, "gina"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不应再建 gina：%v", err)
	}
}
