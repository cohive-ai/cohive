package notify_test

import (
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/notify"
)

func msg(text, chatType string, chatID int64, username string) notify.Update {
	return notify.Update{UpdateID: 1, Message: &notify.TGMessage{MessageID: 7, Text: text,
		Chat: notify.TGChat{ID: chatID, Type: chatType, Title: "开发群"},
		From: &notify.TGUser{ID: 42, Username: username}}}
}

func TestChatIDCommand(t *testing.T) {
	for _, text := range []string{"/chatid", "/chatid@CohiveBot", "/chatid 多余参数"} {
		reply, ok := notify.HandleCommand(msg(text, "supergroup", -1001234567890, "lee"), "CohiveBot")
		if !ok {
			t.Fatalf("%q 应被处理", text)
		}
		if !strings.Contains(reply, "-1001234567890") || !strings.Contains(reply, "workspace notify") {
			t.Fatalf("应回复群编号与配置命令：%s", reply)
		}
	}
}

func TestChatIDInPrivateChat(t *testing.T) {
	reply, ok := notify.HandleCommand(msg("/chatid", "private", 42, "lee"), "CohiveBot")
	if !ok || !strings.Contains(reply, "群里") {
		t.Fatalf("私聊里应提示到群里发送：%s", reply)
	}
}

func TestWhoAmICommand(t *testing.T) {
	reply, ok := notify.HandleCommand(msg("/whoami", "group", -1, "lee_dev"), "CohiveBot")
	if !ok || !strings.Contains(reply, "lee_dev") || !strings.Contains(reply, "member set") {
		t.Fatalf("应回复用户名与登记命令：%s", reply)
	}
	reply, ok = notify.HandleCommand(msg("/whoami", "group", -1, ""), "CohiveBot")
	if !ok || !strings.Contains(reply, "还没有设置") {
		t.Fatalf("没有用户名时应提示先设置：%s", reply)
	}
}

func TestIgnoresOtherMessages(t *testing.T) {
	for _, text := range []string{"大家好", "/start", "/chatid@OtherBot", ""} {
		if _, ok := notify.HandleCommand(msg(text, "group", -1, "lee"), "CohiveBot"); ok {
			t.Fatalf("%q 不应被处理", text)
		}
	}
	if _, ok := notify.HandleCommand(notify.Update{UpdateID: 1}, "CohiveBot"); ok {
		t.Fatal("没有消息的更新不应被处理")
	}
}
