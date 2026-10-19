package notify_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/gorilla/websocket"
)

const (
	slackBotTok = "xoxb-test-bot"
	slackAppTok = "xapp-test-app"
	slackTicket = "TICKET-SECRET-123"
)

type slackCall struct {
	api  string
	auth string
	body map[string]any
}

// fakeSlack 模拟 Slack：网页接口（/api/方法名）与 Socket 模式（/link，WebSocket）。
type fakeSlack struct {
	srv    *httptest.Server
	frames chan string // 要推给 Cohive 的消息
	opened chan struct{}
	acks   chan string // 收到的确认里的 envelope_id

	mu      sync.Mutex
	calls   []slackCall
	errs    map[string]string // 接口名 → 返回的错误码
	status  map[string]int    // 接口名 → HTTP 状态码
	team    string
	tsSeq   int
	connErr bool // apps.connections.open 失败
}

func newFakeSlack(t *testing.T) *fakeSlack {
	f := &fakeSlack{frames: make(chan string, 16), opened: make(chan struct{}, 8), acks: make(chan string, 16),
		errs: map[string]string{}, status: map[string]int{}, team: "T1"}
	up := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/link" {
			if r.URL.Query().Get("ticket") != slackTicket {
				http.Error(w, "bad ticket", http.StatusForbidden)
				return
			}
			conn, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			f.opened <- struct{}{}
			_ = conn.WriteJSON(map[string]any{"type": "hello", "num_connections": 1})
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					_, msg, err := conn.ReadMessage()
					if err != nil {
						return
					}
					var a map[string]any
					_ = json.Unmarshal(msg, &a)
					if id, _ := a["envelope_id"].(string); id != "" {
						f.acks <- id
					}
				}
			}()
			for {
				select {
				case m := <-f.frames:
					if err := conn.WriteMessage(websocket.TextMessage, []byte(m)); err != nil {
						return
					}
					if strings.Contains(m, `"disconnect"`) {
						return
					}
				case <-done:
					return
				}
			}
		}
		api := strings.TrimPrefix(r.URL.Path, "/api/")
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.calls = append(f.calls, slackCall{api: api, auth: r.Header.Get("Authorization"), body: body})
		errCode, status, team, connErr := f.errs[api], f.status[api], f.team, f.connErr
		f.tsSeq++
		ts := fmt.Sprintf("1700000000.%06d", f.tsSeq)
		f.mu.Unlock()
		if status != 0 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(status)
			return
		}
		if errCode != "" {
			_, _ = w.Write([]byte(`{"ok":false,"error":"` + errCode + `"}`))
			return
		}
		var out any
		switch api {
		case "auth.test":
			out = map[string]any{"ok": true, "user_id": "UBOT", "team_id": team}
		case "apps.connections.open":
			if connErr {
				_, _ = w.Write([]byte(`{"ok":false,"error":"invalid_auth"}`))
				return
			}
			out = map[string]any{"ok": true, "url": "ws" + strings.TrimPrefix(f.srv.URL, "http") + "/link?ticket=" + slackTicket}
		case "chat.postMessage":
			out = map[string]any{"ok": true, "channel": body["channel"], "ts": ts}
		case "conversations.info":
			out = map[string]any{"ok": true, "channel": map[string]any{"name": "ops"}}
		case "users.info":
			out = map[string]any{"ok": true, "user": map[string]any{"name": "lee", "profile": map[string]any{"display_name": "lee.s"}}}
		default:
			out = map[string]any{"ok": true}
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSlack) api() *notify.Slack {
	return &notify.Slack{BotToken: slackBotTok, AppToken: slackAppTok, BaseURL: f.srv.URL + "/api"}
}

func (f *fakeSlack) callsTo(api string) []slackCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []slackCall
	for _, c := range f.calls {
		if c.api == api {
			out = append(out, c)
		}
	}
	return out
}

// waitCalls 等到某个接口被调用至少 n 次。
func (f *fakeSlack) waitCalls(t *testing.T, api string, n int) []slackCall {
	t.Helper()
	for i := 0; i < 300; i++ {
		if c := f.callsTo(api); len(c) >= n {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等不到 %d 次 %s：%+v", n, api, f.callsTo(api))
	return nil
}

func (f *fakeSlack) ack(t *testing.T, id string) {
	t.Helper()
	select {
	case got := <-f.acks:
		if got != id {
			t.Fatalf("确认的是 %s，应为 %s", got, id)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("没有确认 %s", id)
	}
}

func envelope(kind, id string, payload any) string {
	b, _ := json.Marshal(map[string]any{"type": kind, "envelope_id": id, "payload": payload})
	return string(b)
}

func waitSlackOnline(t *testing.T, ch *notify.SlackChannel) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if ok, _ := ch.Status(); ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Socket 模式一直没连上")
}

// logSink 收集日志，检查里面没有一次性票据与令牌。
type logSink struct {
	mu   sync.Mutex
	logs []string
}

func (l *logSink) logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs = append(l.logs, fmt.Sprintf(format, args...))
}

func (l *logSink) all() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.logs, "\n")
}

// 握手：先查机器人自己（机器人令牌），再换连接地址（应用级令牌），收到 hello 才算在线；Slack 要求换连接时马上重连；
// 服务停止时 Run 返回；日志里没有票据与令牌。
func TestSlackSocketHandshakeAndReconnect(t *testing.T) {
	f := newFakeSlack(t)
	logs := &logSink{}
	ch := &notify.SlackChannel{API: f.api(), Logf: logs.logf}
	if ok, why := ch.Status(); ok || why == "" {
		t.Fatalf("连上之前应离线并说明原因：%v %q", ok, why)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() { ch.Run(ctx); close(stopped) }()
	<-f.opened
	waitSlackOnline(t, ch)
	if a := f.callsTo("auth.test"); len(a) != 1 || a[0].auth != "Bearer "+slackBotTok {
		t.Fatalf("应先用机器人令牌查自己：%+v", a)
	}
	if o := f.callsTo("apps.connections.open"); len(o) != 1 || o[0].auth != "Bearer "+slackAppTok {
		t.Fatalf("应用应用级令牌换连接地址：%+v", o)
	}

	f.frames <- `{"type":"disconnect","reason":"refresh_requested"}`
	select {
	case <-f.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("Slack 要求换连接后应马上重连")
	}
	waitSlackOnline(t, ch)
	if n := len(f.callsTo("apps.connections.open")); n != 2 {
		t.Fatalf("重连应重新换地址：%d", n)
	}

	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("服务停止时 Run 应返回")
	}
	if all := logs.all(); strings.Contains(all, slackTicket) || strings.Contains(all, slackBotTok) || strings.Contains(all, slackAppTok) {
		t.Fatalf("日志里不应有票据或令牌：\n%s", all)
	}
}

// 令牌错：连不上，状态里写明原因；不会把令牌写进原因。应用关了 Socket 模式也写明。
func TestSlackSocketAuthFailure(t *testing.T) {
	f := newFakeSlack(t)
	f.errs["auth.test"] = "invalid_auth"
	ch := &notify.SlackChannel{API: f.api(), Logf: t.Logf}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ch.Run(ctx)
	f.waitCalls(t, "auth.test", 1)
	for i := 0; i < 300; i++ {
		if _, why := ch.Status(); strings.Contains(why, "invalid_auth") {
			if strings.Contains(why, slackBotTok) {
				t.Fatalf("原因里不应有令牌：%s", why)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if ok, why := ch.Status(); ok || !strings.Contains(why, "invalid_auth") {
		t.Fatalf("令牌错时应离线并说明：%v %q", ok, why)
	}
	if n := len(f.callsTo("apps.connections.open")); n != 0 {
		t.Fatalf("机器人令牌错时不应再去换连接地址：%d", n)
	}

	g := newFakeSlack(t)
	ch2 := &notify.SlackChannel{API: g.api(), Logf: t.Logf}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go ch2.Run(ctx2)
	<-g.opened
	waitSlackOnline(t, ch2)
	g.frames <- `{"type":"disconnect","reason":"link_disabled"}`
	for i := 0; i < 300; i++ {
		if ok, why := ch2.Status(); !ok && strings.Contains(why, "Socket Mode") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("应用关了 Socket 模式时应离线并说明")
}

// 发消息：通知用一段 mrkdwn，按用户编号 @；审批消息带两个按钮，按钮数据是种类:工作空间:编号:y/n；
// Socket 模式不在线时不带按钮、提示去网页；改成结果时去掉按钮；错误按类归好。
func TestSlackSend(t *testing.T) {
	ctx := context.Background()
	f := newFakeSlack(t)
	ch := &notify.SlackChannel{API: f.api(), Logf: t.Logf}
	fm := ch.Format()
	text := "#12 " + fm.Esc("<!channel> a&b") + " " + fm.Mention(i18n.ZhCN, "lee", &store.ChatIdentity{Provider: "slack", UserID: "U1"}, false)
	if err := ch.Send(ctx, "C1", notify.Rendered{Text: text, Lang: i18n.ZhCN}); err != nil {
		t.Fatal(err)
	}
	p := f.callsTo("chat.postMessage")[0]
	blocks := []byte(fmt.Sprint(p.body["blocks"]))
	if p.auth != "Bearer "+slackBotTok || p.body["channel"] != "C1" || !strings.Contains(string(blocks), "<@U1>") ||
		!strings.Contains(string(blocks), "&lt;!channel&gt; a&amp;b") || strings.Contains(string(blocks), "<!channel>") {
		t.Fatalf("通知：%+v %s", p, blocks)
	}

	// 离线：不带按钮
	ref, err := ch.SendApproval(ctx, "C1", notify.Rendered{Text: "注册申请", Lang: i18n.ZhCN}, notify.Approval{Kind: "su", WorkspaceID: 4, RefID: 5})
	if err != nil || ref == "" {
		t.Fatalf("离线时也应发出并返回编号：%q %v", ref, err)
	}
	b, _ := json.Marshal(f.callsTo("chat.postMessage")[1].body)
	if strings.Contains(string(b), `"actions"`) || !strings.Contains(string(b), "管理 → 待审") {
		t.Fatalf("离线时不应带按钮、应提示去网页：%s", b)
	}

	// 在线：带按钮
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go ch.Run(cctx)
	<-f.opened
	waitSlackOnline(t, ch)
	ref, err = ch.SendApproval(ctx, "C1", notify.Rendered{Text: "注册申请", Lang: i18n.EN}, notify.Approval{Kind: "su", WorkspaceID: 4, RefID: 5})
	if err != nil {
		t.Fatal(err)
	}
	b, _ = json.Marshal(f.callsTo("chat.postMessage")[2].body)
	if !strings.Contains(string(b), `"value":"su:4:5:y"`) || !strings.Contains(string(b), `"value":"su:4:5:n"`) || !strings.Contains(string(b), "Approve") {
		t.Fatalf("审批消息应带按钮：%s", b)
	}
	if err := ch.FinishApproval(ctx, "C1", ref, notify.Rendered{Text: "已批准"}); err != nil {
		t.Fatal(err)
	}
	u := f.callsTo("chat.update")[0]
	b, _ = json.Marshal(u.body)
	if u.body["ts"] != ref || u.body["channel"] != "C1" || strings.Contains(string(b), `"actions"`) || !strings.Contains(string(b), "已批准") {
		t.Fatalf("改成结果并去掉按钮：%s", b)
	}

	var perm *notify.PermanentError
	var down *notify.ChannelDownError
	var retry *notify.RetryAfterError
	f.errs["chat.postMessage"] = "not_in_channel"
	if err := ch.Send(ctx, "C1", notify.Rendered{Text: "x"}); !errors.As(err, &perm) {
		t.Fatalf("机器人不在频道里：不值得重试：%v", err)
	}
	f.errs["chat.postMessage"] = "invalid_auth"
	if err := ch.Send(ctx, "C1", notify.Rendered{Text: "x"}); !errors.As(err, &down) || strings.Contains(err.Error(), slackBotTok) {
		t.Fatalf("令牌错：通道整体不可用：%v", err)
	}
	f.errs["chat.postMessage"] = ""
	f.status["chat.postMessage"] = http.StatusTooManyRequests
	if err := ch.Send(ctx, "C1", notify.Rendered{Text: "x"}); !errors.As(err, &retry) || retry.Seconds != 7 {
		t.Fatalf("限流：按 Retry-After 重试：%v", err)
	}
}

func slackClick(id, user, team, value string) string {
	return envelope("interactive", id, map[string]any{"type": "block_actions",
		"team": map[string]any{"id": "T1"}, "user": map[string]any{"id": user, "team_id": team, "username": "lee"},
		"channel": map[string]any{"id": "C-OPS"}, "message": map[string]any{"ts": "1700.000001"},
		"container": map[string]any{"message_ts": "1700.000001", "channel_id": "C-OPS"},
		"actions":   []any{map[string]any{"action_id": "cohive_approve", "value": value}}})
}

// 按钮：先确认，再经 Hub.Click 审批，把被点的消息改成结果；没改时给点击者发临时提示；
// 别的工作区的人点按钮一律拒绝，不碰业务层。
func TestSlackApprovalClick(t *testing.T) {
	f := newFakeSlack(t)
	ws := store.Workspace{ID: 4, Name: "ops", Notify: store.NotifyConfig{Channel: store.ProviderSlack, Target: "C-OPS"}}
	fc := &fakeCore{workspace: ws, actor: store.Account{ID: 9, Name: "lee"},
		posts: []store.BotPost{{Kind: "su", RefID: 5, WorkspaceID: 4, Provider: store.ProviderSlack, ChatID: "C-OPS", MessageID: "1700.000001"}}}
	ch := &notify.SlackChannel{API: f.api(), Logf: t.Logf}
	ch.Hub = &notify.Hub{Core: fc, Channels: map[string]notify.Channel{store.ProviderSlack: ch}, Logf: t.Logf}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ch.Run(ctx)
	<-f.opened
	waitSlackOnline(t, ch)

	// 外部工作区的人
	f.frames <- slackClick("e1", "UX", "T2", "su:4:5:y")
	f.ack(t, "e1")
	eph := f.waitCalls(t, "chat.postEphemeral", 1)
	if eph[0].body["user"] != "UX" || !strings.Contains(eph[0].body["text"].(string), "不在这个 Slack 工作区") {
		t.Fatalf("外部的人应收到拒绝提示：%+v", eph[0].body)
	}
	if len(f.callsTo("chat.update")) != 0 || len(fc.calls) != 0 {
		t.Fatalf("外部的人点按钮不应碰业务层：%v", fc.calls)
	}

	// 本工作区的人：批准，改成结果
	f.frames <- slackClick("e2", "U1", "T1", "su:4:5:y")
	f.ack(t, "e2")
	u := f.waitCalls(t, "chat.update", 1)
	b, _ := json.Marshal(u[0].body)
	if u[0].body["channel"] != "C-OPS" || u[0].body["ts"] != "1700.000001" || strings.Contains(string(b), `"actions"`) || !strings.Contains(string(b), "已批准") {
		t.Fatalf("应把被点的消息改成结果：%s", b)
	}
	if !strings.Contains(fmt.Sprint(fc.calls), "signup 9 4 5 true") {
		t.Fatalf("应以连接的账号审批申请 5：%v", fc.calls)
	}

	// 按钮数据和审批消息记录对不上：不审批，只提示点击者
	f.frames <- slackClick("e3", "U1", "T1", "su:4:6:y")
	f.ack(t, "e3")
	eph = f.waitCalls(t, "chat.postEphemeral", 2)
	if eph[1].body["user"] != "U1" || len(f.callsTo("chat.update")) != 1 {
		t.Fatalf("对不上时只给点击者提示：%+v", eph[1].body)
	}
}

// 收件：@机器人 绑群（记下频道名）、私聊发码（带上 Slack 昵称）、话题里问就回到话题里；
// 自己和别的机器人的消息、编辑、重发的事件不处理；别的工作区的人发码一律拒绝。
func TestSlackInbound(t *testing.T) {
	f := newFakeSlack(t)
	fc := &fakeCore{actor: store.Account{ID: 9, Name: "lee"}}
	dc := &fakeDTCore{link: core.LinkSubmit{Account: "lee"}}
	ch := &notify.SlackChannel{API: f.api(), Core: dc, Logf: t.Logf}
	ch.Hub = &notify.Hub{Core: fc, Channels: map[string]notify.Channel{store.ProviderSlack: ch}, Logf: t.Logf}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ch.Run(ctx)
	<-f.opened
	waitSlackOnline(t, ch)
	event := func(id, eventID string, ev map[string]any) {
		f.frames <- envelope("events_api", id, map[string]any{"team_id": "T1", "event_id": eventID, "event": ev})
		f.ack(t, id)
	}
	event("m1", "Ev1", map[string]any{"type": "app_mention", "user": "U1", "text": "<@UBOT> BindGroup GOODCODE", "channel": "C-OPS", "ts": "1.1", "thread_ts": "1.0"})
	event("m1b", "Ev1", map[string]any{"type": "app_mention", "user": "U1", "text": "<@UBOT> BindGroup GOODCODE", "channel": "C-OPS", "ts": "1.1"}) // 重发
	event("m2", "Ev2", map[string]any{"type": "message", "channel_type": "im", "user": "U1", "text": "ABCD-2345", "channel": "D1", "ts": "2.1"})
	event("m3", "Ev3", map[string]any{"type": "message", "channel_type": "im", "user": "UBOT", "text": "ABCD2345", "channel": "D1", "ts": "3.1"})
	event("m4", "Ev4", map[string]any{"type": "message", "channel_type": "im", "user": "U1", "subtype": "message_changed", "text": "ABCD2345", "channel": "D1"})
	event("m5", "Ev5", map[string]any{"type": "message", "channel_type": "im", "user": "U2", "bot_id": "B9", "text": "ABCD2345", "channel": "D2"})
	event("m6", "Ev6", map[string]any{"type": "message", "channel_type": "im", "user": "UX", "user_team": "T2", "text": "ABCD2345", "channel": "D3"})
	event("m7", "Ev7", map[string]any{"type": "message", "channel_type": "channel", "user": "U1", "text": "ABCD2345", "channel": "C-OPS"})

	posts := f.waitCalls(t, "chat.postMessage", 3)
	time.Sleep(200 * time.Millisecond) // 等不该处理的消息有机会出错
	posts = f.callsTo("chat.postMessage")
	if len(posts) != 3 {
		t.Fatalf("应只回复绑群、私聊发码、外部的人三条：%+v", posts)
	}
	byChan := map[string]slackCall{}
	for _, p := range posts {
		byChan[p.body["channel"].(string)] = p
	}
	if p := byChan["C-OPS"]; p.body["thread_ts"] != "1.0" || !strings.Contains(p.body["text"].(string), "绑定到工作空间") {
		t.Fatalf("绑群的回复应回到话题里：%+v", p.body)
	}
	if p := byChan["D1"]; !strings.Contains(p.body["text"].(string), "已收到") {
		t.Fatalf("私聊发码的回复：%+v", p.body)
	}
	if p := byChan["D3"]; !strings.Contains(p.body["text"].(string), "不在这个 Slack 工作区") {
		t.Fatalf("外部的人发码应被拒绝：%+v", p.body)
	}
	dc.mu.Lock()
	calls := strings.Join(dc.calls, "\n")
	dc.mu.Unlock()
	want := "bind|slack|C-OPS|#ops|GOODCODE\nlink|slack|ABCD-2345|T1|U1|lee.s|私聊"
	if calls != want && calls != "link|slack|ABCD-2345|T1|U1|lee.s|私聊\nbind|slack|C-OPS|#ops|GOODCODE" {
		t.Fatalf("交给业务层的应只有绑群与私聊发码（频道名、昵称由 Slack 查到）：\n%s", calls)
	}
}

// 格式：只转义 &、<、>；链接写成 <地址|文字>；没连接的人只写名字；审批消息里 @ 后括注名字。
func TestSlackFormat(t *testing.T) {
	f := notify.SlackFormat{}
	if got := f.Esc("<@U1> & <!here>"); got != "&lt;@U1&gt; &amp; &lt;!here&gt;" {
		t.Errorf("转义：%s", got)
	}
	if got := f.Link(i18n.ZhCN, "https://x.example/ws/a b|c"); got != "<https://x.example/ws/a%20b%7Cc|查看问题单>" {
		t.Errorf("链接：%s", got)
	}
	if got := f.Mention(i18n.ZhCN, "lee", nil, true); got != "lee" {
		t.Errorf("没连接：%s", got)
	}
	if got := f.Mention(i18n.ZhCN, "lee", &store.ChatIdentity{UserID: "U1"}, true); got != "<@U1>（lee）" {
		t.Errorf("审批消息里：%s", got)
	}
	if got := f.Code("a`b<c"); got != "`a'b&lt;c`" {
		t.Errorf("等宽：%s", got)
	}
}

// 仓库里的应用清单与代码对得上：Socket 模式、按钮回调、两个事件、用到的权限都在，没有多要读历史消息的权限。
func TestSlackManifest(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("..", "..", "deploy", "slack-app-manifest.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	m := string(b)
	for _, want := range []string{"socket_mode_enabled: true", "is_enabled: true", "messages_tab_enabled: true",
		"messages_tab_read_only_enabled: false", "- app_mention", "- message.im", "- chat:write", "- app_mentions:read",
		"- im:history", "- channels:read", "- groups:read", "- users:read", "token_rotation_enabled: false"} {
		if !strings.Contains(m, want) {
			t.Errorf("应用清单缺少 %q", want)
		}
	}
	for _, bad := range []string{"channels:history", "groups:history", "message.channels", "request_url"} {
		if strings.Contains(m, bad) {
			t.Errorf("应用清单不应有 %q（不读频道历史、不用公网回调）", bad)
		}
	}
}
