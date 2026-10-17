package notify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/gorilla/websocket"
)

// fakeGateway 模拟钉钉 Stream 网关：connections/open 换票据，WebSocket 上推帧、收应答。
// 其余路径交给 fakeDingTalk（取令牌、卡片、会话 Webhook）。
type fakeGateway struct {
	*fakeDingTalk
	frames  chan string // 要推给 Cohive 的帧
	mu      sync.Mutex
	acks    []map[string]any
	ackSeen chan struct{}
	opened  chan string // 连接请求里的 clientSecret（核对凭据只在请求体里）
}

func newFakeGateway(t *testing.T) *fakeGateway {
	g := &fakeGateway{fakeDingTalk: &fakeDingTalk{}, frames: make(chan string, 16), ackSeen: make(chan struct{}, 16),
		opened: make(chan string, 4)}
	up := websocket.Upgrader{}
	g.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.0/gateway/connections/open":
			var in map[string]any
			_ = json.NewDecoder(r.Body).Decode(&in)
			g.opened <- in["clientSecret"].(string)
			_ = json.NewEncoder(w).Encode(map[string]string{"endpoint": "ws" + strings.TrimPrefix(g.srv.URL, "http") + "/gateway", "ticket": "tk-1"})
		case "/gateway":
			if r.URL.Query().Get("ticket") != "tk-1" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			conn, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			go func() {
				for {
					var ack map[string]any
					if err := conn.ReadJSON(&ack); err != nil {
						return
					}
					g.mu.Lock()
					g.acks = append(g.acks, ack)
					g.mu.Unlock()
					g.ackSeen <- struct{}{}
				}
			}()
			for f := range g.frames {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(f)); err != nil {
					return
				}
			}
		default:
			g.fakeDingTalk.serve(w, r)
		}
	}))
	t.Cleanup(func() { close(g.frames); g.srv.Close() })
	return g
}

func frame(typ, topic, id string, data any) string {
	d, _ := json.Marshal(data)
	if s, ok := data.(string); ok {
		d = []byte(s)
	}
	b, _ := json.Marshal(map[string]any{"specVersion": "1.0", "type": typ,
		"headers": map[string]string{"topic": topic, "messageId": id}, "data": string(d)})
	return string(b)
}

// ackFor 等到某条消息的应答。
func (g *fakeGateway) ackFor(t *testing.T, id string) map[string]any {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		g.mu.Lock()
		for _, a := range g.acks {
			if h, _ := a["headers"].(map[string]any); h["messageId"] == id {
				g.mu.Unlock()
				return a
			}
		}
		g.mu.Unlock()
		select {
		case <-g.ackSeen:
		case <-deadline:
			t.Fatalf("等不到消息 %s 的应答：%v", id, g.acks)
		}
	}
}

// fakeDTCore 模拟钉钉机器人用到的业务操作。
type fakeDTCore struct {
	mu    sync.Mutex
	calls []string
	link  core.LinkSubmit
	err   error
}

func (f *fakeDTCore) SubmitLinkCode(_ context.Context, provider, code string, who core.ChatSender, private bool) (core.LinkSubmit, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join([]string{"link", provider, code, who.Tenant, who.UserID, who.Name, map[bool]string{true: "私聊", false: "群里"}[private]}, "|"))
	if !private {
		return core.LinkSubmit{}, core.ErrLinkCodeLeaked
	}
	return f.link, f.err
}

func (f *fakeDTCore) CompleteChatBind(_ context.Context, provider, chatID, title, code string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.Join([]string{"bind", provider, chatID, title, code}, "|"))
	if code != "GOODCODE" {
		return "", core.ErrBindCodeGone
	}
	return "支付", nil
}

func waitOnline(t *testing.T, ch *notify.DingTalkChannel) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if ok, _ := ch.Status(); ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Stream 一直没连上")
}

// 端到端：经 Stream 收 ping、单聊绑定码、群里误发的绑定码、卡片按钮；逐帧应答，按钮在应答里带回结果卡片。
func TestDingTalkStreamEndToEnd(t *testing.T) {
	g := newFakeGateway(t)
	ws := store.Workspace{ID: 4, Name: "ops", Notify: store.NotifyConfig{Channel: store.ProviderDingTalk, Target: "cid-ops"}}
	fc := &fakeCore{workspace: ws, dtGroups: map[string]store.Workspace{"cid-ops": ws},
		dtUsers: map[string]store.Account{"corp1/u-lee": {ID: 9, Name: "lee"}},
		posts:   []store.BotPost{{Kind: "su", RefID: 5, WorkspaceID: 4, Provider: store.ProviderDingTalk, ChatID: "cid-ops", MessageID: "cohive-su-5-4-abc"}}}
	dc := &fakeDTCore{link: core.LinkSubmit{Account: "lee"}}
	ch := &notify.DingTalkChannel{API: g.api(), Core: dc, ApprovalTemplate: "tpl-a", Logf: t.Logf}
	hub := &notify.Hub{Core: fc, Channels: map[string]notify.Channel{store.ProviderDingTalk: ch}, Logf: t.Logf}
	ch.Hub = hub
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ch.Run(ctx)
	if secret := <-g.opened; secret != fakeSecret {
		t.Fatalf("连接请求应带应用凭据：%q", secret)
	}
	waitOnline(t, ch)

	g.frames <- frame("SYSTEM", "ping", "m-ping", `{"opaque":"x"}`)
	if a := g.ackFor(t, "m-ping"); a["code"].(float64) != 200 || a["data"] != `{"opaque":"x"}` {
		t.Fatalf("ping 应原样应答：%v", a)
	}

	hook := g.srv.URL + "/robot/send"
	g.frames <- frame("CALLBACK", "/v1.0/im/bot/messages/get", "m-dm", map[string]any{
		"conversationId": "cid-dm", "conversationType": "1", "senderStaffId": "u-lee", "senderCorpId": "corp1",
		"senderNick": "李", "sessionWebhook": hook, "msgtype": "text", "text": map[string]string{"content": " 绑定 abcd-2345 "}})
	g.ackFor(t, "m-dm")
	g.frames <- frame("CALLBACK", "/v1.0/im/bot/messages/get", "m-grp", map[string]any{
		"conversationId": "cid-ops", "conversationType": "2", "conversationTitle": "运维群", "senderStaffId": "u-x",
		"senderCorpId": "corp1", "sessionWebhook": hook, "msgtype": "text", "text": map[string]string{"content": "@Cohive ABCD2345"}})
	g.ackFor(t, "m-grp")
	replies := waitReplies(t, g, 2)
	if !strings.Contains(replies, "已收到") || !strings.Contains(replies, "已作废") || strings.Contains(replies, "ABCD2345") {
		t.Fatalf("单聊收码、群里误发作废（回复不复述码）：%s", replies)
	}
	dc.mu.Lock()
	calls := strings.Join(dc.calls, "\n")
	dc.mu.Unlock()
	if !strings.Contains(calls, "link|dingtalk|abcd-2345|corp1|u-lee|李|私聊") || !strings.Contains(calls, "link|dingtalk|ABCD2345|corp1|u-x||群里") {
		t.Fatalf("应把码连同平台填写的发送者交给业务层：%s", calls)
	}

	g.frames <- frame("CALLBACK", "/v1.0/card/instances/callback", "m-card", map[string]any{
		"outTrackId": "cohive-su-5-4-abc", "corpId": "corp1", "userId": "u-lee", "type": "actionCallback",
		"content": `{"cardPrivateData":{"actionIds":["approve"],"params":{"decision":"approve"}}}`})
	a := g.ackFor(t, "m-card")
	var resp struct {
		Response struct {
			CardData struct {
				CardParamMap map[string]string `json:"cardParamMap"`
			} `json:"cardData"`
			UserPrivateData struct {
				CardParamMap map[string]string `json:"cardParamMap"`
			} `json:"userPrivateData"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(a["data"].(string)), &resp); err != nil {
		t.Fatalf("卡片应答：%v %v", a, err)
	}
	if p := resp.Response.CardData.CardParamMap; p["pending"] != "false" || !strings.Contains(p["status"], "已批准") ||
		resp.Response.UserPrivateData.CardParamMap["tip"] != "已批准" {
		t.Fatalf("应在应答里把卡片改成结果：%+v", resp.Response)
	}
	if len(fc.calls) < 3 || fc.calls[2] != "signup" || fc.calls[5] != int64(5) {
		t.Fatalf("应按卡片编号反查到申请 5 并审批：%v", fc.calls)
	}

	// 网关要求重连：重新换票据再连上
	g.frames <- frame("SYSTEM", "disconnect", "m-bye", "")
	select {
	case <-g.opened:
	case <-time.After(10 * time.Second):
		t.Fatal("收到 disconnect 后应重连")
	}
}

func waitReplies(t *testing.T, g *fakeGateway, n int) string {
	t.Helper()
	for i := 0; i < 300; i++ {
		calls := g.callsTo("/robot/send")
		if len(calls) >= n {
			var out []string
			for _, c := range calls {
				md, _ := c.body["markdown"].(map[string]any)
				out = append(out, md["text"].(string))
			}
			return strings.Join(out, "\n")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等不到 %d 条回复：%+v", n, g.callsTo("/robot/send"))
	return ""
}

// 发通知：配了通知模板时发卡片、按幂等键定卡片编号、只 @ 连接了钉钉的人；没配时降级为不能 @ 的 Markdown。
// 审批：Stream 不在线时发不带按钮的文字、不返回卡片编号；改卡只改给出的变量。
func TestDingTalkChannelSend(t *testing.T) {
	f := newFakeDingTalk(t)
	ctx := context.Background()
	plain := &notify.DingTalkChannel{API: f.api()}
	if err := plain.Send(ctx, "cid1", notify.Rendered{Title: "#1 新单", Text: "第一行\n第二行"}); err != nil {
		t.Fatal(err)
	}
	if c := f.callsTo("/v1.0/robot/groupMessages/send"); len(c) != 1 || !strings.Contains(c[0].body["msgParam"].(string), `第一行  \n第二行`) {
		t.Fatalf("没配模板时发 Markdown：%+v", c)
	}
	card := &notify.DingTalkChannel{API: f.api(), NoticeTemplate: "tpl-n"}
	m := notify.Rendered{Title: "#1 新单", Text: "@ann 请接单", Key: "n5",
		Mentions: []store.ChatIdentity{{Provider: store.ProviderDingTalk, Tenant: "corp1", UserID: "u-ann", DisplayName: "安"}}}
	if err := card.Send(ctx, "cid1", m); err != nil {
		t.Fatal(err)
	}
	c := f.callsTo("/v1.0/card/instances/createAndDeliver")
	if len(c) != 1 {
		t.Fatalf("应投放一张卡片：%+v", c)
	}
	b := c[0].body
	at := b["imGroupOpenDeliverModel"].(map[string]any)["atUserIds"].(map[string]any)
	if b["outTrackId"] != "cohive-n5" || b["cardTemplateId"] != "tpl-n" || b["openSpaceId"] != "dtv1.card//IM_GROUP.cid1" ||
		len(at) != 1 || at["u-ann"] != "安" || b["callbackType"] != nil {
		t.Fatalf("通知卡片：%+v", b)
	}

	ref, err := card.SendApproval(ctx, "cid1", notify.Rendered{Title: "注册申请", Text: "新成员 ann 申请加入"}, notify.Approval{Kind: "su", WorkspaceID: 4, RefID: 5})
	if err != nil || ref != "" {
		t.Fatalf("没配审批模板、Stream 也不在线：不带按钮、没有卡片编号：%q %v", ref, err)
	}
	if c := f.callsTo("/v1.0/robot/groupMessages/send"); len(c) != 2 || !strings.Contains(c[1].body["msgParam"].(string), "管理 → 待审") {
		t.Fatalf("降级的审批消息应指向网页：%+v", c)
	}
	// 配了审批模板，但 Stream 不在线（点了按钮也收不到回调）：同样不带按钮
	offline := &notify.DingTalkChannel{API: f.api(), ApprovalTemplate: "tpl-a"}
	if ref, err := offline.SendApproval(ctx, "cid1", notify.Rendered{Title: "注册申请", Text: "x"}, notify.Approval{Kind: "su", WorkspaceID: 4, RefID: 6}); err != nil || ref != "" ||
		len(f.callsTo("/v1.0/card/instances/createAndDeliver")) != 1 {
		t.Fatalf("Stream 不在线时不发审批卡片：%q %v", ref, err)
	}
	if err := card.FinishApproval(ctx, "cid1", "", notify.Rendered{Text: "x"}); err != nil || len(f.callsTo("/v1.0/card/instances")) != 0 {
		t.Fatal("没有卡片编号时不改")
	}
	if err := card.FinishApproval(ctx, "cid1", "cohive-su-5-4-x", notify.Rendered{Text: "新成员 ann 申请加入\n已批准（审批人 lee）"}); err != nil {
		t.Fatal(err)
	}
	u := f.callsTo("/v1.0/card/instances")
	p := u[0].body["cardData"].(map[string]any)["cardParamMap"].(map[string]any)
	if len(u) != 1 || u[0].method != http.MethodPut || u[0].body["outTrackId"] != "cohive-su-5-4-x" || p["status"] != "已批准（审批人 lee）" ||
		p["pending"] != "false" || p["content"] != "新成员 ann 申请加入" {
		t.Fatalf("改卡：%+v", u)
	}
}

// 在线时审批卡片带 Stream 回调；没连接钉钉的人点按钮只给他自己看提示；审批慢于等待时间时先回“处理中”，结果随后改到卡片上。
func TestDingTalkApprovalCardClicks(t *testing.T) {
	g := newFakeGateway(t)
	ws := store.Workspace{ID: 4, Name: "ops", Notify: store.NotifyConfig{Channel: store.ProviderDingTalk, Target: "cid-ops"}}
	fc := &fakeCore{workspace: ws, dtGroups: map[string]store.Workspace{"cid-ops": ws},
		dtUsers: map[string]store.Account{"corp1/u-lee": {ID: 9, Name: "lee"}}, delay: 300 * time.Millisecond}
	ch := &notify.DingTalkChannel{API: g.api(), ApprovalTemplate: "tpl-a", Logf: t.Logf, ClickWait: 50 * time.Millisecond}
	hub := &notify.Hub{Core: fc, Channels: map[string]notify.Channel{store.ProviderDingTalk: ch}, Logf: t.Logf}
	ch.Hub = hub
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ch.Run(ctx)
	<-g.opened
	waitOnline(t, ch)

	if err := hub.AnnounceSignup(ctx, store.Signup{ID: 5, WorkspaceID: 4, Name: "ann"}); err != nil {
		t.Fatal(err)
	}
	c := g.callsTo("/v1.0/card/instances/createAndDeliver")
	if len(c) != 1 || c[0].body["callbackType"] != "STREAM" || len(fc.posts) != 1 || !strings.HasPrefix(fc.posts[0].MessageID, "cohive-su-5-4-") {
		t.Fatalf("审批卡片应带 Stream 回调并记下卡片编号：%+v %+v", c, fc.posts)
	}
	ref := fc.posts[0].MessageID
	click := func(id, user string) map[string]string {
		g.frames <- frame("CALLBACK", "/v1.0/card/instances/callback", id, map[string]any{
			"outTrackId": ref, "corpId": "corp1", "userId": user,
			"content": `{"cardPrivateData":{"params":{"decision":"approve"}}}`})
		var resp struct {
			Response struct {
				UserPrivateData struct {
					CardParamMap map[string]string `json:"cardParamMap"`
				} `json:"userPrivateData"`
				CardData *struct{} `json:"cardData"`
			} `json:"response"`
		}
		_ = json.Unmarshal([]byte(g.ackFor(t, id)["data"].(string)), &resp)
		out := resp.Response.UserPrivateData.CardParamMap
		if resp.Response.CardData != nil {
			out["has_card"] = "yes"
		}
		return out
	}
	if r := click("c1", "u-stranger"); !strings.Contains(r["tip"], "还没有连接") || r["has_card"] != "" {
		t.Fatalf("没连接钉钉的人点按钮：只给他看提示、不改卡：%v", r)
	}
	if r := click("c2", "u-lee"); !strings.Contains(r["tip"], "处理中") {
		t.Fatalf("审批慢于等待时间时先回“处理中”：%v", r)
	}
	for i := 0; i < 200 && len(g.callsTo("/v1.0/card/instances")) == 0; i++ {
		time.Sleep(10 * time.Millisecond)
	}
	u := g.callsTo("/v1.0/card/instances")
	if len(u) != 1 || u[0].body["outTrackId"] != ref ||
		!strings.Contains(u[0].body["cardData"].(map[string]any)["cardParamMap"].(map[string]any)["status"].(string), "已批准") {
		t.Fatalf("结果随后改到被点的卡片上：%+v", u)
	}
}
