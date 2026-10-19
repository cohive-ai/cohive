package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNotifyTestCommand(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "workspace", "add", "--name", "payments")

	var chatID, text string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		chatID = fmt.Sprint(body["chat_id"])
		text, _ = body["text"].(string)
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer tg.Close()
	t.Setenv("COHIVE_TELEGRAM_API_BASE", tg.URL)

	t.Setenv("COHIVE_TELEGRAM_BOT_TOKEN", "")
	if _, err := runCmd(t, "notify", "test", "--workspace", "payments"); err == nil || !strings.Contains(err.Error(), "COHIVE_TELEGRAM_BOT_TOKEN") {
		t.Fatalf("没有机器人令牌时应提示设置环境变量，得到 %v", err)
	}
	t.Setenv("COHIVE_TELEGRAM_BOT_TOKEN", "123:abc")
	if _, err := runCmd(t, "notify", "test", "--workspace", "payments"); err == nil || !strings.Contains(err.Error(), "workspace notify") {
		t.Fatalf("工作空间没配置群时应提示配置方法，得到 %v", err)
	}

	mustRun(t, "workspace", "notify", "--workspace", "payments", "--telegram-chat", "-1009")
	out := mustRun(t, "notify", "test", "--workspace", "payments")
	if chatID != "-1009" || !strings.Contains(text, "payments") || !strings.Contains(out, "已发送") {
		t.Fatalf("应往配置的群发送测试消息：%q %q %q", chatID, text, out)
	}
}

// 钉钉：凭据只配一半时报清楚缺哪个；按群编号绑钉钉群；notify test 经钉钉发出（没配通知模板时发 Markdown）；--off 一并解绑。
func TestNotifyTestDingTalk(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "workspace", "add", "--name", "ops")
	var paths []string
	var body map[string]any
	dt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v1.0/oauth2/accessToken" {
			w.Write([]byte(`{"accessToken":"tok","expireIn":7200}`))
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{}`))
	}))
	defer dt.Close()
	t.Setenv("COHIVE_TELEGRAM_BOT_TOKEN", "")
	t.Setenv("COHIVE_DINGTALK_API_BASE", dt.URL)
	t.Setenv("COHIVE_DINGTALK_CLIENT_ID", "dingabc")
	t.Setenv("COHIVE_DINGTALK_CLIENT_SECRET", "")
	if _, err := runCmd(t, "notify", "test", "--workspace", "ops"); err == nil || !strings.Contains(err.Error(), "COHIVE_DINGTALK_CLIENT_SECRET") {
		t.Fatalf("凭据只配一半：%v", err)
	}
	t.Setenv("COHIVE_DINGTALK_CLIENT_SECRET", "s3cret")
	mustRun(t, "workspace", "notify", "--workspace", "ops", "--dingtalk-chat", "cid-ops")
	out := mustRun(t, "notify", "test", "--workspace", "ops")
	if !strings.Contains(out, "已发送") || len(paths) != 2 || paths[1] != "/v1.0/robot/groupMessages/send" || body["openConversationId"] != "cid-ops" {
		t.Fatalf("应经钉钉发到绑定的群：%q %v %v", out, paths, body)
	}
	if _, err := runCmd(t, "workspace", "notify", "--workspace", "ops", "--telegram-chat", "-1001"); err == nil || !strings.Contains(err.Error(), "解绑") {
		t.Fatalf("已绑钉钉群时绑 Telegram 群要先解绑：%v", err)
	}
	mustRun(t, "workspace", "notify", "--workspace", "ops", "--off")
	if _, err := runCmd(t, "notify", "test", "--workspace", "ops"); err == nil || !strings.Contains(err.Error(), "还没配置群通知") {
		t.Fatalf("--off 后钉钉群应一并解绑：%v", err)
	}
}

// Slack：令牌只配一个或填反时报清楚；按频道编号绑 Slack 频道；notify test 经 Slack 发出；两种群参数不能同时用；--off 一并解绑。
func TestNotifyTestSlack(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "workspace", "add", "--name", "ops")
	var calls []string
	var body map[string]any
	sl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path+" "+r.Header.Get("Authorization"))
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Write([]byte(`{"ok":true,"ts":"1.2"}`))
	}))
	defer sl.Close()
	t.Setenv("COHIVE_TELEGRAM_BOT_TOKEN", "")
	t.Setenv("COHIVE_SLACK_API_BASE", sl.URL)
	t.Setenv("COHIVE_SLACK_BOT_TOKEN", "xoxb-bot")
	t.Setenv("COHIVE_SLACK_APP_TOKEN", "")
	if _, err := runCmd(t, "notify", "test", "--workspace", "ops"); err == nil || !strings.Contains(err.Error(), "COHIVE_SLACK_APP_TOKEN") {
		t.Fatalf("令牌只配一个：%v", err)
	}
	t.Setenv("COHIVE_SLACK_BOT_TOKEN", "xapp-app")
	t.Setenv("COHIVE_SLACK_APP_TOKEN", "xoxb-bot")
	if _, err := runCmd(t, "notify", "test", "--workspace", "ops"); err == nil || !strings.Contains(err.Error(), "填反") {
		t.Fatalf("令牌填反：%v", err)
	}
	t.Setenv("COHIVE_SLACK_BOT_TOKEN", "xoxb-bot")
	t.Setenv("COHIVE_SLACK_APP_TOKEN", "xapp-app")
	if _, err := runCmd(t, "workspace", "notify", "--workspace", "ops", "--slack-chat", "C1", "--dingtalk-chat", "cid"); err == nil || !strings.Contains(err.Error(), "只能用一个") {
		t.Fatalf("两种群参数不能同时用：%v", err)
	}
	if out := mustRun(t, "workspace", "notify", "--workspace", "ops", "--slack-chat", "C1"); !strings.Contains(out, "Slack 频道 C1") {
		t.Fatalf("绑 Slack 频道：%s", out)
	}
	out := mustRun(t, "notify", "test", "--workspace", "ops")
	if !strings.Contains(out, "已发送") || len(calls) != 1 || calls[0] != "/chat.postMessage Bearer xoxb-bot" || body["channel"] != "C1" {
		t.Fatalf("应经 Slack 用机器人令牌发到绑定的频道：%q %v %v", out, calls, body)
	}
	mustRun(t, "workspace", "notify", "--workspace", "ops", "--off")
	if _, err := runCmd(t, "notify", "test", "--workspace", "ops"); err == nil || !strings.Contains(err.Error(), "还没配置群通知") {
		t.Fatalf("--off 后 Slack 频道应一并解绑：%v", err)
	}
}
