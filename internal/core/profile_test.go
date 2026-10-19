package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// 本人改昵称：可改可清；与同空间其他在册成员的登录名或昵称撞名（不区分大小写、忽略首尾空白）时拒绝，
// 只说撞的是登录名还是昵称；已移出、已停用、不同空间的人不算；改了才记录，记录是“本人改了自己的昵称，从什么改成什么”。
func TestSetMyDisplayName(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	q := e.svc.Pool
	me, _ := store.AccountByID(ctx, q, e.a.AccountID)
	other, _ := store.AccountByID(ctx, q, e.b.AccountID) // lee-codex，同在 default
	if err := store.SetAccountDisplayName(ctx, q, other.ID, "Codex Bot"); err != nil {
		t.Fatal(err)
	}
	act := core.WebActor(me)
	events := func() []store.AdminEvent {
		es, err := store.ListAdminEvents(ctx, q, store.AdminEventFilter{Actions: []string{core.ActAccountUpdate}, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		return es
	}
	taken := func(display, want string) {
		t.Helper()
		_, err := e.svc.SetMyDisplayName(ctx, act, display)
		var te *core.DisplayNameTakenError
		if !errors.As(err, &te) || te.Clash != want {
			t.Fatalf("%q 应因撞%s被拒：%v", display, want, err)
		}
		if strings.Contains(i18n.Message(i18n.EN, err), "lee-codex") || strings.Contains(i18n.Message(i18n.ZhCN, err), "lee-codex") {
			t.Fatalf("报错不应说出是谁：%v", err)
		}
	}

	// 改成新昵称：去首尾空白后保存，记一条本人的记录
	got, err := e.svc.SetMyDisplayName(ctx, act, "  Claude 李  ")
	if err != nil || got != "Claude 李" {
		t.Fatalf("改昵称：%q %v", got, err)
	}
	if a, _ := store.AccountByID(ctx, q, me.ID); a.DisplayName != "Claude 李" || a.Name != "lee-claude" {
		t.Fatalf("应只改昵称不改登录名：%+v", a)
	}
	es := events()
	if len(es) != 1 {
		t.Fatalf("应记一条：%d", len(es))
	}
	ev := es[0]
	dn, _ := ev.Detail["display_name"].(map[string]any)
	if ev.Via != core.ViaWeb || ev.ActorName != "lee-claude" || ev.TargetName != "lee-claude" || ev.WorkspaceID != nil ||
		ev.Detail["self"] != true || dn["from"] != "" || dn["to"] != "Claude 李" {
		t.Fatalf("记录不对：%+v", ev)
	}
	if txt := core.EventText(i18n.ZhCN, ev); !strings.Contains(txt, "自己") {
		t.Fatalf("记录应写成本人改了自己的：%s", txt)
	}

	// 原样再存一次：不记录
	if _, err := e.svc.SetMyDisplayName(ctx, act, "Claude 李"); err != nil || len(events()) != 1 {
		t.Fatalf("没改不应记录：%v %d", err, len(events()))
	}

	// 冒充：别人的登录名、昵称，大小写或首尾空白不同也算
	taken("lee-codex", store.ClashName)
	taken("LEE-Codex", store.ClashName)
	taken(" lee-codex\t", store.ClashName)
	taken("codex bot", store.ClashDisplayName)
	taken("CODEX BOT", store.ClashDisplayName)
	if a, _ := store.AccountByID(ctx, q, me.ID); a.DisplayName != "Claude 李" || len(events()) != 1 {
		t.Fatalf("被拒时不应改动也不应记录：%q %d", a.DisplayName, len(events()))
	}

	// 自己的登录名可以用
	if _, err := e.svc.SetMyDisplayName(ctx, act, "LEE-CLAUDE"); err != nil {
		t.Fatalf("用自己的登录名应可以：%v", err)
	}

	// 不可见字符拒绝
	for _, s := range []string{"lee​-codex", "a‮b", "a\x07b"} {
		var ie *core.InputError
		if _, err := e.svc.SetMyDisplayName(ctx, act, s); !errors.As(err, &ie) {
			t.Fatalf("%q 应被拒：%v", s, err)
		}
	}

	// 其他空间的人不算；同空间已移出、账号停用的不算
	ws2, err := store.CreateWorkspace(ctx, q, "ops")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMember(ctx, q, ws2.ID, "zoe", "Zoe Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetMyDisplayName(ctx, act, "zoe"); err != nil {
		t.Fatalf("不同空间的人不算：%v", err)
	}
	if _, err := e.svc.SetMyDisplayName(ctx, act, "zoe z"); err != nil {
		t.Fatalf("不同空间的人不算：%v", err)
	}
	// 加入 ops 后 zoe 就算了（原样保存当前昵称不检查，所以先换一个）
	if _, err := e.svc.SetMyDisplayName(ctx, act, "Neutral"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddMembership(ctx, q, me.ID, ws2.ID, store.RoleMember, nil); err != nil {
		t.Fatal(err)
	}
	taken("Zoe", store.ClashName)
	taken("zoe z", store.ClashDisplayName)
	gone, err := store.CreateMember(ctx, q, e.a.WorkspaceID, "gone", "Gone G")
	if err != nil {
		t.Fatal(err)
	}
	taken("gone", store.ClashName)
	if err := store.RemoveMembership(ctx, q, gone.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetMyDisplayName(ctx, act, "gone"); err != nil {
		t.Fatalf("已移出的不算：%v", err)
	}
	if _, err := q.Exec(ctx, `UPDATE accounts SET disabled_at = now() WHERE id = $1`, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetMyDisplayName(ctx, act, "lee-codex"); err != nil {
		t.Fatalf("已停用的不算：%v", err)
	}

	// 清空：保存为空串，记录从什么改成空
	n := len(events())
	if got, err := e.svc.SetMyDisplayName(ctx, act, "   "); err != nil || got != "" {
		t.Fatalf("清空：%q %v", got, err)
	}
	if a, _ := store.AccountByID(ctx, q, me.ID); a.DisplayName != "" {
		t.Fatalf("应已清空：%q", a.DisplayName)
	}
	if es := events(); len(es) != n+1 || es[0].Detail["display_name"].(map[string]any)["to"] != "" {
		t.Fatalf("清空也应记录：%d %+v", len(es), es[0])
	}

	// 改名后按名字找人立即用新昵称
	if _, err := e.svc.SetMyDisplayName(ctx, act, "Sunny"); err != nil {
		t.Fatal(err)
	}
	ms, _, err := e.svc.FindMembers(ctx, e.b, "sunny")
	if err != nil || len(ms) != 1 || ms[0].Name != "lee-claude" || !ms[0].Exact {
		t.Fatalf("找人应读到新昵称：%+v %v", ms, err)
	}
}
