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

	// 钉钉账号的编号碰巧与 Telegram 编号相同：按 Telegram 编号找账号时不串号
	if err := store.UpsertChatIdentity(ctx, pool, store.ChatIdentity{AccountID: bob.AccountID, Provider: store.ProviderDingTalk,
		Tenant: "", UserID: "7001"}); err != nil {
		t.Fatal(err)
	}
	if a, err := store.AccountByTelegramUserID(ctx, pool, 7001); err != nil || a.ID != ann.AccountID {
		t.Fatalf("按 Telegram 编号只找 Telegram 连接：%+v %v", a, err)
	}
	if _, err := store.AccountByTelegramUserID(ctx, pool, 8888); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("没有这个 Telegram 编号：%v", err)
	}
	if err := store.UpsertChatIdentity(ctx, pool, store.ChatIdentity{AccountID: bob.AccountID, Provider: store.ProviderDingTalk,
		UserID: "8888"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AccountByTelegramUserID(ctx, pool, 8888); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("钉钉连接不应被当成 Telegram 连接：%v", err)
	}
	if err := store.DeleteChatIdentity(ctx, pool, bob.AccountID, store.ProviderDingTalk); err != nil {
		t.Fatal(err)
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
	if got, err := store.BotPostByMessage(ctx, pool, store.ProviderDingTalk, "", "cohive-su-5-x"); err != nil || got != p {
		t.Fatalf("按卡片编号找审批消息：%+v %v", got, err)
	}
	if _, err := store.BotPostByMessage(ctx, pool, store.ProviderTelegram, "", "cohive-su-5-x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("通讯工具不同不应匹配：%v", err)
	}
}

// 同一账号、同一群号同时有 Telegram 与钉钉两种记录：凡按通讯工具过滤的读写都互不影响。
// 钉钉群号、用户编号与 Telegram 的同值时最容易串（主脑探针 P1、P2）。
func TestTwoProvidersCoexist(t *testing.T) {
	pool, ws := setup(t)
	ctx := context.Background()
	other, err := store.CreateWorkspace(ctx, pool, "other")
	if err != nil {
		t.Fatal(err)
	}
	ann := mustMember(t, pool, ws, "ann")
	bob := mustMember(t, pool, ws, "bob")

	// 账号：ann 两种都连；bob 只连钉钉，且钉钉编号与 ann 的 Telegram 编号同值
	tg := int64(555)
	if err := store.SetAccountTelegram(ctx, pool, ann.AccountID, &tg, "anntg"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []store.ChatIdentity{
		{AccountID: ann.AccountID, Provider: store.ProviderDingTalk, UserID: "ann-dt", DisplayName: "安"},
		{AccountID: bob.AccountID, Provider: store.ProviderDingTalk, UserID: "555", Handle: "bob-handle"},
	} {
		if err := store.UpsertChatIdentity(ctx, pool, c); err != nil {
			t.Fatal(err)
		}
	}
	if c, err := store.ChatIdentityOf(ctx, pool, ann.AccountID, store.ProviderDingTalk); err != nil || c.UserID != "ann-dt" {
		t.Fatalf("按通讯工具读连接：%+v %v", c, err)
	}
	if a, err := store.AccountByChatIdentity(ctx, pool, store.ProviderDingTalk, "", "555"); err != nil || a.ID != bob.AccountID {
		t.Fatalf("钉钉编号 555 是 bob：%+v %v", a, err)
	}
	if a, err := store.AccountByChatIdentity(ctx, pool, store.ProviderTelegram, "", "555"); err != nil || a.ID != ann.AccountID {
		t.Fatalf("Telegram 编号 555 是 ann：%+v %v", a, err)
	}
	// 账号、成员上的 Telegram 字段只来自 Telegram 连接
	if m, _ := store.MemberByID(ctx, pool, bob.ID); m.TelegramUserID != nil || m.TelegramUsername != "" {
		t.Fatalf("bob 没连 Telegram，不应读出钉钉的编号或名字：%+v", m)
	}
	if a, _ := store.AccountByID(ctx, pool, bob.AccountID); a.TelegramUserID != nil || a.TelegramUsername != "" {
		t.Fatalf("账号上的 Telegram 字段也一样：%+v", a)
	}
	// 断开 ann 的 Telegram 不影响她的钉钉连接（P2）
	if err := store.SetAccountTelegram(ctx, pool, ann.AccountID, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ChatIdentityOf(ctx, pool, ann.AccountID, store.ProviderDingTalk); err != nil {
		t.Fatalf("断开 Telegram 不应连带断开钉钉：%v", err)
	}
	if err := store.DeleteChatIdentity(ctx, pool, ann.AccountID, store.ProviderTelegram); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ChatIdentityOf(ctx, pool, ann.AccountID, store.ProviderDingTalk); err != nil {
		t.Fatalf("按通讯工具删除不应删到钉钉：%v", err)
	}
	// 重连 Telegram 时只改 Telegram 那一行
	if err := store.SetAccountTelegramUsername(ctx, pool, ann.AccountID, "anntg2"); err != nil {
		t.Fatal(err)
	}
	if c, _ := store.ChatIdentityOf(ctx, pool, ann.AccountID, store.ProviderDingTalk); c.Handle != "" || c.DisplayName != "安" {
		t.Fatalf("改 Telegram 用户名不应改到钉钉连接：%+v", c)
	}

	// 群：ws 绑 Telegram 群 "123"，other 绑钉钉群 "123"（同值）
	if err := store.SetWorkspaceNotify(ctx, pool, ws, store.NotifyConfig{Channel: store.ProviderTelegram, Target: "123"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspaceChat(ctx, pool, store.WorkspaceChat{WorkspaceID: other.ID, Provider: store.ProviderDingTalk, ChatID: "123"}); err != nil {
		t.Fatalf("不同通讯工具的同号群不冲突：%v", err)
	}
	if c, err := store.WorkspaceChatOf(ctx, pool, store.ProviderDingTalk, "", "123"); err != nil || c.WorkspaceID != other.ID {
		t.Fatalf("钉钉群 123 反查到 other（P1）：%+v %v", c, err)
	}
	if c, err := store.WorkspaceChatOf(ctx, pool, store.ProviderTelegram, "", "123"); err != nil || c.WorkspaceID != ws {
		t.Fatalf("Telegram 群 123 反查到 ws（P1）：%+v %v", c, err)
	}
	if w, err := store.WorkspaceByNotifyTarget(ctx, pool, store.ProviderTelegram, "123"); err != nil || w.ID != ws {
		t.Fatalf("按 Telegram 群找工作空间：%+v %v", w, err)
	}
	// Telegram 群升级改号只改 Telegram 的群
	if err := store.SetWorkspaceNotifyTarget(ctx, pool, other.ID, "-999"); err != nil {
		t.Fatal(err)
	}
	if c, _ := store.WorkspaceChats(ctx, pool, other.ID); len(c) != 1 || c[0].ChatID != "123" {
		t.Fatalf("other 只有钉钉群，Telegram 改号不应改到它：%+v", c)
	}

	// 同一工作空间两种群并存（表结构允许，第一期业务层只允许一个）：按通讯工具解绑只解那一个
	if err := store.SetWorkspaceChat(ctx, pool, store.WorkspaceChat{WorkspaceID: ws, Provider: store.ProviderDingTalk, ChatID: "cid-ws"}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateWorkspaceChatID(ctx, pool, ws, store.ProviderTelegram, "456"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspaceChat(ctx, pool, store.WorkspaceChat{WorkspaceID: ws, Provider: store.ProviderDingTalk, ChatID: "cid-ws2"}); err != nil {
		t.Fatal(err)
	}
	chats, _ := store.WorkspaceChats(ctx, pool, ws)
	got := map[string]string{}
	for _, c := range chats {
		got[c.Provider] = c.ChatID
	}
	if len(chats) != 2 || got[store.ProviderTelegram] != "456" || got[store.ProviderDingTalk] != "cid-ws2" {
		t.Fatalf("改一种通讯工具的群号不应改到另一种：%+v", chats)
	}
	if err := store.DeleteWorkspaceChats(ctx, pool, ws, store.ProviderDingTalk); err != nil {
		t.Fatal(err)
	}
	if chats, _ := store.WorkspaceChats(ctx, pool, ws); len(chats) != 1 || chats[0].Provider != store.ProviderTelegram {
		t.Fatalf("只解绑钉钉群：%+v", chats)
	}
	// 保存 Telegram 群配置时解除别的通讯工具的群，但不动别的工作空间
	if err := store.SetWorkspaceChat(ctx, pool, store.WorkspaceChat{WorkspaceID: ws, Provider: store.ProviderDingTalk, ChatID: "cid-ws3"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspaceNotify(ctx, pool, ws, store.NotifyConfig{Channel: store.ProviderTelegram, Target: "456"}); err != nil {
		t.Fatal(err)
	}
	if chats, _ := store.WorkspaceChats(ctx, pool, ws); len(chats) != 1 || chats[0].Provider != store.ProviderTelegram || chats[0].ChatID != "456" {
		t.Fatalf("保存 Telegram 群后只剩这一个：%+v", chats)
	}
	if c, _ := store.WorkspaceChats(ctx, pool, other.ID); len(c) != 1 {
		t.Fatalf("不动别的工作空间：%+v", c)
	}
}
