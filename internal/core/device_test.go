package core_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

func startDevice(t *testing.T, e env, workspace string) core.DeviceStart {
	t.Helper()
	st, err := e.svc.StartDeviceLogin(context.Background(), []string{"claude-code", "codex", "codex"}, "lee 的 Mac\x07", workspace)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(st.Nonce, core.DeviceStartPrefix) || len(st.Nonce) > 64 || st.MatchNumber < 10 || st.MatchNumber > 99 {
		t.Fatalf("发起结果不符：%+v", st)
	}
	return st
}

func TestDeviceLoginFlow(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	bindTelegram(t, e, e.b.MemberID)
	st := startDevice(t, e, "")

	if p, err := e.svc.PollDeviceLogin(ctx, st.RequestID, st.PollSecret); err != nil || p.Status != store.DevicePending {
		t.Fatalf("确认前应为等待：%+v %v", p, err)
	}
	if _, err := e.svc.PollDeviceLogin(ctx, st.RequestID, "wrong"); !errors.Is(err, core.ErrDeviceGone) {
		t.Fatalf("轮询密钥不对应拒绝：%v", err)
	}

	v, err := e.svc.OpenDeviceLogin(ctx, st.Nonce, tgUser)
	if err != nil {
		t.Fatal(err)
	}
	if v.MemberName != "lee-codex" || v.DeviceName != "lee 的 Mac" || len(v.Numbers) != 3 || !slices.Contains(v.Numbers, st.MatchNumber) ||
		!slices.Equal(v.Tools, []string{"claude-code", "codex"}) {
		t.Fatalf("确认内容不符：%+v", v)
	}
	if _, err := e.svc.ConfirmDeviceLogin(ctx, st.RequestID, tgUser, st.MatchNumber); err != nil {
		t.Fatal(err)
	}

	p, err := e.svc.PollDeviceLogin(ctx, st.RequestID, st.PollSecret)
	if err != nil || p.Status != store.DeviceApproved || p.Member != "lee-codex" || len(p.Tokens) != 2 {
		t.Fatalf("确认后应交付两个令牌：%+v %v", p, err)
	}
	for tool, tok := range p.Tokens {
		id, err := store.LookupToken(ctx, e.svc.Pool, tok)
		if err != nil || id.MemberID != e.b.MemberID || id.Tool != tool {
			t.Fatalf("%s 的令牌不对：%+v %v", tool, id, err)
		}
	}
	if p, _ := e.svc.PollDeviceLogin(ctx, st.RequestID, st.PollSecret); p.Status != store.DeviceDelivered || p.Tokens != nil {
		t.Fatalf("令牌只交付一次：%+v", p)
	}
}

func TestDeviceLoginWrongNumber(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	bindTelegram(t, e, e.b.MemberID)
	st := startDevice(t, e, "")
	if _, err := e.svc.OpenDeviceLogin(ctx, st.Nonce, tgUser); err != nil {
		t.Fatal(err)
	}
	wrong := st.MatchNumber + 1
	if _, err := e.svc.ConfirmDeviceLogin(ctx, st.RequestID, tgUser, wrong); !errors.Is(err, core.ErrDeviceWrongNumber) {
		t.Fatalf("选错数字应作废：%v", err)
	}
	if _, err := e.svc.ConfirmDeviceLogin(ctx, st.RequestID, tgUser, st.MatchNumber); !errors.Is(err, core.ErrDeviceGone) {
		t.Fatalf("作废后不能再确认：%v", err)
	}
	if p, _ := e.svc.PollDeviceLogin(ctx, st.RequestID, st.PollSecret); p.Status != store.DeviceDenied {
		t.Fatalf("终端应看到被拒绝：%+v", p)
	}
}

func TestDeviceLoginOpenedByOther(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	bindTelegram(t, e, e.b.MemberID)
	other := int64(42)
	if err := store.SetMemberTelegramUserID(ctx, e.svc.Pool, e.a.MemberID, &other); err != nil {
		t.Fatal(err)
	}
	st := startDevice(t, e, "")
	if _, err := e.svc.OpenDeviceLogin(ctx, st.Nonce, tgUser); err != nil {
		t.Fatal(err)
	}
	// 别人拿到链接再打开：作废
	if _, err := e.svc.OpenDeviceLogin(ctx, st.Nonce, other); !errors.Is(err, core.ErrDeviceTaken) {
		t.Fatalf("链接被第二个账号打开应作废：%v", err)
	}
	// 别人伪造按钮也不行
	if _, err := e.svc.ConfirmDeviceLogin(ctx, st.RequestID, other, st.MatchNumber); !errors.Is(err, core.ErrDeviceGone) {
		t.Fatalf("非打开人不能确认：%v", err)
	}
	if p, _ := e.svc.PollDeviceLogin(ctx, st.RequestID, st.PollSecret); p.Status != store.DeviceDenied {
		t.Fatalf("应已作废：%+v", p)
	}
}

func TestDeviceLoginNotBoundAndCancel(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	st := startDevice(t, e, "")
	if _, err := e.svc.OpenDeviceLogin(ctx, st.Nonce, tgUser); !errors.Is(err, core.ErrNotBound) {
		t.Fatalf("未绑定应提示先加入：%v", err)
	}
	// 绑定后同一账号可以重新打开同一链接
	bindTelegram(t, e, e.b.MemberID)
	if _, err := e.svc.OpenDeviceLogin(ctx, st.Nonce, tgUser); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.CancelDeviceLogin(ctx, st.RequestID, tgUser); err != nil {
		t.Fatal(err)
	}
	if p, _ := e.svc.PollDeviceLogin(ctx, st.RequestID, st.PollSecret); p.Status != store.DeviceDenied {
		t.Fatalf("取消后终端应看到被拒绝：%+v", p)
	}
	if _, err := e.svc.OpenDeviceLogin(ctx, "dl_nope", tgUser); !errors.Is(err, core.ErrDeviceGone) {
		t.Fatalf("不存在的链接：%v", err)
	}
}

func TestDeviceLoginChooseWorkspace(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	bindTelegram(t, e, e.b.MemberID)
	ws2, err := store.CreateWorkspace(ctx, e.svc.Pool, "payments")
	if err != nil {
		t.Fatal(err)
	}
	m2, err := store.CreateMember(ctx, e.svc.Pool, ws2.ID, "lee", "")
	if err != nil {
		t.Fatal(err)
	}
	bindTelegram(t, e, m2.ID)

	st := startDevice(t, e, "")
	v, err := e.svc.OpenDeviceLogin(ctx, st.Nonce, tgUser)
	if err != nil || len(v.Choices) != 2 || v.Numbers != nil {
		t.Fatalf("加入多个工作空间时应先选：%+v %v", v, err)
	}
	if _, err := e.svc.ConfirmDeviceLogin(ctx, st.RequestID, tgUser, st.MatchNumber); !errors.Is(err, core.ErrDeviceGone) {
		t.Fatalf("没选工作空间不能确认：%v", err)
	}
	v, err = e.svc.ChooseDeviceWorkspace(ctx, st.RequestID, tgUser, ws2.ID)
	if err != nil || v.Workspace != "payments" || v.MemberName != "lee" || len(v.Numbers) != 3 {
		t.Fatalf("选定后应出数字：%+v %v", v, err)
	}
	if _, err := e.svc.ConfirmDeviceLogin(ctx, st.RequestID, tgUser, st.MatchNumber); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.PollDeviceLogin(ctx, st.RequestID, st.PollSecret)
	if err != nil || p.Workspace != "payments" || p.Member != "lee" {
		t.Fatalf("令牌应属于 payments 的 lee：%+v %v", p, err)
	}

	// 终端指定了工作空间时直接出数字
	st = startDevice(t, e, "default")
	if v, err := e.svc.OpenDeviceLogin(ctx, st.Nonce, tgUser); err != nil || v.Workspace != "default" || len(v.Numbers) != 3 {
		t.Fatalf("指定工作空间时应直接出数字：%+v %v", v, err)
	}
}

func TestDeviceLoginExpired(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	bindTelegram(t, e, e.b.MemberID)
	st := startDevice(t, e, "")
	if _, err := e.svc.Pool.Exec(ctx, `UPDATE device_logins SET expires_at = now() - interval '1 second' WHERE id = $1`, st.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.OpenDeviceLogin(ctx, st.Nonce, tgUser); !errors.Is(err, core.ErrDeviceGone) {
		t.Fatalf("过期后不能打开：%v", err)
	}
	if p, _ := e.svc.PollDeviceLogin(ctx, st.RequestID, st.PollSecret); p.Status != store.DeviceExpired {
		t.Fatalf("终端应看到过期：%+v", p)
	}
}

func TestDeviceLoginBadInput(t *testing.T) {
	e := newEnv(t)
	var ie *core.InputError
	for _, tools := range [][]string{nil, {"Bad Tool"}, {"a1", "b1", "c1", "d1", "e1"}} {
		if _, err := e.svc.StartDeviceLogin(context.Background(), tools, "", ""); !errors.As(err, &ie) {
			t.Errorf("工具 %v 应拒绝：%v", tools, err)
		}
	}
}
