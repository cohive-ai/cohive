package server_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/httpapi"
	"github.com/cohive-ai/cohive/internal/server"
	"github.com/cohive-ai/cohive/internal/testutil"
)

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// 只启用了 Slack 的服务器：连接 Slack 给出绑定码，没启用的钉钉报 503 并说清是钉钉；频道绑定码按 Slack 生成；
// 服务器管理的账号列表按通讯工具列出已连接的；断开走通用地址。
func TestSlackLinkAndBindHTTP(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewSchemaPool(t)
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	svc := &core.Service{Pool: pool, DataDir: filepath.Join(t.TempDir(), "data")}
	path, err := svc.EnsureInitialAdmin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.New(svc, builtWeb, server.Options{API: httpapi.Options{
		Channels: func() []httpapi.ChannelInfo {
			return []httpapi.ChannelInfo{{Provider: "slack", Online: true}}
		}}}))
	defer srv.Close()
	admin := newBrowser(t, srv.URL)
	pw := initialPassword(t, path)
	admin.do("POST", "/api/v1/auth/login", map[string]string{"name": "admin", "password": pw}, "")
	if code, _ := admin.do("POST", "/api/v1/auth/password", map[string]string{"old_password": pw, "new_password": "admin-new-password"}, ""); code != 204 {
		t.Fatal("改密码")
	}

	code, out := admin.do("POST", "/api/v1/me/links", map[string]string{"provider": "slack"}, "")
	if code != 200 || !core.LooksLikeCode(out["code"].(string)) {
		t.Fatalf("连接 Slack 应给出绑定码：%d %v", code, out)
	}
	if code, out := admin.do("POST", "/api/v1/me/links", map[string]string{"provider": "dingtalk"}, ""); code != 503 || !(strings.Contains(out["error"].(string), "DingTalk") || strings.Contains(out["error"].(string), "钉钉")) {
		t.Fatalf("没启用的钉钉：%d %v", code, out)
	}
	if _, err := svc.SubmitLinkCode(ctx, core.ProviderSlack, out["code"].(string), core.ChatSender{Tenant: "T1", UserID: "U1", Name: "admin.s"}, true); err != nil {
		t.Fatal(err)
	}
	id := jsonNumber(int64(out["id"].(float64)))
	if code, out := admin.do("GET", "/api/v1/me/links/"+id, nil, ""); code != 200 || out["status"] != "awaiting_confirm" || out["candidate_name"] != "admin.s" {
		t.Fatalf("等本人确认：%d %v", code, out)
	}
	if code, _ := admin.do("POST", "/api/v1/me/links/"+id+"/confirm", map[string]bool{"accept": true}, ""); code != 204 {
		t.Fatalf("确认连接：%d", code)
	}
	if code, out := admin.do("GET", "/api/v1/server/accounts", nil, ""); code != 200 || !strings.Contains(jsonText(out), `"chat_linked":["slack"]`) {
		t.Fatalf("账号列表应列出已连接 Slack：%d %v", code, out)
	}
	if code, out := admin.do("POST", "/api/v1/w/default/admin/chat-binds", map[string]string{"provider": "slack"}, ""); code != 200 || out["code"] == nil {
		t.Fatalf("生成 Slack 频道绑定码：%d %v", code, out)
	}
	if code, _ := admin.do("DELETE", "/api/v1/server/accounts/admin/links/slack", nil, ""); code != 204 {
		t.Fatalf("断开 Slack：%d", code)
	}
	if _, out := admin.do("GET", "/api/v1/server/accounts", nil, ""); !strings.Contains(jsonText(out), `"chat_linked":[]`) {
		t.Fatalf("断开后不再列出：%v", out)
	}
}
