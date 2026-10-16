package notify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/notify"
)

type recorded struct {
	path string
	body map[string]any
}

func recordingTelegram(t *testing.T, result string) (*notify.Telegram, *[]recorded) {
	t.Helper()
	var calls []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, recorded{path: r.URL.Path, body: body})
		w.Write([]byte(`{"ok":true,"result":` + result + `}`))
	}))
	t.Cleanup(srv.Close)
	return &notify.Telegram{Token: testToken, BaseURL: srv.URL}, &calls
}

func TestSendButtons(t *testing.T) {
	tg, calls := recordingTelegram(t, `{"message_id":55}`)
	id, err := tg.SendButtons(context.Background(), -100, "要批准吗？", [][]notify.Button{{{Text: "批准", Data: "join:1:ok"}, {Text: "拒绝", Data: "join:1:no"}}})
	if err != nil || id != 55 {
		t.Fatalf("应返回消息编号 55：%d %v", id, err)
	}
	c := (*calls)[0]
	if !strings.HasSuffix(c.path, "/sendMessage") {
		t.Fatalf("路径不符：%s", c.path)
	}
	kb, _ := json.Marshal(c.body["reply_markup"])
	if !strings.Contains(string(kb), `"inline_keyboard"`) || !strings.Contains(string(kb), `"callback_data":"join:1:ok"`) {
		t.Fatalf("应带内联按钮：%s", kb)
	}
}

func TestEditAndAnswer(t *testing.T) {
	tg, calls := recordingTelegram(t, `true`)
	if err := tg.EditMessage(context.Background(), -100, 55, "已批准", nil); err != nil {
		t.Fatal(err)
	}
	if err := tg.AnswerCallback(context.Background(), "cb1", "只有管理员能批准"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix((*calls)[0].path, "/editMessageText") || (*calls)[0].body["message_id"] != float64(55) {
		t.Fatalf("编辑消息请求不符：%+v", (*calls)[0])
	}
	if !strings.HasSuffix((*calls)[1].path, "/answerCallbackQuery") || (*calls)[1].body["callback_query_id"] != "cb1" {
		t.Fatalf("回应回调请求不符：%+v", (*calls)[1])
	}
}

func TestButtonDataLimit(t *testing.T) {
	tg, _ := recordingTelegram(t, `{"message_id":1}`)
	_, err := tg.SendButtons(context.Background(), -100, "x", [][]notify.Button{{{Text: "a", Data: strings.Repeat("x", 65)}}})
	if err == nil {
		t.Fatal("回调数据超过 64 字节应报错")
	}
}

func TestParseCallbackUpdate(t *testing.T) {
	var u notify.Update
	raw := `{"update_id":9,"callback_query":{"id":"cb1","from":{"id":42,"username":"lee"},"data":"dl:3:37","message":{"message_id":8,"chat":{"id":42,"type":"private"}}}}`
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatal(err)
	}
	if u.CallbackQuery == nil || u.CallbackQuery.From.ID != 42 || u.CallbackQuery.Data != "dl:3:37" || u.CallbackQuery.Message.MessageID != 8 {
		t.Fatalf("回调解析不符：%+v", u.CallbackQuery)
	}
}
