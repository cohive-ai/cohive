package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/httpapi"
	"github.com/cohive-ai/cohive/internal/server"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/internal/testutil"
)

// browser 模拟浏览器：带 Cookie，非 GET 请求带本站 Origin。
type browser struct {
	t      *testing.T
	base   string
	client *http.Client
	origin string
}

func newBrowser(t *testing.T, base string) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, base: base, origin: base,
		client: &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

func (b *browser) do(method, path string, in any, token string) (int, map[string]any) {
	b.t.Helper()
	var body *bytes.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	} else {
		body = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, b.base+path, body)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet && b.origin != "" {
		req.Header.Set("Origin", b.origin)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

type fakeAnnouncer struct {
	mu      sync.Mutex
	signups int
	devices int
	joined  int
	self    int // 其中本人加入（不 @）的次数
}

func (f *fakeAnnouncer) AnnounceSignup(context.Context, store.Signup) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signups++
	return nil
}
func (f *fakeAnnouncer) AnnounceDevice(context.Context, store.DeviceAuth) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.devices++
	return nil
}
func (f *fakeAnnouncer) AnnounceSignupDecided(context.Context, core.SignupDecision) error { return nil }
func (f *fakeAnnouncer) AnnounceDeviceDecided(context.Context, core.DeviceDecision) error { return nil }
func (f *fakeAnnouncer) AnnounceJoined(context.Context, store.Member) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joined++
	return nil
}
func (f *fakeAnnouncer) AnnounceSelfJoined(context.Context, store.Member) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.joined++
	f.self++
	return nil
}

func initialPassword(t *testing.T, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "密码："); ok {
			return v
		}
	}
	t.Fatal("初始密码文件里没有密码")
	return ""
}

func TestWebAccountFlow(t *testing.T) {
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
	ann := &fakeAnnouncer{}
	srv := httptest.NewServer(server.New(svc, builtWeb, server.Options{API: httpapi.Options{
		BotName: func() string { return "CohiveBot" }, Announcer: ann}}))
	defer srv.Close()

	// 初始管理员登录：必须先改密码
	adminB := newBrowser(t, srv.URL)
	if code, out := adminB.do("POST", "/api/v1/auth/login", map[string]string{"name": "admin", "password": "wrong-password"}, ""); code != 401 {
		t.Fatalf("密码错误：%d %v", code, out)
	}
	pw := initialPassword(t, path)
	if code, out := adminB.do("POST", "/api/v1/auth/login", map[string]string{"name": "admin", "password": pw}, ""); code != 200 || out["must_change_password"] != true {
		t.Fatalf("初始管理员登录：%d %v", code, out)
	}
	if code, out := adminB.do("GET", "/api/v1/w/default/admin/members", nil, ""); code != 403 || out["must_change_password"] != true {
		t.Fatalf("改密码前不能做别的：%d %v", code, out)
	}
	if code, _ := adminB.do("GET", "/api/v1/me", nil, ""); code != 200 {
		t.Fatal("改密码前仍可查看自己")
	}
	if code, out := adminB.do("POST", "/api/v1/auth/password", map[string]string{"old_password": pw, "new_password": "admin-new-password"}, ""); code != 204 {
		t.Fatalf("改密码：%d %v", code, out)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("改密码后初始密码文件应删除")
	}

	// 跨站请求被拒
	evil := *adminB
	evil.origin = "https://evil.example.com"
	if code, _ := evil.do("POST", "/api/v1/w/default/admin/invites", map[string]any{"kind": "personal"}, ""); code != 403 {
		t.Fatalf("跨站请求应拒绝：%d", code)
	}

	// 个人邀请：注册即生效
	code, out := adminB.do("POST", "/api/v1/w/default/admin/invites", map[string]any{"kind": "personal"}, "")
	inviteURL, _ := out["url"].(string)
	if code != 200 || !strings.HasPrefix(inviteURL, srv.URL+"/i/") {
		t.Fatalf("生成邀请：%d %v", code, out)
	}
	inviteCode := strings.TrimPrefix(inviteURL, srv.URL+"/i/")
	ann1 := newBrowser(t, srv.URL)
	if code, out := ann1.do("GET", "/api/v1/invites/"+inviteCode, nil, ""); code != 200 || out["valid"] != true || out["workspace"] != "default" {
		t.Fatalf("查看邀请：%d %v", code, out)
	}
	if code, out := ann1.do("POST", "/api/v1/register", map[string]string{"code": inviteCode, "name": "ann", "display_name": "Ann", "password": "ann-password-1"}, ""); code != 200 || out["status"] != "active" {
		t.Fatalf("注册：%d %v", code, out)
	}
	if code, out := ann1.do("GET", "/api/v1/me", nil, ""); code != 200 || accName(out) != "ann" || out["tool"] != "web" ||
		len(out["workspaces"].([]any)) != 1 {
		t.Fatalf("注册后已登录：%d %v", code, out)
	}
	if code, _ := ann1.do("GET", "/api/v1/w/default/admin/members", nil, ""); code != 403 {
		t.Fatal("普通成员不能访问管理接口")
	}
	// 通讯工具：成员看到启用了哪些（不含状态细节），状态只给服务器管理员
	_, me := ann1.do("GET", "/api/v1/me", nil, "")
	if chs, _ := me["channels"].([]any); len(chs) != 1 || chs[0].(map[string]any)["provider"] != "telegram" {
		t.Fatalf("个人信息里应列出启用的通讯工具：%v", me["channels"])
	}
	if chats, ok := me["chat"].([]any); !ok || len(chats) != 0 {
		t.Fatalf("还没连接任何通讯工具：%v", me["chat"])
	}
	if code, _ := ann1.do("GET", "/api/v1/server/channels", nil, ""); code != 403 {
		t.Fatalf("普通成员不能看通讯工具状态：%d", code)
	}
	if code, out := adminB.do("GET", "/api/v1/server/channels", nil, ""); code != 200 ||
		out["channels"].([]any)[0].(map[string]any)["bot_name"] != "CohiveBot" {
		t.Fatalf("服务器管理员看通讯工具状态：%d %v", code, out)
	}

	// 终端发起设备授权，浏览器授权（首台免审），终端领令牌
	term := newBrowser(t, srv.URL)
	term.origin = ""
	code, st := term.do("POST", "/api/v1/device/start", map[string]any{"tools": []string{"claude-code", "codex"}, "device_name": "ann-mac"}, "")
	authURL, _ := st["url"].(string)
	if code != 200 || !strings.HasPrefix(authURL, srv.URL+"/device/da_") {
		t.Fatalf("发起：%d %v", code, st)
	}
	nonce := strings.TrimPrefix(authURL, srv.URL+"/device/")
	poll := map[string]any{"request_id": st["request_id"], "poll_secret": st["poll_secret"]}
	if code, out := term.do("POST", "/api/v1/device/poll", poll, ""); code != 200 || out["status"] != "pending" {
		t.Fatalf("授权前：%d %v", code, out)
	}
	if code, out := ann1.do("GET", "/api/v1/device/auth/"+nonce, nil, ""); code != 200 || out["first_device"] != true || out["device_name"] != "ann-mac" {
		t.Fatalf("授权页：%d %v", code, out)
	}
	if code, out := ann1.do("POST", "/api/v1/device/auth/"+nonce, map[string]bool{"approve": true}, ""); code != 200 || out["status"] != "approved" {
		t.Fatalf("授权：%d %v", code, out)
	}
	code, got := term.do("POST", "/api/v1/device/poll", poll, "")
	wss, _ := got["workspaces"].([]any)
	var tokens map[string]any
	if len(wss) == 1 {
		tokens, _ = wss[0].(map[string]any)["tokens"].(map[string]any)
	}
	devSecret, _ := got["device_secret"].(string)
	if code != 200 || got["status"] != "approved" || got["account"] != "ann" || len(tokens) != 2 || !strings.HasPrefix(devSecret, "cohive_dev_") {
		t.Fatalf("领设备密钥与令牌：%d %v", code, got)
	}
	token, _ := tokens["codex"].(string)
	if code, out := term.do("GET", "/api/v1/me", nil, token); code != 200 || out["tool"] != "codex" {
		t.Fatalf("令牌可用：%d %v", code, out)
	}
	if code, _ := term.do("POST", "/api/v1/device/auth/"+nonce, map[string]bool{"approve": true}, token); code != 403 {
		t.Fatal("设备授权只能在网页上做")
	}

	// cohive web：设备密钥换一次性网页登录链接
	code, out = term.do("POST", "/api/v1/auth/web-login", map[string]any{}, devSecret)
	onceURL, _ := out["url"].(string)
	if code != 200 || !strings.HasPrefix(onceURL, srv.URL+"/auth/once/") {
		t.Fatalf("免密登录链接：%d %v", code, out)
	}
	web := newBrowser(t, srv.URL)
	resp, err := web.client.Get(onceURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("应跳转：%d", resp.StatusCode)
	}
	if code, out := web.do("GET", "/api/v1/me", nil, ""); code != 200 || accName(out) != "ann" || out["login_method"] != "device" {
		t.Fatalf("免密登录后应已登录：%d %v", code, out)
	}

	// 第二台设备：需管理员批准
	code, st2 := term.do("POST", "/api/v1/device/start", map[string]any{"tools": []string{"codex"}, "device_name": "ann-win"}, "")
	nonce2 := strings.TrimPrefix(st2["url"].(string), srv.URL+"/device/")
	if code, out := ann1.do("POST", "/api/v1/device/auth/"+nonce2, map[string]bool{"approve": true}, ""); code != 200 || out["status"] != "awaiting_admin" {
		t.Fatalf("非首台：%d %v", code, out)
	}
	code, pending := adminB.do("GET", "/api/v1/w/default/admin/pending", nil, "")
	devs, _ := pending["devices"].([]any)
	if code != 200 || len(devs) != 1 {
		t.Fatalf("待审设备：%d %v", code, pending)
	}
	id := devs[0].(map[string]any)["id"]
	if code, _ := adminB.do("POST", "/api/v1/w/default/admin/device-auths/"+jsonNumber(int64(id.(float64))), map[string]bool{"approve": true}, ""); code != 204 {
		t.Fatalf("批准设备：%d", code)
	}
	if code, out := term.do("POST", "/api/v1/device/poll", map[string]any{"request_id": st2["request_id"], "poll_secret": st2["poll_secret"]}, ""); code != 200 || out["status"] != "approved" {
		t.Fatalf("批准后领取：%d %v", code, out)
	}

	// 团队邀请：注册后待审，管理员批准后可登录
	_, out = adminB.do("POST", "/api/v1/w/default/admin/invites", map[string]any{"kind": "team", "max_uses": 5}, "")
	teamCode := strings.TrimPrefix(out["url"].(string), srv.URL+"/i/")
	bob := newBrowser(t, srv.URL)
	if code, out := bob.do("POST", "/api/v1/register", map[string]string{"code": teamCode, "name": "bob", "password": "bob-password-1"}, ""); code != 200 || out["status"] != "pending" {
		t.Fatalf("团队注册：%d %v", code, out)
	}
	if code, _ := bob.do("POST", "/api/v1/auth/login", map[string]string{"name": "bob", "password": "bob-password-1"}, ""); code != 403 {
		t.Fatalf("待审时登录应 403：%d", code)
	}
	_, pending = adminB.do("GET", "/api/v1/w/default/admin/pending", nil, "")
	sus, _ := pending["signups"].([]any)
	if len(sus) != 1 {
		t.Fatalf("待审注册：%v", pending)
	}
	sid := int64(sus[0].(map[string]any)["id"].(float64))
	if code, _ := adminB.do("POST", "/api/v1/w/default/admin/signups/"+jsonNumber(sid), map[string]bool{"approve": true}, ""); code != 204 {
		t.Fatalf("批准注册：%d", code)
	}
	if code, _ := bob.do("POST", "/api/v1/auth/login", map[string]string{"name": "bob", "password": "bob-password-1"}, ""); code != 200 {
		t.Fatalf("批准后可登录：%d", code)
	}
	// 通知在后台发出，稍等片刻
	for i := 0; ; i++ {
		ann.mu.Lock()
		s, d, j, self := ann.signups, ann.devices, ann.joined, ann.self
		ann.mu.Unlock()
		if s == 1 && d == 1 && j == 2 && self == 1 { // 个人邀请注册是本人加入（不 @），团队申请获批是管理员批准
			break
		}
		if i > 100 {
			t.Fatalf("应通知管理员一次注册、一次设备，并在群里说两次新成员加入（个人邀请注册、团队申请获批）：%d %d %d", s, d, j)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 管理页：成员、设备、邀请、工作空间
	if code, out := adminB.do("GET", "/api/v1/w/default/admin/members", nil, ""); code != 200 || len(out["items"].([]any)) != 3 {
		t.Fatalf("成员列表：%d %v", code, out)
	}
	if code, out := adminB.do("GET", "/api/v1/w/default/admin/devices", nil, ""); code != 200 || len(out["items"].([]any)) != 2 {
		t.Fatalf("设备列表：%d %v", code, out)
	}
	if code, _ := adminB.do("POST", "/api/v1/w/default/admin/workspace", map[string]any{"telegram_chat": "-1001", "show_title": true}, ""); code != 204 {
		t.Fatalf("保存工作空间：%d", code)
	}
	if code, out := adminB.do("GET", "/api/v1/w/default/admin/workspace", nil, ""); code != 200 || out["telegram_chat"] != "-1001" || out["bot_enabled"] != true {
		t.Fatalf("工作空间：%d %v", code, out)
	}
	if code, out := ann1.do("POST", "/api/v1/me/links", map[string]string{"provider": "telegram"}, ""); code != 200 || !strings.HasPrefix(out["url"].(string), "https://t.me/CohiveBot?start=ln_") {
		t.Fatalf("连接 Telegram：%d %v", code, out)
	}

	// 退出
	if code, _ := ann1.do("POST", "/api/v1/auth/logout", nil, ""); code != 204 {
		t.Fatal("退出")
	}
	if code, _ := ann1.do("GET", "/api/v1/me", nil, ""); code != 401 {
		t.Fatal("退出后未登录")
	}
}

// accName 取出 /api/v1/me 与登录结果里的账号登录名。
func accName(out map[string]any) string {
	a, _ := out["account"].(map[string]any)
	n, _ := a["name"].(string)
	return n
}
