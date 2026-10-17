package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cohive-ai/cohive/internal/store"
)

func TestChatIdentities(t *testing.T) {
	pool, ws := setup(t)
	ctx := context.Background()
	ann := mustMember(t, pool, ws, "ann")
	bob := mustMember(t, pool, ws, "bob")

	// Telegram 半连接：先只登记用户名，再补编号；成员读出来的 Telegram 字段跟着变
	if err := store.SetAccountTelegramUsername(ctx, pool, ann.AccountID, "anntg"); err != nil {
		t.Fatal(err)
	}
	m, _ := store.MemberByID(ctx, pool, ann.ID)
	if m.TelegramUsername != "anntg" || m.TelegramUserID != nil {
		t.Fatalf("只登记用户名：%+v", m)
	}
	id := int64(7001)
	if err := store.SetAccountTelegramUserID(ctx, pool, ann.AccountID, &id); err != nil {
		t.Fatal(err)
	}
	m, _ = store.MemberByID(ctx, pool, ann.ID)
	if m.TelegramUsername != "anntg" || m.TelegramUserID == nil || *m.TelegramUserID != 7001 {
		t.Fatalf("补编号后用户名应保留：%+v", m)
	}
	if a, err := store.AccountByTelegramUserID(ctx, pool, 7001); err != nil || a.ID != ann.AccountID {
		t.Fatalf("按 Telegram 编号找账号：%+v %v", a, err)
	}

	// 同一 Telegram 编号不能连第二个账号
	if err := store.SetAccountTelegram(ctx, pool, bob.AccountID, &id, "x"); err == nil {
		t.Fatal("同一 Telegram 编号不应能连第二个账号")
	}
	// 钉钉连接与 Telegram 互不影响
	if err := store.UpsertChatIdentity(ctx, pool, store.ChatIdentity{AccountID: ann.AccountID, Provider: store.ProviderDingTalk,
		Tenant: "dingcorp", UserID: "u1", DisplayName: "安"}); err != nil {
		t.Fatal(err)
	}
	if a, err := store.AccountByChatIdentity(ctx, pool, store.ProviderDingTalk, "dingcorp", "u1"); err != nil || a.ID != ann.AccountID {
		t.Fatalf("按钉钉账号找账号：%+v %v", a, err)
	}
	if _, err := store.AccountByChatIdentity(ctx, pool, store.ProviderDingTalk, "othercorp", "u1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("别的企业的同名 userId 不应匹配：%v", err)
	}
	all, err := store.ChatIdentitiesOf(ctx, pool, []int64{ann.AccountID, bob.AccountID})
	if err != nil || len(all[ann.AccountID]) != 2 || len(all[bob.AccountID]) != 0 {
		t.Fatalf("批量读取：%+v %v", all, err)
	}

	// 两项都清空 = 断开
	if err := store.SetAccountTelegram(ctx, pool, ann.AccountID, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ChatIdentityOf(ctx, pool, ann.AccountID, store.ProviderTelegram); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("断开后不应再有 Telegram 连接：%v", err)
	}
	if err := store.SetAccountTelegramUsername(ctx, pool, 999999, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("账号不存在应返回 ErrNotFound：%v", err)
	}
}

func TestWorkspaceChats(t *testing.T) {
	pool, ws := setup(t)
	ctx := context.Background()
	other, err := store.CreateWorkspace(ctx, pool, "other")
	if err != nil {
		t.Fatal(err)
	}
	if other.Notify.Channel != "" || other.Notify.Target != "" {
		t.Fatalf("新工作空间没有群：%+v", other.Notify)
	}
	cfg := store.NotifyConfig{Channel: store.ProviderTelegram, Target: "-1001", ShowTitle: true, ShowBrief: false}
	if err := store.SetWorkspaceNotify(ctx, pool, ws, cfg); err != nil {
		t.Fatal(err)
	}
	w, _ := store.GetWorkspace(ctx, pool, ws)
	if w.Notify != cfg {
		t.Fatalf("读回群通知配置 %+v，应为 %+v", w.Notify, cfg)
	}
	if got, err := store.WorkspaceByNotifyTarget(ctx, pool, store.ProviderTelegram, "-1001"); err != nil || got.ID != ws {
		t.Fatalf("按群找工作空间：%+v %v", got, err)
	}
	// 同一个群不能绑第二个工作空间
	if err := store.SetWorkspaceNotify(ctx, pool, other.ID, cfg); err == nil {
		t.Fatal("同一个群不应能绑第二个工作空间")
	}
	// 改绑另一种通讯工具的群：原来的群解除（第一期一个工作空间只有一个群）
	if err := store.SetWorkspaceNotify(ctx, pool, ws, store.NotifyConfig{Channel: store.ProviderDingTalk, Target: "cid1", ShowTitle: true}); err != nil {
		t.Fatal(err)
	}
	chats, _ := store.WorkspaceChats(ctx, pool, ws)
	if len(chats) != 1 || chats[0].Provider != store.ProviderDingTalk || chats[0].ChatID != "cid1" {
		t.Fatalf("改绑后应只有钉钉群：%+v", chats)
	}
	// 群升级：只改 Telegram 的群编号，不影响别的通讯工具
	if err := store.SetWorkspaceNotifyTarget(ctx, pool, ws, "-1009"); err != nil {
		t.Fatal(err)
	}
	if chats, _ := store.WorkspaceChats(ctx, pool, ws); chats[0].ChatID != "cid1" {
		t.Fatalf("Telegram 群升级不应改钉钉群：%+v", chats)
	}
	// 关闭群通知：解除全部群
	if err := store.SetWorkspaceNotify(ctx, pool, ws, store.NotifyConfig{ShowTitle: true}); err != nil {
		t.Fatal(err)
	}
	if w, _ := store.GetWorkspace(ctx, pool, ws); w.Notify.Channel != "" || w.Notify.Target != "" {
		t.Fatalf("关闭后不应有群：%+v", w.Notify)
	}
	if err := store.SetWorkspaceNotify(ctx, pool, 999999, cfg); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("工作空间不存在应返回 ErrNotFound：%v", err)
	}
}

func TestBotPostByMessage(t *testing.T) {
	pool, ws := setup(t)
	ctx := context.Background()
	p := store.BotPost{Kind: "su", RefID: 5, WorkspaceID: ws, Provider: store.ProviderDingTalk, ChatID: "cid1", MessageID: "cohive-su-5-x"}
	if err := store.InsertBotPost(ctx, pool, p); err != nil {
		t.Fatal(err)
	}
	if got, err := store.BotPostByMessage(ctx, pool, store.ProviderDingTalk, "cohive-su-5-x"); err != nil || got != p {
		t.Fatalf("按卡片编号找审批消息：%+v %v", got, err)
	}
	if _, err := store.BotPostByMessage(ctx, pool, store.ProviderTelegram, "cohive-su-5-x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("通讯工具不同不应匹配：%v", err)
	}
}
