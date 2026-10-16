package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/server"
	"github.com/cohive-ai/cohive/internal/store"
)

func postJSON(t *testing.T, url string, in any, out any) int {
	t.Helper()
	body, _ := json.Marshal(in)
	resp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatal(err)
		}
	}
	return resp.StatusCode
}

func TestDeviceLoginAPI(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, builtWeb)

	// 未启用机器人：503
	var e map[string]string
	if code := postJSON(t, f.url+"/api/v1/device/start", map[string]any{"tools": []string{"codex"}}, &e); code != http.StatusServiceUnavailable || !strings.Contains(e["error"], "token-stdin") {
		t.Fatalf("未启用机器人应返回 503：%d %v", code, e)
	}

	srv := httptest.NewServer(server.New(f.svc, builtWeb, server.Options{BotName: func() string { return "CohiveBot" }}))
	defer srv.Close()
	var st struct {
		RequestID   int64  `json:"request_id"`
		PollSecret  string `json:"poll_secret"`
		Link        string `json:"link"`
		MatchNumber int    `json:"match_number"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if code := postJSON(t, srv.URL+"/api/v1/device/start", map[string]any{"tools": []string{"claude-code", "codex"}, "device_name": "mac"}, &st); code != http.StatusOK {
		t.Fatalf("发起失败：%d", code)
	}
	if !strings.HasPrefix(st.Link, "https://t.me/CohiveBot?start=dl_") || st.ExpiresIn != 600 || st.PollSecret == "" {
		t.Fatalf("发起结果不符：%+v", st)
	}

	type pollOut struct {
		Status string            `json:"status"`
		Member string            `json:"member"`
		Tokens map[string]string `json:"tokens"`
	}
	var p pollOut
	poll := map[string]any{"request_id": st.RequestID, "poll_secret": st.PollSecret}
	if code := postJSON(t, srv.URL+"/api/v1/device/poll", poll, &p); code != http.StatusOK || p.Status != "pending" {
		t.Fatalf("确认前应为等待：%d %+v", code, p)
	}
	if code := postJSON(t, srv.URL+"/api/v1/device/poll", map[string]any{"request_id": st.RequestID, "poll_secret": "x"}, nil); code != http.StatusNotFound {
		t.Fatalf("轮询密钥不对应返回 404：%d", code)
	}

	// 模拟机器人私聊里的确认
	uid := int64(8001)
	if err := store.SetMemberTelegramUserID(ctx, f.svc.Pool, f.a.MemberID, &uid); err != nil {
		t.Fatal(err)
	}
	nonce := st.Link[strings.Index(st.Link, "=")+1:]
	if _, err := f.svc.OpenDeviceLogin(ctx, nonce, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.ConfirmDeviceLogin(ctx, st.RequestID, uid, st.MatchNumber); err != nil {
		t.Fatal(err)
	}

	p = pollOut{}
	if code := postJSON(t, srv.URL+"/api/v1/device/poll", poll, &p); code != http.StatusOK || p.Status != "approved" || p.Member != "lee-claude" || len(p.Tokens) != 2 {
		t.Fatalf("确认后应交付令牌：%d %+v", code, p)
	}
	if code, _, body := get(t, srv.URL+"/api/v1/me", p.Tokens["codex"]); code != http.StatusOK || !strings.Contains(body, `"tool":"codex"`) {
		t.Fatalf("领到的令牌应可用：%d %s", code, body)
	}
}
