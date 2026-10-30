package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

func directTelegram(t *testing.T, handler http.HandlerFunc) *notify.Bot {
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &notify.Bot{TG: &notify.Telegram{Token: "T", BaseURL: srv.URL}}
}

func TestDirectTelegram(t *testing.T) {
	var got map[string]any
	var path string
	b := directTelegram(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1}}`))
	})
	err := b.SendDirect(context.Background(), store.ChatIdentity{Provider: store.ProviderTelegram, UserID: "12345"}, notify.Rendered{Text: "<b>hi</b>"})
	if err != nil {
		t.Fatal(err)
	}
	if path != "/botT/sendMessage" || got["chat_id"] != float64(12345) || got["parse_mode"] != "HTML" ||
		got["text"] != "<b>hi</b>" || got["disable_web_page_preview"] != true {
		t.Fatalf("path=%s body=%v", path, got)
	}
}

func TestDirectTelegramPermanent(t *testing.T) {
	b := directTelegram(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was blocked by the user"}`))
	})
	err := b.SendDirect(context.Background(), store.ChatIdentity{UserID: "12345"}, notify.Rendered{Text: "x"})
	if !notify.IsPermanent(err) {
		t.Fatalf("blocked bot should be permanent: %v", err)
	}
	// 用户编号不是数字：不发请求，直接是永久错误
	err = b.SendDirect(context.Background(), store.ChatIdentity{UserID: "abc"}, notify.Rendered{Text: "x"})
	if !notify.IsPermanent(err) {
		t.Fatalf("non-numeric user ID should be permanent: %v", err)
	}
}

// fakeDingDirect 模拟钉钉：访问令牌接口与单聊批量发送接口。
func fakeDingDirect(t *testing.T, resp string, status int) (*notify.DingTalkChannel, *map[string]any, *string) {
	var body map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			_, _ = w.Write([]byte(`{"accessToken":"tok","expireIn":7200}`))
			return
		}
		path = r.Method + " " + r.URL.Path
		if r.Header.Get("x-acs-dingtalk-access-token") != "tok" {
			t.Errorf("missing access token header")
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(resp))
	}))
	t.Cleanup(srv.Close)
	return &notify.DingTalkChannel{API: &notify.DingTalk{ClientID: "ding-app", ClientSecret: "s", BaseURL: srv.URL}}, &body, &path
}

func TestDirectDingTalk(t *testing.T) {
	ch, body, path := fakeDingDirect(t, `{"processQueryKey":"k","invalidStaffIdList":[],"flowControlledStaffIdList":[]}`, 200)
	to := store.ChatIdentity{Provider: store.ProviderDingTalk, Tenant: "corp", UserID: "u1"}
	if err := ch.SendDirect(context.Background(), to, notify.Rendered{Title: "标题", Text: "第一行\n第二行"}); err != nil {
		t.Fatal(err)
	}
	if *path != "POST /v1.0/robot/oToMessages/batchSend" {
		t.Fatalf("path=%s", *path)
	}
	ids, _ := (*body)["userIds"].([]any)
	if (*body)["robotCode"] != "ding-app" || len(ids) != 1 || ids[0] != "u1" || (*body)["msgKey"] != "sampleMarkdown" {
		t.Fatalf("body=%v", *body)
	}
	var param map[string]string
	if err := json.Unmarshal([]byte((*body)["msgParam"].(string)), &param); err != nil {
		t.Fatal(err)
	}
	if param["title"] != "标题" || param["text"] != "第一行  \n第二行" {
		t.Fatalf("param=%v", param)
	}
	// 标题为空时用 Cohive
	if err := ch.SendDirect(context.Background(), to, notify.Rendered{Text: "x"}); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal([]byte((*body)["msgParam"].(string)), &param)
	if param["title"] != "Cohive" {
		t.Fatalf("param=%v", param)
	}
}

func TestDirectDingTalkInvalidAndFlowControlled(t *testing.T) {
	to := store.ChatIdentity{UserID: "u1"}
	ch, _, _ := fakeDingDirect(t, `{"invalidStaffIdList":["u1"]}`, 200)
	if err := ch.SendDirect(context.Background(), to, notify.Rendered{Text: "x"}); !notify.IsPermanent(err) {
		t.Fatalf("invalid staff should be permanent: %v", err)
	}
	ch, _, _ = fakeDingDirect(t, `{"flowControlledStaffIdList":["u1"]}`, 200)
	err := ch.SendDirect(context.Background(), to, notify.Rendered{Text: "x"})
	var ra *notify.RetryAfterError
	if !errors.As(err, &ra) || ra.Seconds != 60 {
		t.Fatalf("flow controlled should be RetryAfterError(60): %v", err)
	}
	// 列表里是别人：不影响
	ch, _, _ = fakeDingDirect(t, `{"invalidStaffIdList":["other"]}`, 200)
	if err := ch.SendDirect(context.Background(), to, notify.Rendered{Text: "x"}); err != nil {
		t.Fatal(err)
	}
}

// fakeSlackDirect 模拟 Slack 的 conversations.open 与 chat.postMessage。
type fakeSlackDirect struct {
	mu      sync.Mutex
	calls   []string
	posts   []map[string]any
	openErr string
	opens   map[string]string // 用户 → 私聊频道
}

func newSlackDirect(t *testing.T) (*notify.SlackChannel, *fakeSlackDirect) {
	f := &fakeSlackDirect{opens: map[string]string{"U1": "D111", "U2": "D222"}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		api := strings.TrimPrefix(r.URL.Path, "/api/")
		f.mu.Lock()
		defer f.mu.Unlock()
		f.calls = append(f.calls, api)
		if r.Header.Get("Authorization") != "Bearer "+slackBotTok {
			t.Errorf("bad auth header")
		}
		switch api {
		case "conversations.open":
			if f.openErr != "" {
				_, _ = w.Write([]byte(`{"ok":false,"error":"` + f.openErr + `"}`))
				return
			}
			u, _ := body["users"].(string)
			_, _ = w.Write([]byte(`{"ok":true,"channel":{"id":"` + f.opens[u] + `"}}`))
		case "chat.postMessage":
			f.posts = append(f.posts, body)
			_, _ = w.Write([]byte(`{"ok":true,"ts":"1.1"}`))
		}
	}))
	t.Cleanup(srv.Close)
	return &notify.SlackChannel{API: &notify.Slack{BotToken: slackBotTok, BaseURL: srv.URL + "/api"}}, f
}

func TestDirectSlackOpenThenPostAndCache(t *testing.T) {
	ch, f := newSlackDirect(t)
	to := store.ChatIdentity{Provider: store.ProviderSlack, Tenant: "T1", UserID: "U1"}
	for i := 0; i < 2; i++ {
		if err := ch.SendDirect(context.Background(), to, notify.Rendered{Text: "hello <https://x|查看>"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ch.SendDirect(context.Background(), store.ChatIdentity{UserID: "U2"}, notify.Rendered{Text: "b"}); err != nil {
		t.Fatal(err)
	}
	want := []string{"conversations.open", "chat.postMessage", "chat.postMessage", "conversations.open", "chat.postMessage"}
	if strings.Join(f.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls=%v", f.calls)
	}
	if f.posts[0]["channel"] != "D111" || f.posts[1]["channel"] != "D111" || f.posts[2]["channel"] != "D222" ||
		f.posts[0]["text"] != "hello <https://x|查看>" {
		t.Fatalf("posts=%v", f.posts)
	}
}

func TestDirectSlackErrors(t *testing.T) {
	to := store.ChatIdentity{UserID: "U1"}
	for _, code := range []string{"user_not_found", "user_disabled", "cannot_dm_bot"} {
		ch, f := newSlackDirect(t)
		f.openErr = code
		if err := ch.SendDirect(context.Background(), to, notify.Rendered{Text: "x"}); !notify.IsPermanent(err) {
			t.Fatalf("%s should be permanent: %v", code, err)
		}
	}
	ch, f := newSlackDirect(t)
	f.openErr = "missing_scope"
	err := ch.SendDirect(context.Background(), to, notify.Rendered{Text: "x"})
	if !notify.IsPermanent(err) || !strings.Contains(err.Error(), "im:write") {
		t.Fatalf("missing_scope should be permanent and mention im:write: %v", err)
	}
	// 令牌失效仍是通道整体不可用
	f.openErr = "invalid_auth"
	var down *notify.ChannelDownError
	if err := ch.SendDirect(context.Background(), to, notify.Rendered{Text: "x"}); !errors.As(err, &down) {
		t.Fatalf("invalid_auth should be channel down: %v", err)
	}
	// 失败后没有缓存频道
	f.openErr = "ratelimited"
	var ra *notify.RetryAfterError
	if err := ch.SendDirect(context.Background(), to, notify.Rendered{Text: "x"}); !errors.As(err, &ra) {
		t.Fatalf("ratelimited should be RetryAfterError: %v", err)
	}
}
