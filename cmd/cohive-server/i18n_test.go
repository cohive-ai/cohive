package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
)

// TestMain：现有测试断言中文文案，固定用中文跑；英文另有用例。
func TestMain(m *testing.M) {
	i18n.SetDefault(i18n.ZhCN)
	os.Exit(m.Run())
}

// 切到英文：帮助、表头、提示与报错都变英文，命令与参数名不变。
func TestOutputEnglish(t *testing.T) {
	i18n.SetDefault(i18n.EN)
	defer i18n.SetDefault(i18n.ZhCN)

	out, err := runCmd(t, "bogus")
	if err == nil || err.Error() != `unknown command "bogus"` {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(out, "Usage: cohive-server <command>") {
		t.Fatalf("usage = %q", out)
	}

	adminEnv(t)
	mustRun(t, "migrate")
	if got := mustRun(t, "migrate"); got != "Migrations complete\n" {
		t.Fatalf("migrate = %q", got)
	}
	if got := mustRun(t, "member", "add", "--name", "ann", "--role", "admin"); !strings.Contains(got, "Created account ann") {
		t.Fatalf("member add = %q", got)
	}
	if got := mustRun(t, "member", "list"); !strings.Contains(got, "Username") || !strings.Contains(got, "active") || !strings.Contains(got, "admin") {
		t.Fatalf("member list = %q", got)
	}
	if got := mustRun(t, "server", "show"); !strings.Contains(got, "External URL: not set") {
		t.Fatalf("server show = %q", got)
	}
	if got := mustRun(t, "audit", "list"); !strings.Contains(got, "Older entries") && !strings.Contains(got, "No entries") && !strings.Contains(got, "#") {
		t.Fatalf("audit list = %q", got)
	}
	if _, err := runCmd(t, "token", "issue", "--member", "ann"); err == nil || err.Error() != "missing argument --tool" {
		t.Fatalf("err = %v", err)
	}
	if got := mustRun(t, "notify", "retry"); !strings.Contains(got, "Requeued 0 failed notifications") {
		t.Fatalf("notify retry = %q", got)
	}
	if _, err := runCmd(t, "service", "add", "--name", "x", "--owner", "nobody"); err == nil || !strings.Contains(err.Error(), `member "nobody" does not exist`) {
		t.Fatalf("err = %v", err)
	}
	// 英文单复数
	if got := i18n.T("server.notify.retried", i18n.D{"Name": "w", "Count": 1}); !strings.HasPrefix(got, "Requeued 1 failed notification for") {
		t.Fatalf("单数：%q", got)
	}
}

// 两种语言的帮助都按同样的版式列出命令，命令名不被翻译。
func TestUsageCommandsSameInBothLanguages(t *testing.T) {
	re := regexp.MustCompile(`(?m)^  ([a-z][a-z-]*(?: [a-z][a-z-]*)?)(?:\s|$)`)
	cmds := func(l i18n.Lang) string {
		var out []string
		for _, m := range re.FindAllStringSubmatch(l.T("server.usage"), -1) {
			out = append(out, m[1])
		}
		return strings.Join(out, ",")
	}
	if zh, en := cmds(i18n.ZhCN), cmds(i18n.EN); zh != en || zh == "" {
		t.Fatalf("命令列表不一致\nzh: %s\nen: %s", zh, en)
	}
}

// COHIVE_DEFAULT_LANGUAGE：不设为英文，zh-CN 与 zh_CN.UTF-8 都能识别，非法值启动前报错并列出可用语言。
func TestDefaultLanguageEnv(t *testing.T) {
	t.Setenv("COHIVE_DEFAULT_LANGUAGE", "")
	if l, err := defaultLanguage(); err != nil || l != i18n.EN {
		t.Fatalf("不设时应为英文：%q %v", l, err)
	}
	for _, v := range []string{"zh-CN", "zh_CN.UTF-8", " zh "} {
		t.Setenv("COHIVE_DEFAULT_LANGUAGE", v)
		if l, err := defaultLanguage(); err != nil || l != i18n.ZhCN {
			t.Fatalf("%q 应识别为 zh-CN：%q %v", v, l, err)
		}
	}
	t.Setenv("COHIVE_DEFAULT_LANGUAGE", "klingon")
	_, err := defaultLanguage()
	if err == nil || !strings.Contains(err.Error(), "COHIVE_DEFAULT_LANGUAGE") || !strings.Contains(err.Error(), "available: en, zh-CN") {
		t.Fatalf("非法值应报错并列出可用语言：%v", err)
	}
}

// serve 遇到非法的缺省语言，在连数据库、监听之前就退出。
func TestServeRejectsBadDefaultLanguage(t *testing.T) {
	defer i18n.SetDefault(i18n.ZhCN)
	t.Setenv("COHIVE_DEFAULT_LANGUAGE", "klingon")
	t.Setenv("COHIVE_DATABASE_URL", "")
	_, err := runCmd(t, "serve")
	if err == nil || !strings.Contains(err.Error(), "unsupported language") {
		t.Fatalf("err = %v", err)
	}
}

// serve 按环境变量设置进程缺省语言（网页未指定语言时的报错、MCP、群通知用它）。
func TestServeAppliesDefaultLanguage(t *testing.T) {
	defer i18n.SetDefault(i18n.ZhCN)
	for env, want := range map[string]i18n.Lang{"": i18n.EN, "zh-CN": i18n.ZhCN} {
		i18n.SetDefault(i18n.Lang("ja")) // 先放个无关值，确认 serve 会覆盖
		t.Setenv("COHIVE_DEFAULT_LANGUAGE", env)
		t.Setenv("COHIVE_ALLOWED_HOSTS", "https://bad") // 在设置语言之后、连库之前就报错退出
		if _, err := runCmd(t, "serve"); err == nil {
			t.Fatal("应因 COHIVE_ALLOWED_HOSTS 不合法而退出")
		}
		if got := i18n.Default(); got != want {
			t.Fatalf("COHIVE_DEFAULT_LANGUAGE=%q：缺省语言 = %q，期望 %q", env, got, want)
		}
	}
}

// notify test 发进群的消息用服务器缺省语言（COHIVE_DEFAULT_LANGUAGE），终端输出用本机语言。
func TestNotifyTestMessageUsesServerDefaultLanguage(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "workspace", "add", "--name", "payments")
	mustRun(t, "workspace", "notify", "--workspace", "payments", "--telegram-chat", "-1009")
	var text string
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		text, _ = body["text"].(string)
		w.Write([]byte(`{"ok":true,"result":{}}`))
	}))
	defer tg.Close()
	t.Setenv("COHIVE_TELEGRAM_API_BASE", tg.URL)
	t.Setenv("COHIVE_TELEGRAM_BOT_TOKEN", "123:abc")

	// 终端是中文，服务器缺省语言是英文：群里收到英文，终端回显中文
	t.Setenv("COHIVE_DEFAULT_LANGUAGE", "en")
	out := mustRun(t, "notify", "test", "--workspace", "payments")
	if !strings.HasPrefix(text, "Cohive test message") || !strings.Contains(out, "已发送") {
		t.Fatalf("群里应收到英文，终端为中文：%q %q", text, out)
	}
	t.Setenv("COHIVE_DEFAULT_LANGUAGE", "zh-CN")
	mustRun(t, "notify", "test", "--workspace", "payments")
	if !strings.HasPrefix(text, "Cohive 测试消息") {
		t.Fatalf("缺省语言为 zh-CN 时群里应收到中文：%q", text)
	}
}
