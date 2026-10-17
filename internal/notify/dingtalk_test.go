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
	"sync/atomic"
	"testing"

	"github.com/cohive-ai/cohive/internal/notify"
)

const fakeSecret = "dt-secret-SHOULD-NOT-LEAK"

// fakeDingTalk 模拟钉钉开放接口：取令牌、投放卡片、更新卡片、群消息、会话 Webhook。
type fakeDingTalk struct {
	srv        *httptest.Server
	mu         sync.Mutex
	tokenCalls atomic.Int32
	calls      []fakeCall
	// failCode 不为空时，业务接口返回这个错误码与状态码
	failCode   string
	failStatus int
	badSecret  bool
}

type fakeCall struct {
	method, path, token string
	body                map[string]any
}

func newFakeDingTalk(t *testing.T) *fakeDingTalk {
	f := &fakeDingTalk{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDingTalk) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if r.URL.Path == "/v1.0/oauth2/accessToken" {
		f.tokenCalls.Add(1)
		if f.badSecret || body["appSecret"] != fakeSecret {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":"invalidClientIdOrSecret","message":"无效的clientId或clientSecret"}`))
			return
		}
		_, _ = w.Write([]byte(`{"accessToken":"tok-1","expireIn":7200}`))
		return
	}
	f.mu.Lock()
	f.calls = append(f.calls, fakeCall{r.Method, r.URL.Path, r.Header.Get("x-acs-dingtalk-access-token"), body})
	f.mu.Unlock()
	if r.URL.RawQuery != "" {
		w.WriteHeader(http.StatusBadRequest) // 令牌只该在请求头里
		return
	}
	if r.URL.Path == "/robot/send" { // 会话 Webhook
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
		return
	}
	if f.failCode != "" {
		w.WriteHeader(f.failStatus)
		_, _ = w.Write([]byte(`{"code":"` + f.failCode + `","message":"失败"}`))
		return
	}
	_, _ = w.Write([]byte(`{"success":true}`))
}

func (f *fakeDingTalk) api() *notify.DingTalk {
	return &notify.DingTalk{ClientID: "dingclient1234", ClientSecret: fakeSecret, BaseURL: f.srv.URL}
}

func (f *fakeDingTalk) callsTo(path string) []fakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeCall
	for _, c := range f.calls {
		if c.path == path {
			out = append(out, c)
		}
	}
	return out
}

// 访问令牌缓存，并发只取一次；令牌放在请求头里；机器人编码缺省用 Client ID。
func TestDingTalkTokenCachedAndInHeader(t *testing.T) {
	f := newFakeDingTalk(t)
	api := f.api()
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := api.GroupMarkdown(ctx, "cid1", "t", "x"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := f.tokenCalls.Load(); n != 1 {
		t.Fatalf("令牌应只取一次，取了 %d 次", n)
	}
	calls := f.callsTo("/v1.0/robot/groupMessages/send")
	if len(calls) != 8 || calls[0].token != "tok-1" || calls[0].body["robotCode"] != "dingclient1234" ||
		calls[0].body["openConversationId"] != "cid1" || calls[0].body["msgKey"] != "sampleMarkdown" {
		t.Fatalf("群消息请求：%+v", calls)
	}
}

// 错误按四类归：限流、通道整体不可用（额度用完、权限不足、凭据错）、不值得重试、其余；错误信息里没有密钥。
func TestDingTalkErrorClasses(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		code   string
		status int
		check  func(error) bool
	}{
		{"Forbidden.AccessDenied.QpsLimitForAppkeyAndApi", 403, func(e error) bool { var r *notify.RetryAfterError; return errors.As(e, &r) }},
		{"Forbidden.AccessDenied.ApiCountLimitForOrg", 403, func(e error) bool {
			var d *notify.ChannelDownError
			return errors.As(e, &d) && strings.Contains(d.Msg, "额度")
		}},
		{"Forbidden.AccessDenied.AccessTokenPermissionDenied", 403, func(e error) bool { var d *notify.ChannelDownError; return errors.As(e, &d) }},
		{"invalidParameter.robotCode.notExist", 400, notify.IsPermanent},
		{"InternalError", 500, func(e error) bool {
			var d *notify.ChannelDownError
			return e != nil && !notify.IsPermanent(e) && !errors.As(e, &d)
		}},
	}
	for _, c := range cases {
		f := newFakeDingTalk(t)
		f.failCode, f.failStatus = c.code, c.status
		err := f.api().GroupMarkdown(ctx, "cid1", "t", "x")
		if !c.check(err) {
			t.Errorf("%s：分类不对：%v", c.code, err)
		}
		if err != nil && strings.Contains(err.Error(), fakeSecret) {
			t.Errorf("错误信息里不应有密钥：%v", err)
		}
	}
	f := newFakeDingTalk(t)
	f.badSecret = true
	err := f.api().GroupMarkdown(ctx, "cid1", "t", "x")
	var down *notify.ChannelDownError
	if !errors.As(err, &down) || strings.Contains(err.Error(), fakeSecret) {
		t.Fatalf("凭据错误应暂停通道、不带密钥：%v", err)
	}
}

// 会话 Webhook 只发往钉钉的域名（或测试配置的接口地址），防止被回调内容引到别处。
func TestDingTalkReplyWebhookHost(t *testing.T) {
	f := newFakeDingTalk(t)
	api := f.api()
	ctx := context.Background()
	if err := api.Reply(ctx, f.srv.URL+"/robot/send", "t", "hi"); err != nil {
		t.Fatalf("测试接口地址应放行：%v", err)
	}
	for _, bad := range []string{"https://evil.example.com/robot/send", "http://oapi.dingtalk.com/robot/send",
		"https://dingtalk.com.evil.example.com/x", "https://user:pw@oapi.dingtalk.com/x", "::"} {
		if err := api.Reply(ctx, bad, "t", "hi"); err == nil {
			t.Errorf("%s 不应放行", bad)
		}
	}
	// 能连通的别的主机（另一个本机端口）也不放行：校验在发请求之前
	other := newFakeDingTalk(t)
	if err := api.Reply(ctx, other.srv.URL+"/robot/send", "t", "hi"); err == nil || len(other.callsTo("/robot/send")) != 0 {
		t.Fatalf("别的主机不应收到请求：%v", err)
	}
	real := &notify.DingTalk{ClientID: "x", ClientSecret: "y"}
	if err := real.Reply(ctx, "https://evil.example.com/robot/send", "t", "hi"); err == nil {
		t.Fatal("不配测试地址时只放行 *.dingtalk.com")
	}
}

// 钉钉格式：用户输入里的 Markdown 记号换成全角，不串成格式；@ 只对连接了钉钉的人。
func TestDingTalkFormat(t *testing.T) {
	f := notify.DingTalkFormat{}
	if got := f.Esc("a*b_[x](y)#<b>@all\nz"); strings.ContainsAny(got, "*_[]()#<>@\n") {
		t.Fatalf("应转义 Markdown 记号与换行：%q", got)
	}
	if got := f.Link("https://c.example.com/w/pay/issues/12"); got != "[查看问题单](https://c.example.com/w/pay/issues/12)" {
		t.Fatalf("链接：%q", got)
	}
}
