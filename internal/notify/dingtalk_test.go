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

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
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
			return errors.As(e, &d) && strings.Contains(d.Msg, "quota")
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
	if got := f.Link(i18n.ZhCN, "https://c.example.com/w/pay/issues/12"); got != "[查看问题单](https://c.example.com/w/pay/issues/12)" {
		t.Fatalf("链接：%q", got)
	}
}

// 卡片内的 @ 必须使用钉钉身份标签，昵称和编号均不能注入标签。
func TestDingTalkCardMention(t *testing.T) {
	f := notify.DingTalkFormat{}
	for _, tc := range []struct {
		name string
		id   *store.ChatIdentity
		want string
	}{
		{"已绑定", &store.ChatIdentity{UserID: "u-ann", DisplayName: "安"}, `<a atId="u-ann">安</a>`},
		{"没有昵称", &store.ChatIdentity{UserID: "u-ann"}, `<a atId="u-ann">ann</a>`},
		{"未绑定", nil, "ann"},
		{"编号为空", &store.ChatIdentity{DisplayName: "安"}, "ann"},
		{"转义", &store.ChatIdentity{UserID: `u"<`, DisplayName: `<安>@all`}, `<a atId="u&#34;&lt;">＜安＞＠all</a>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := f.Mention(i18n.ZhCN, "ann", tc.id, false); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDingTalkMarkdownFallbackHasNoCardTags(t *testing.T) {
	f := newFakeDingTalk(t)
	ch := &notify.DingTalkChannel{API: f.api()}
	person := store.ChatIdentity{Provider: store.ProviderDingTalk, UserID: "u-ann", DisplayName: "安"}
	text := ch.Format().Mention(i18n.ZhCN, "ann", &person, false) + " 请处理"
	if err := ch.Send(context.Background(), "cid1", notify.Rendered{Title: "通知", Text: text, Mentions: []store.ChatIdentity{person}}); err != nil {
		t.Fatal(err)
	}
	calls := f.callsTo("/v1.0/robot/groupMessages/send")
	var param map[string]string
	if len(calls) != 1 {
		t.Fatalf("calls: %+v", calls)
	}
	if err := json.Unmarshal([]byte(calls[0].body["msgParam"].(string)), &param); err != nil {
		t.Fatal(err)
	}
	if param["text"] != "安 请处理" {
		t.Fatalf("降级消息不应泄露卡片标签：%q", param["text"])
	}
}

// 从问题单渲染到 HTTP 请求，卡片标签和两份 @ 名单必须对应同一身份。
func TestDingTalkRenderedMentionDelivery(t *testing.T) {
	for _, tc := range []struct {
		name        string
		actor       int64
		linked      bool
		wantMention bool
	}{
		{"他人收到通知", claude.ID, true, true},
		{"未绑定不提及", claude.ID, false, false},
		{"操作者不提及自己", codex.ID, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeDingTalk(t)
			ch := &notify.DingTalkChannel{API: f.api(), NoticeTemplate: "tpl"}
			people := map[int64]notify.Person{claude.ID: {Name: claude.Name}, codex.ID: {Name: codex.Name}}
			if tc.linked {
				p := people[codex.ID]
				p.Chats = []store.ChatIdentity{{Provider: store.ProviderDingTalk, UserID: "staff-123", DisplayName: "小明"}}
				people[codex.ID] = p
			}
			msg := notify.RenderFor(ch.Format(), store.ProviderDingTalk, "", workspace(true, true, "").ws, sampleIssue(),
				store.Notification{Event: "assigned", ActorMemberID: tc.actor, Recipients: []int64{codex.ID, codex.ID}}, people)
			if err := ch.Send(context.Background(), "cid1", msg); err != nil {
				t.Fatal(err)
			}
			body := f.callsTo("/v1.0/card/instances/createAndDeliver")[0].body
			cardAt, hasCardAt := body["cardAtUserIds"]
			at, hasAt := body["imGroupOpenDeliverModel"].(map[string]any)["atUserIds"]
			content := body["cardData"].(map[string]any)["cardParamMap"].(map[string]any)["content"].(string)
			if !tc.wantMention {
				if hasCardAt || hasAt || strings.Contains(content, "<a atId=") {
					t.Fatalf("不应 @：%+v", body)
				}
				return
			}
			if !hasCardAt || !hasAt {
				t.Fatalf("缺少 @ 参数：%+v", body)
			}
			ids := cardAt.([]any)
			users := at.(map[string]any)
			if len(ids) != 1 || ids[0] != "staff-123" || len(users) != 1 || users["staff-123"] != "小明" ||
				!strings.Contains(content, `<a atId="staff-123">小明</a>`) {
				t.Fatalf("@ 身份必须一致且去重：%+v", body)
			}
		})
	}
}
