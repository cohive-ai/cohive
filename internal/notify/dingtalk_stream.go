package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/gorilla/websocket"
)

// 钉钉 Stream 模式：服务端主动向钉钉建一条 WebSocket 长连接，机器人消息与卡片回调都从这条连接推过来，
// 不需要公网地址。协议（与官方 Go SDK 一致）：
//  1. POST /v1.0/gateway/connections/open（带应用凭据与订阅的主题）换到连接地址和一次性票据；
//  2. 连到“地址?ticket=票据”；
//  3. 收到的每一帧是 {type, headers{topic, messageId}, data}，逐帧回 {code, headers{messageId}, message, data}；
//     SYSTEM/ping 原样回 data，SYSTEM/disconnect 表示服务端要求重连。
// 官方 SDK v0.9.1 断线重连时会向已关闭的通道发送而让进程崩溃，重连也不随 context 结束，所以这里自己实现。

const (
	streamTopicBot  = "/v1.0/im/bot/messages/get"
	streamTopicCard = "/v1.0/card/instances/callback"
)

type streamFrame struct {
	SpecVersion string            `json:"specVersion"`
	Type        string            `json:"type"`
	Headers     map[string]string `json:"headers"`
	Data        string            `json:"data"`
}

type streamAck struct {
	Code    int               `json:"code"`
	Headers map[string]string `json:"headers"`
	Message string            `json:"message"`
	Data    string            `json:"data"`
}

// dingStream 维护到钉钉的 Stream 长连接，断线后按退避时间重连，直到 ctx 结束。
type dingStream struct {
	api    *DingTalk
	dialer *websocket.Dialer // 为空时用 websocket.DefaultDialer
	ua     string
	logf   func(format string, args ...any)
	// onBot 处理一条机器人消息（群里 @ 机器人、单聊）；调用方应尽快返回，耗时操作自己开协程
	onBot func(ctx context.Context, data []byte)
	// onCard 处理一次卡片回调，返回值作为回调响应（可带新的卡片数据）；钉钉要求 2 秒内响应
	onCard func(ctx context.Context, data []byte) any

	online atomic.Bool
	reason atomic.Pointer[string]
}

func (s *dingStream) setDown(why string) {
	s.online.Store(false)
	s.reason.Store(&why)
}

// status 返回连接是否在线与不在线的原因。
func (s *dingStream) status() (bool, string) {
	if s.online.Load() {
		return true, ""
	}
	if p := s.reason.Load(); p != nil {
		return false, *p
	}
	return false, i18n.T("notify.stream.connecting")
}

// open 换取连接地址与票据。
func (s *dingStream) open(ctx context.Context) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"clientId": s.api.ClientID, "clientSecret": s.api.ClientSecret, "ua": s.ua, "localIp": "",
		"subscriptions": []map[string]string{{"type": "CALLBACK", "topic": streamTopicBot}, {"type": "CALLBACK", "topic": streamTopicCard}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.api.base()+"/v1.0/gateway/connections/open", bytes.NewReader(body))
	if err != nil {
		return "", errors.New("failed to build DingTalk request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := s.api.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to connect to DingTalk Stream: %v", unwrapURL(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var out struct {
		Endpoint string `json:"endpoint"`
		Ticket   string `json:"ticket"`
		dtError
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || out.Endpoint == "" || out.Ticket == "" {
		// 消息服务被暂停（多半是 Webhook 与 Stream 的月度额度用完）、凭据错误等都在这里
		return "", fmt.Errorf("DingTalk Stream refused the connection (HTTP %d, %s): %s", resp.StatusCode, out.Code, out.Message)
	}
	if !strings.HasPrefix(out.Endpoint, "wss://") && !strings.HasPrefix(out.Endpoint, "ws://") {
		return "", errors.New("DingTalk Stream returned a connection address that is not a WebSocket address")
	}
	sep := "?"
	if strings.Contains(out.Endpoint, "?") {
		sep = "&"
	}
	return out.Endpoint + sep + "ticket=" + out.Ticket, nil
}

// Run 一直运行到 ctx 结束：连接、处理、断线后重连（3 秒起，最长 1 分钟）。
func (s *dingStream) Run(ctx context.Context) {
	wait := 3 * time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := s.session(ctx)
		if ctx.Err() != nil {
			return
		}
		s.setDown(err.Error())
		s.logf("DingTalk Stream disconnected, reconnecting later: %v", err)
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
func (s *dingStream) session(ctx context.Context) error {
	wsURL, err := s.open(ctx)
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
		return fmt.Errorf("failed to connect to DingTalk Stream: %v", err) // 错误里不带地址（地址里有票据）
	}
	defer conn.Close()
	s.online.Store(true)
	s.logf("DingTalk: Stream connected")

	var wmu sync.Mutex
	write := func(v any) error {
		wmu.Lock()
		defer wmu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		return conn.WriteJSON(v)
	}
	// 读超时：钉钉会定时发 ping；我们也每分钟发一次 WebSocket ping，3 分钟没有任何消息就重连
	const idle = 3 * time.Minute
	_ = conn.SetReadDeadline(time.Now().Add(idle))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(idle)) })
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
			return fmt.Errorf("DingTalk Stream read failed: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(idle))
		var f streamFrame
		if json.Unmarshal(msg, &f) != nil || f.Headers == nil {
			continue
		}
		topic, id := f.Headers["topic"], f.Headers["messageId"]
		ack := func(data string) error {
			return write(streamAck{Code: 200, Headers: map[string]string{"contentType": "application/json", "messageId": id},
				Message: "OK", Data: data})
		}
		switch {
		case f.Type == "SYSTEM" && topic == "ping":
			if err := ack(f.Data); err != nil {
				return err
			}
		case f.Type == "SYSTEM" && topic == "disconnect":
			return errors.New("DingTalk requested a reconnect")
		case f.Type == "CALLBACK" && topic == streamTopicBot:
			// 先确认收到，再处理（回复走会话 Webhook，不依赖这条确认）
			if err := ack(""); err != nil {
				return err
			}
			if s.onBot != nil {
				s.onBot(ctx, []byte(f.Data))
			}
		case f.Type == "CALLBACK" && topic == streamTopicCard:
			// 卡片回调在单独的协程里处理，不挡住后续帧；响应要在 2 秒内写回
			data := []byte(f.Data)
			go func() {
				var resp any = map[string]any{}
				if s.onCard != nil {
					resp = s.onCard(ctx, data)
				}
				b, _ := json.Marshal(map[string]any{"response": resp})
				if err := ack(string(b)); err != nil {
					s.logf("failed to answer DingTalk card callback: %v", err)
				}
			}()
		default:
			_ = write(streamAck{Code: 404, Headers: map[string]string{"messageId": id}, Message: "not handled"})
		}
	}
}
