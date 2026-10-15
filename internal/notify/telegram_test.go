package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/notify"
)

const testToken = "123456:SECRET-BOT-TOKEN"

func TestTelegramSend(t *testing.T) {
	var got map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer srv.Close()

	tg := &notify.Telegram{Token: testToken, BaseURL: srv.URL}
	if err := tg.Send(context.Background(), "-1001", "<b>hi</b>"); err != nil {
		t.Fatal(err)
	}
	if path != "/bot"+testToken+"/sendMessage" {
		t.Fatalf("接口路径不符：%s", path)
	}
	if got["chat_id"] != "-1001" || got["text"] != "<b>hi</b>" || got["parse_mode"] != "HTML" || got["disable_web_page_preview"] != true {
		t.Fatalf("请求内容不符：%v", got)
	}
}

func TestTelegramErrorsClassified(t *testing.T) {
	status, body := 0, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
	defer srv.Close()
	tg := &notify.Telegram{Token: testToken, BaseURL: srv.URL}

	status, body = 429, `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":17}}`
	err := tg.Send(context.Background(), "-1", "x")
	var ra *notify.RetryAfterError
	if !errors.As(err, &ra) || ra.Seconds != 17 {
		t.Fatalf("429 应返回 RetryAfterError(17)，得到 %v", err)
	}

	status, body = 403, `{"ok":false,"error_code":403,"description":"Forbidden: bot was kicked from the group chat"}`
	err = tg.Send(context.Background(), "-1", "x")
	if !notify.IsPermanent(err) || !strings.Contains(err.Error(), "kicked") {
		t.Fatalf("403 应为不可重试错误并带上原因，得到 %v", err)
	}

	status, body = 401, `{"ok":false,"error_code":401,"description":"Unauthorized"}`
	var down *notify.ChannelDownError
	if err := tg.Send(context.Background(), "-1", "x"); !errors.As(err, &down) {
		t.Fatalf("401 是令牌错误，应为通道不可用，得到 %v", err)
	}

	status, body = 400, `{"ok":false,"error_code":400,"description":"Bad Request: group chat was upgraded to a supergroup chat","parameters":{"migrate_to_chat_id":-1001234}}`
	var mig *notify.ChatMigratedError
	if err := tg.Send(context.Background(), "-1", "x"); !errors.As(err, &mig) || mig.NewChatID != "-1001234" {
		t.Fatalf("群升级应返回新群编号，得到 %v", err)
	}

	status, body = 502, `bad gateway`
	if err := tg.Send(context.Background(), "-1", "x"); err == nil || notify.IsPermanent(err) {
		t.Fatalf("502 应为可重试错误，得到 %v", err)
	}
}

// 接口地址里带着机器人令牌，任何错误信息都不能包含它。
func TestTelegramErrorsNeverContainToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // 让请求连接失败
	tg := &notify.Telegram{Token: testToken, BaseURL: url}
	err := tg.Send(context.Background(), "-1", "x")
	if err == nil {
		t.Fatal("连接失败应返回错误")
	}
	if strings.Contains(err.Error(), "SECRET-BOT-TOKEN") {
		t.Fatalf("错误信息泄露了令牌：%v", err)
	}
	if _, err := tg.GetUpdates(context.Background(), 0, 0); err == nil || strings.Contains(err.Error(), "SECRET-BOT-TOKEN") {
		t.Fatalf("收消息的错误信息也不能泄露令牌：%v", err)
	}
}

func TestTelegramGetUpdates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/getUpdates") || r.URL.Query().Get("offset") != "5" {
			t.Errorf("请求不符：%s?%s", r.URL.Path, r.URL.RawQuery)
		}
		w.Write([]byte(`{"ok":true,"result":[{"update_id":5,"message":{"message_id":9,"text":"/chatid","chat":{"id":-100,"type":"supergroup","title":"开发群"},"from":{"id":1,"username":"lee"}}}]}`))
	}))
	defer srv.Close()
	tg := &notify.Telegram{Token: testToken, BaseURL: srv.URL}
	ups, err := tg.GetUpdates(context.Background(), 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(ups) != 1 || ups[0].Message.Chat.ID != -100 || ups[0].Message.From.Username != "lee" {
		t.Fatalf("解析结果不符：%+v", ups)
	}
}
