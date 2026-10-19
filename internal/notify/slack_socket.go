package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/gorilla/websocket"
)

// Slack 的 Socket 模式：服务端主动向 Slack 建一条 WebSocket 长连接，事件（@机器人、私聊）与按钮点击都从这条连接推过来，
// 不需要公网地址。协议：
//  1. 用应用级令牌调 apps.connections.open 换到连接地址（地址里带一次性票据）；
//  2. 连上后先收到 {type: hello}；
//  3. 之后每条推送是 {envelope_id, type, payload}，type 为 events_api 或 interactive，都要回 {envelope_id} 确认；
//  4. {type: disconnect} 表示 Slack 要求换一条连接（定期刷新，或应用关了 Socket 模式）。
// 官方没有 Go 开发包；社区包还没到 1.0，Socket 模式部分标着“实验性”，所以和钉钉 Stream 一样自己实现。

// slackEnvelope 是 Socket 模式推来的一条消息。
type slackEnvelope struct {
	Type       string          `json:"type"`
	EnvelopeID string          `json:"envelope_id"`
	Payload    json.RawMessage `json:"payload"`
	Reason     string          `json:"reason"` // disconnect 的原因：warning、refresh_requested、link_disabled
}

// errSlackRefresh 表示 Slack 要求换一条连接：马上重连，不算掉线。
var errSlackRefresh = errors.New("Slack asked to refresh the connection")

// slackSocket 维护到 Slack 的 Socket 模式长连接，断线后按退避时间重连，直到 ctx 结束。
type slackSocket struct {
	api    *Slack
	dialer *websocket.Dialer // 为空时用 websocket.DefaultDialer
	logf   func(format string, args ...any)
	// onConnect 在每条新连接建好之前调用（查机器人自己的编号与工作区）；返回错误时放弃这次连接
	onConnect func(ctx context.Context) error
	// onEvent 处理一条 events_api 或 interactive 推送；已确认过，调用方应尽快返回，耗时操作自己开协程
	onEvent func(ctx context.Context, kind string, payload json.RawMessage)

	online atomic.Bool
	reason atomic.Pointer[string]
}

func (s *slackSocket) setDown(why string) {
	s.online.Store(false)
	s.reason.Store(&why)
}

// status 返回连接是否在线与不在线的原因。
func (s *slackSocket) status() (bool, string) {
	if s.online.Load() {
		return true, ""
	}
	if p := s.reason.Load(); p != nil {
		return false, *p
	}
	return false, i18n.T("notify.stream.connecting")
}

// Run 一直运行到 ctx 结束：连接、处理、断线后重连（3 秒起，最长 1 分钟）；Slack 要求换连接时马上重连。
func (s *slackSocket) Run(ctx context.Context) {
	wait := 3 * time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := s.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errSlackRefresh) {
			s.logf("Slack: refreshing the Socket Mode connection")
			wait = 3 * time.Second
			if time.Since(started) < 5*time.Second { // 刚连上就又要求换：稍等一下，免得空转
				sleep(ctx, time.Second)
			}
			continue
		}
		s.setDown(err.Error())
		s.logf("Slack Socket Mode disconnected, reconnecting later: %v", err)
		if time.Since(started) > time.Minute {
			wait = 3 * time.Second
		}
		sleep(ctx, wait)
		if wait < time.Minute {
			wait *= 2
		}
	}
}

// session 建一条连接并处理到断开为止。
func (s *slackSocket) session(ctx context.Context) error {
	if s.onConnect != nil {
		if err := s.onConnect(ctx); err != nil {
			return err
		}
	}
	wsURL, err := s.api.openSocket(ctx)
	if err != nil {
		return err
	}
	dialer := s.dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	conn, resp, err := dialer.DialContext(ctx, wsURL, nil)
	if resp != nil && resp.Body != nil {
		resp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("failed to connect to Slack Socket Mode: %v", err) // 错误里不带地址（地址里有票据）
	}
	defer conn.Close()

	var wmu sync.Mutex
	write := func(v any) error {
		wmu.Lock()
		defer wmu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(v)
	}
	// 读超时：Slack 会定时发 ping；我们也每分钟发一次 WebSocket ping，3 分钟没有任何消息就重连
	const idle = 3 * time.Minute
	_ = conn.SetReadDeadline(time.Now().Add(idle))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(idle)) })
	conn.SetPingHandler(func(data string) error {
		_ = conn.SetReadDeadline(time.Now().Add(idle))
		wmu.Lock()
		defer wmu.Unlock()
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(10*time.Second))
	})
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				wmu.Lock()
				_ = conn.WriteControl(websocket.CloseMessage, nil, time.Now().Add(time.Second))
				wmu.Unlock()
				conn.Close()
				return
			case <-t.C:
				wmu.Lock()
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				wmu.Unlock()
			}
		}
	}()

	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("Slack Socket Mode read failed: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(idle))
		var env slackEnvelope
		if json.Unmarshal(msg, &env) != nil {
			continue
		}
		switch env.Type {
		case "hello":
			s.online.Store(true)
			s.logf("Slack: Socket Mode connected")
		case "disconnect":
			if env.Reason == "link_disabled" {
				return errors.New("Socket Mode is turned off for this Slack app: turn it on in the app settings (Socket Mode)")
			}
			return errSlackRefresh
		case "events_api", "interactive":
			// 先确认收到（Slack 要求 3 秒内），再处理；回复与改消息走网页接口，不依赖这条确认
			if env.EnvelopeID != "" {
				if err := write(map[string]string{"envelope_id": env.EnvelopeID}); err != nil {
					return err
				}
			}
			if s.onEvent != nil {
				s.onEvent(ctx, env.Type, env.Payload)
			}
		default:
			if env.EnvelopeID != "" { // 没订阅的种类（例如斜杠命令）：确认掉，免得 Slack 重发
				_ = write(map[string]string{"envelope_id": env.EnvelopeID})
			}
		}
	}
}
