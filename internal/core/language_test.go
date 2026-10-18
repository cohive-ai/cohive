package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// 账号语言：本人可设可清，代码规范成语言包的代码；不认识的语言按参数错误拒绝。
func TestSetMyLanguage(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	acc, err := store.AccountByID(ctx, e.svc.Pool, e.a.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	if acc.Language != "" {
		t.Fatalf("新账号语言应为空：%q", acc.Language)
	}
	if got, err := e.svc.SetMyLanguage(ctx, acc, "zh"); err != nil || got != "zh-CN" {
		t.Fatalf("zh 应规范成 zh-CN：%q %v", got, err)
	}
	if acc, _ = store.AccountByID(ctx, e.svc.Pool, e.a.AccountID); acc.Language != "zh-CN" {
		t.Fatalf("应已保存：%q", acc.Language)
	}
	var ie *core.InputError
	if _, err := e.svc.SetMyLanguage(ctx, acc, "klingon"); !errors.As(err, &ie) || ie.Localize(i18n.EN) != `Unsupported language klingon` {
		t.Fatalf("不认识的语言：%v", err)
	}
	if _, err := e.svc.SetMyLanguage(ctx, acc, ""); err != nil {
		t.Fatal(err)
	}
	if acc, _ = store.AccountByID(ctx, e.svc.Pool, e.a.AccountID); acc.Language != "" {
		t.Fatalf("清除后应为空：%q", acc.Language)
	}
}

// 工作空间语言：经网页设置与服务器命令行都记管理操作记录；不改时不记；指向空串表示跟随服务器缺省。
func TestWorkspaceLanguage(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ws, _ := store.DefaultWorkspace(ctx, e.svc.Pool)
	en := "en"
	if err := e.svc.UpdateWorkspace(ctx, core.ServerCLI(), ws, core.WorkspaceSettings{ShowTitle: true, ShowBrief: true, Language: &en}); err != nil {
		t.Fatal(err)
	}
	w, _ := store.GetWorkspace(ctx, e.svc.Pool, ws)
	if w.Language != "en" {
		t.Fatalf("应已保存 en：%q", w.Language)
	}
	evs, err := store.ListAdminEvents(ctx, e.svc.Pool, store.AdminEventFilter{Limit: 10})
	if err != nil || len(evs) == 0 || evs[0].Action != core.ActWorkspaceSettings || evs[0].Detail["language"] == nil {
		t.Fatalf("应记下语言修改：%v %v", evs, err)
	}
	n := len(evs)
	if err := e.svc.SetWorkspaceLanguage(ctx, core.ServerCLI(), ws, "en"); err != nil {
		t.Fatal(err)
	}
	if evs, _ = store.ListAdminEvents(ctx, e.svc.Pool, store.AdminEventFilter{Limit: 10}); len(evs) != n {
		t.Fatalf("没变化不应再记：%d → %d", n, len(evs))
	}
	if err := e.svc.SetWorkspaceLanguage(ctx, core.ServerCLI(), ws, ""); err != nil {
		t.Fatal(err)
	}
	if w, _ = store.GetWorkspace(ctx, e.svc.Pool, ws); w.Language != "" {
		t.Fatalf("清除后应为空：%q", w.Language)
	}
	bad := "xx-unknown-lang"
	if err := e.svc.UpdateWorkspace(ctx, core.ServerCLI(), ws, core.WorkspaceSettings{Language: &bad}); err == nil {
		t.Fatal("不认识的语言应被拒绝")
	}
}

// 注册时记下界面语言：个人邀请直接写到账号上，团队邀请记在申请上、获批建账号时写入；不认识的语言忽略。
func TestRegisterRecordsLanguage(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	ws, _ := store.DefaultWorkspace(ctx, e.svc.Pool)
	personal, err := e.svc.CreateInvite(ctx, core.ServerCLI(), ws, core.InviteSpec{Kind: store.InvitePersonal})
	if err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Register(ctx, core.RegisterInput{Code: personal.Code, Name: "newbie", Password: "newbie-password-1", Language: "en-US"})
	if err != nil {
		t.Fatal(err)
	}
	if acc, _ := store.AccountByID(ctx, e.svc.Pool, res.Account.ID); acc.Language != "en" {
		t.Fatalf("个人邀请注册应记下 en：%q", acc.Language)
	}
	team, err := e.svc.CreateInvite(ctx, core.ServerCLI(), ws, core.InviteSpec{Kind: store.InviteTeam})
	if err != nil {
		t.Fatal(err)
	}
	res, err = e.svc.Register(ctx, core.RegisterInput{Code: team.Code, Name: "teamer", Password: "teamer-password-1", Language: "zh-Hans"})
	if err != nil || res.Signup == nil || res.Signup.Language != "zh-CN" {
		t.Fatalf("团队邀请应记在申请上：%v %v", res.Signup, err)
	}
	dec, err := e.svc.DecideSignup(ctx, core.ServerCLI(), ws, res.Signup.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if acc, _ := store.AccountByID(ctx, e.svc.Pool, dec.Account.ID); acc.Language != "zh-CN" {
		t.Fatalf("获批建账号应写入 zh-CN：%q", acc.Language)
	}
	res, err = e.svc.Register(ctx, core.RegisterInput{Code: team.Code, Name: "odd", Password: "odd-password-123", Language: "tlh"})
	if err != nil || res.Signup.Language != "" {
		t.Fatalf("不认识的语言忽略：%v %v", res.Signup, err)
	}
}
