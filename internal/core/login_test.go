package core_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

const tgUser int64 = 7012345678

func bindTelegram(t *testing.T, e env, memberID int64) {
	t.Helper()
	uid := tgUser
	if err := store.SetMemberTelegramUserID(context.Background(), e.svc.Pool, memberID, &uid); err != nil {
		t.Fatal(err)
	}
}

func TestLoginCodeRequiresBinding(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.IssueLoginCode(context.Background(), tgUser, "")
	if !errors.Is(err, core.ErrNotBound) {
		t.Fatalf("没有绑定的 Telegram 账号不应拿到登录码，得到 %v", err)
	}
}

func TestLoginCodeExchange(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	bindTelegram(t, e, e.b.MemberID)

	res, err := e.svc.IssueLoginCode(ctx, tgUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Z2-9]{4}-[A-Z2-9]{4}$`).MatchString(res.Code) || res.Member.Name != "lee-codex" {
		t.Fatalf("登录码格式或成员不符：%+v", res)
	}

	// 大小写、空格和横线不影响
	sloppy := strings.ToLower(strings.Replace(res.Code, "-", " ", 1))
	token, who, err := e.svc.ExchangeLoginCode(ctx, sloppy, "codex")
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.LookupToken(ctx, e.svc.Pool, token)
	if err != nil || id.MemberID != e.b.MemberID || id.Tool != "codex" || who.MemberName != "lee-codex" {
		t.Fatalf("换到的令牌应属于 lee-codex 的 codex：%+v %+v %v", id, who, err)
	}

	if _, _, err := e.svc.ExchangeLoginCode(ctx, res.Code, "codex"); !errors.Is(err, core.ErrBadLoginCode) {
		t.Fatalf("登录码只能用一次，得到 %v", err)
	}
	if _, _, err := e.svc.ExchangeLoginCode(ctx, "ZZZZ-ZZZZ", "codex"); !errors.Is(err, core.ErrBadLoginCode) {
		t.Fatalf("错误的登录码应拒绝，得到 %v", err)
	}
}

func TestLoginCodeExpires(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	bindTelegram(t, e, e.a.MemberID)
	res, err := e.svc.IssueLoginCode(ctx, tgUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE login_codes SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.ExchangeLoginCode(ctx, res.Code, "claude-code"); !errors.Is(err, core.ErrBadLoginCode) {
		t.Fatalf("过期的登录码应拒绝，得到 %v", err)
	}
}

func TestLoginCodeToolValidated(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	bindTelegram(t, e, e.a.MemberID)
	res, err := e.svc.IssueLoginCode(ctx, tgUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.ExchangeLoginCode(ctx, res.Code, "bad tool!"); err == nil || errors.Is(err, core.ErrBadLoginCode) {
		t.Fatalf("工具名不合法应直接报错且不消耗登录码，得到 %v", err)
	}
	if _, _, err := e.svc.ExchangeLoginCode(ctx, res.Code, "claude-code"); err != nil {
		t.Fatalf("工具名报错后登录码应仍可用：%v", err)
	}
}

func TestLoginCodeChooseWorkspace(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	bindTelegram(t, e, e.a.MemberID)
	ws, err := store.CreateWorkspace(ctx, e.svc.Pool, "payments")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateMember(ctx, e.svc.Pool, ws.ID, "lee", "")
	if err != nil {
		t.Fatal(err)
	}
	uid := tgUser
	if err := store.SetMemberTelegramUserID(ctx, e.svc.Pool, other.ID, &uid); err != nil {
		t.Fatal(err)
	}

	res, err := e.svc.IssueLoginCode(ctx, tgUser, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != "" || len(res.Choices) != 2 {
		t.Fatalf("绑定了多个工作空间时应先让用户选择：%+v", res)
	}
	res, err = e.svc.IssueLoginCode(ctx, tgUser, "payments")
	if err != nil || res.Code == "" || res.Member.ID != other.ID || res.Workspace.Name != "payments" {
		t.Fatalf("指定工作空间后应签发该工作空间的登录码：%+v %v", res, err)
	}
	if _, err := e.svc.IssueLoginCode(ctx, tgUser, "nope"); err == nil || !strings.Contains(err.Error(), "payments") {
		t.Fatalf("工作空间名不对时应列出可选项，得到 %v", err)
	}
}
