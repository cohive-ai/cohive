package core_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// nickEnv 在 newEnv 的两位成员之外加几位带昵称的成员：
// guanyu（关羽，唯一昵称）、zhang-a 与 zhang-b（都叫 Zhang，昵称重名）、
// liubei（登录名），而 xuande 的昵称恰好也是 liubei。
func nickEnv(t *testing.T) (env, int64) {
	t.Helper()
	ctx := context.Background()
	e := newEnv(t)
	ws := e.a.WorkspaceID
	for _, m := range [][2]string{{"guanyu", "关羽"}, {"zhang-a", "Zhang"}, {"zhang-b", "zhang"},
		{"liubei", "刘备"}, {"xuande", "liubei"}} {
		if _, err := store.CreateMember(ctx, e.svc.Pool, ws, m[0], m[1]); err != nil {
			t.Fatal(err)
		}
	}
	return e, ws
}

func names(ms []store.MemberMatch) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return out
}

func choiceErr(t *testing.T, err error, code string, want ...string) *core.MemberChoiceError {
	t.Helper()
	var ce *core.MemberChoiceError
	if !errors.As(err, &ce) {
		t.Fatalf("应返回成员待确认错误，得到：%v", err)
	}
	if ce.ErrorCode() != code {
		t.Fatalf("错误码应为 %s，得到 %s", code, ce.ErrorCode())
	}
	var got []string
	for _, m := range ce.Candidates {
		got = append(got, m.Name)
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("候选应为 %v，得到 %v", want, got)
	}
	for _, l := range i18n.Supported() {
		msg := ce.Localize(l)
		for _, n := range want {
			if !strings.Contains(msg, n) {
				t.Errorf("%s 报错应列出候选 %s：%s", l, n, msg)
			}
		}
		if strings.Contains(msg, "core.") {
			t.Errorf("%s 报错留有未翻译的键：%s", l, msg)
		}
	}
	return ce
}

func TestFindMembersByNickname(t *testing.T) {
	ctx := context.Background()
	e, _ := nickEnv(t)

	got, more, err := e.svc.FindMembers(ctx, e.a, " 关羽 ")
	if err != nil {
		t.Fatal(err)
	}
	if more || len(got) != 1 || got[0].Name != "guanyu" || got[0].DisplayName != "关羽" || !got[0].Exact ||
		got[0].Role != store.RoleMember {
		t.Fatalf("唯一昵称应查到 guanyu：%+v", got)
	}

	// 登录名大小写不同也能查到
	if got, _, _ := e.svc.FindMembers(ctx, e.a, "GuanYu"); len(got) != 1 || got[0].Name != "guanyu" || !got[0].Exact {
		t.Fatalf("登录名查找应不区分大小写：%+v", got)
	}

	// 昵称重名返回多个候选，完全相同的在前，包含匹配在后
	// azhang 按字母排在最前，但只是包含匹配，应排在完全相同的后面
	for _, m := range [][2]string{{"zhangfei", "张飞"}, {"azhang", ""}} {
		if _, err := store.CreateMember(ctx, e.svc.Pool, e.a.WorkspaceID, m[0], m[1]); err != nil {
			t.Fatal(err)
		}
	}
	got, _, err = e.svc.FindMembers(ctx, e.a, "ZHANG")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names(got), ",") != "zhang-a,zhang-b,azhang,zhangfei" || !got[0].Exact || !got[1].Exact || got[2].Exact {
		t.Fatalf("昵称重名应返回两位完全相同的候选，再是包含匹配：%+v", got)
	}

	// 检索词里的 % 和 _ 按原样匹配，不当通配符
	if got, _, _ := e.svc.FindMembers(ctx, e.a, "%"); len(got) != 0 {
		t.Fatalf("%% 不应匹配任何人：%v", names(got))
	}
	if got, _, _ := e.svc.FindMembers(ctx, e.a, "_"); len(got) != 0 {
		t.Fatalf("_ 不应匹配任何人：%v", names(got))
	}

	// 返回负责的服务
	got, _, _ = e.svc.FindMembers(ctx, e.a, "lee-codex")
	if len(got) != 1 || strings.Join(got[0].Services, ",") != "order-api" {
		t.Fatalf("应带上负责的服务：%+v", got)
	}
}

func TestFindMembersOnlyActiveInWorkspace(t *testing.T) {
	ctx := context.Background()
	e, ws := nickEnv(t)
	pool := e.svc.Pool

	other, err := store.CreateWorkspace(ctx, pool, "other")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateMember(ctx, pool, other.ID, "guan-other", "关羽"); err != nil {
		t.Fatal(err)
	}
	gone, err := store.CreateMember(ctx, pool, ws, "guan-gone", "关羽")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RemoveMembership(ctx, pool, gone.ID, nil); err != nil {
		t.Fatal(err)
	}
	off, err := store.CreateMember(ctx, pool, ws, "guan-off", "关羽")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccountDisabled(ctx, pool, off.AccountID, true, nil); err != nil {
		t.Fatal(err)
	}

	got, _, err := e.svc.FindMembers(ctx, e.a, "guan")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names(got), ",") != "guanyu" {
		t.Fatalf("只应返回本工作空间在册、未停用的成员：%v", names(got))
	}
	// 被排除的同昵称成员也不算候选：按昵称指派仍只列 guanyu
	_, err = e.svc.Draft(ctx, e.a, core.DraftInput{Title: "t", Owner: "关羽"})
	choiceErr(t, err, core.CodeMemberNickname, "guanyu")
}

func TestFindMembersLimit(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	for i := range core.MemberSearchLimit + 1 {
		if _, err := store.CreateMember(ctx, e.svc.Pool, e.a.WorkspaceID, fmt.Sprintf("bulk-%02d", i), ""); err != nil {
			t.Fatal(err)
		}
	}
	got, more, err := e.svc.FindMembers(ctx, e.a, "bulk")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != core.MemberSearchLimit || !more {
		t.Fatalf("应截断到 %d 条并标出还有更多：%d 条，more=%v", core.MemberSearchLimit, len(got), more)
	}
	got, more, _ = e.svc.FindMembers(ctx, e.a, "")
	if len(got) != core.MemberSearchLimit || !more || got[0].Exact {
		t.Fatalf("空检索词列出全部（截断），且不算完全相同：%d 条，more=%v", len(got), more)
	}
}

func TestAssignByNicknameIsBlocked(t *testing.T) {
	ctx := context.Background()
	e, _ := nickEnv(t)

	// 起草：昵称唯一也不直接指派
	_, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "t", Owner: "关羽"})
	choiceErr(t, err, core.CodeMemberNickname, "guanyu")
	// 登录名大小写不对也要确认
	_, err = e.svc.Draft(ctx, e.a, core.DraftInput{Title: "t", Owner: "GuanYu"})
	choiceErr(t, err, core.CodeMemberNickname, "guanyu")
	// 昵称重名
	_, err = e.svc.Draft(ctx, e.a, core.DraftInput{Title: "t", Owner: "zhang"})
	choiceErr(t, err, core.CodeMemberNickname, "zhang-a", "zhang-b")

	// 发出：liubei 是登录名，又是 xuande 的昵称
	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "无负责人"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Act(ctx, e.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit,
		ExpectedVersion: d.Version, Assignee: "liubei"})
	choiceErr(t, err, core.CodeMemberAmbiguous, "liubei", "xuande")
	if got, _, _ := e.svc.Get(ctx, e.a, d.ID); got.Status != issue.StatusDraft || got.OwnerID != nil {
		t.Fatalf("被拦下的发出不应改动问题单：%+v", got)
	}

	// 转交：同样拦下
	is := e.act(t, e.a, e.draft(t), issue.ActSubmit)
	_, err = e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActTransfer,
		ExpectedVersion: is.Version, Assignee: "liubei", Note: "转给前端"})
	choiceErr(t, err, core.CodeMemberAmbiguous, "liubei", "xuande")
	_, err = e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActTransfer,
		ExpectedVersion: is.Version, Assignee: "刘备", Note: "转给前端"})
	choiceErr(t, err, core.CodeMemberNickname, "liubei")

	// 确认后用登录名重试：昵称没撞的登录名照旧成功
	got, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActTransfer,
		ExpectedVersion: is.Version, Assignee: "xuande", Note: "转给前端"})
	if err != nil || got.OwnerName != "xuande" {
		t.Fatalf("用登录名转交应成功：%+v %v", got, err)
	}
}

func TestAssignByLoginNameStillWorks(t *testing.T) {
	ctx := context.Background()
	e, _ := nickEnv(t)

	d, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "t", Owner: "guanyu"})
	if err != nil || d.OwnerName != "guanyu" {
		t.Fatalf("用登录名起草应成功：%+v %v", d, err)
	}
	d, err = e.svc.Draft(ctx, e.a, core.DraftInput{Title: "无负责人"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: d.ID, Action: issue.ActSubmit,
		ExpectedVersion: d.Version, Assignee: "zhang-b"})
	if err != nil || got.OwnerName != "zhang-b" {
		t.Fatalf("用登录名发出应成功：%+v %v", got, err)
	}

	// 名字谁都对不上：照旧报成员不存在
	_, err = e.svc.Draft(ctx, e.a, core.DraftInput{Title: "t", Owner: "nobody"})
	var ie *core.InputError
	var ce *core.MemberChoiceError
	if !errors.As(err, &ie) || errors.As(err, &ce) || !strings.Contains(err.Error(), "nobody") {
		t.Fatalf("对不上任何人时应报成员不存在：%v", err)
	}
}

// 登录名所属的账号已停用、而它又是别人的昵称时，只列能指派的候选。
func TestAssignDisabledLoginWithNicknameClash(t *testing.T) {
	ctx := context.Background()
	e, _ := nickEnv(t)
	m, err := store.MemberByName(ctx, e.svc.Pool, e.a.WorkspaceID, "liubei")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetAccountDisabled(ctx, e.svc.Pool, m.AccountID, true, nil); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Draft(ctx, e.a, core.DraftInput{Title: "t", Owner: "liubei"})
	choiceErr(t, err, core.CodeMemberAmbiguous, "xuande")

	// 没有昵称冲突时照旧报已停用
	g, _ := store.MemberByName(ctx, e.svc.Pool, e.a.WorkspaceID, "guanyu")
	if err := store.SetAccountDisabled(ctx, e.svc.Pool, g.AccountID, true, nil); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Draft(ctx, e.a, core.DraftInput{Title: "t", Owner: "guanyu"})
	var ce *core.MemberChoiceError
	if err == nil || errors.As(err, &ce) {
		t.Fatalf("停用账号应报不能指派：%v", err)
	}
}
