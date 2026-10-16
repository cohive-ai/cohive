package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Telegram 是 Telegram 机器人接口的最小客户端。只需要出站访问。
type Telegram struct {
	Token   string
	BaseURL string       // 默认 https://api.telegram.org，测试时替换
	HTTP    *http.Client // 默认带 40 秒超时（长轮询 30 秒）
}

// RetryAfterError 表示被限流，需要等待指定秒数再重试。
type RetryAfterError struct{ Seconds int }

func (e *RetryAfterError) Error() string {
	return fmt.Sprintf("Telegram 限流，%d 秒后重试", e.Seconds)
}

// ChannelDownError 表示通道整体不可用（例如机器人令牌错误或已重置），与具体哪条通知无关。
type ChannelDownError struct{ Msg string }

func (e *ChannelDownError) Error() string { return e.Msg }

// ChatMigratedError 表示群已升级为超级群，需改用新的群编号。
type ChatMigratedError struct{ NewChatID string }

func (e *ChatMigratedError) Error() string {
	return fmt.Sprintf("群已升级为超级群，新的群编号是 %s", e.NewChatID)
}

// PermanentError 表示不值得重试的错误（例如群编号错误、机器人被移出群）。
type PermanentError struct{ Msg string }

func (e *PermanentError) Error() string { return e.Msg }

// IsPermanent 判断错误是否不值得重试。
func IsPermanent(err error) bool {
	var p *PermanentError
	return errors.As(err, &p)
}

type Update struct {
	UpdateID      int64       `json:"update_id"`
	Message       *TGMessage  `json:"message"`
	CallbackQuery *TGCallback `json:"callback_query"`
}

// TGCallback 是用户点了消息下方按钮。From 由 Telegram 填写，可以作为身份依据。
type TGCallback struct {
	ID      string     `json:"id"`
	From    TGUser     `json:"from"`
	Message *TGMessage `json:"message"`
	Data    string     `json:"data"`
}

// Button 是消息下方的一个内联按钮，Data 不超过 64 字节。
type Button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

type TGMessage struct {
	MessageID int64   `json:"message_id"`
	Text      string  `json:"text"`
	Chat      TGChat  `json:"chat"`
	From      *TGUser `json:"from"`
}

type TGChat struct {
	ID    int64  `json:"id"`
	Type  string `json:"type"` // private、group、supergroup、channel
	Title string `json:"title"`
}

type TGUser struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
}

type tgResponse struct {
	OK          bool            `json:"ok"`
	ErrorCode   int             `json:"error_code"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
	Parameters  struct {
		RetryAfter      int   `json:"retry_after"`
		MigrateToChatID int64 `json:"migrate_to_chat_id"`
	} `json:"parameters"`
}

func (t *Telegram) client() *http.Client {
	if t.HTTP != nil {
		return t.HTTP
	}
	return &http.Client{Timeout: 40 * time.Second}
}

func (t *Telegram) endpoint(method string) string {
	base := t.BaseURL
	if base == "" {
		base = "https://api.telegram.org"
	}
	return base + "/bot" + t.Token + "/" + method
}

// call 调用接口。接口地址包含令牌，所以网络错误只保留底层原因，不带地址。
func (t *Telegram) call(ctx context.Context, method string, req *http.Request) (json.RawMessage, error) {
	resp, err := t.client().Do(req.WithContext(ctx))
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("调用 Telegram %s 失败：%v", method, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var r tgResponse
	if json.Unmarshal(body, &r) != nil {
		return nil, fmt.Errorf("Telegram %s 返回 HTTP %d", method, resp.StatusCode)
	}
	if r.OK {
		return r.Result, nil
	}
	switch {
	case r.ErrorCode == 429:
		sec := r.Parameters.RetryAfter
		if sec <= 0 {
			sec = 30
		}
		return nil, &RetryAfterError{Seconds: sec}
	case r.Parameters.MigrateToChatID != 0:
		return nil, &ChatMigratedError{NewChatID: strconv.FormatInt(r.Parameters.MigrateToChatID, 10)}
	case r.ErrorCode == 401 || r.ErrorCode == 404:
		return nil, &ChannelDownError{Msg: fmt.Sprintf("Telegram 机器人令牌无效（%d）：%s", r.ErrorCode, r.Description)}
	case r.ErrorCode == 400 || r.ErrorCode == 403:
		return nil, &PermanentError{Msg: fmt.Sprintf("Telegram %s 拒绝（%d）：%s", method, r.ErrorCode, r.Description)}
	}
	return nil, fmt.Errorf("Telegram %s 失败（%d）：%s", method, r.ErrorCode, r.Description)
}

// Send 往群里发一条 HTML 格式的消息。
func (t *Telegram) Send(ctx context.Context, chatID, text string) error {
	return t.send(ctx, map[string]any{
		"chat_id": chatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true,
	})
}

// Reply 回复某条消息。
func (t *Telegram) Reply(ctx context.Context, chatID, replyTo int64, text string) error {
	return t.send(ctx, map[string]any{
		"chat_id": chatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true,
		"reply_parameters": map[string]any{"message_id": replyTo, "allow_sending_without_reply": true},
	})
}

func (t *Telegram) send(ctx context.Context, payload map[string]any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, t.endpoint("sendMessage"), bytes.NewReader(b))
	if err != nil {
		return errors.New("构造 Telegram 请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	_, err = t.call(ctx, "sendMessage", req)
	return err
}

// GetUpdates 长轮询接收机器人收到的消息。
func (t *Telegram) GetUpdates(ctx context.Context, offset int64, timeoutSec int) ([]Update, error) {
	q := url.Values{}
	q.Set("offset", strconv.FormatInt(offset, 10))
	q.Set("timeout", strconv.Itoa(timeoutSec))
	q.Set("allowed_updates", `["message","callback_query"]`)
	req, err := http.NewRequest(http.MethodGet, t.endpoint("getUpdates")+"?"+q.Encode(), nil)
	if err != nil {
		return nil, errors.New("构造 Telegram 请求失败")
	}
	raw, err := t.call(ctx, "getUpdates", req)
	if err != nil {
		return nil, err
	}
	var ups []Update
	if err := json.Unmarshal(raw, &ups); err != nil {
		return nil, errors.New("解析 Telegram 消息失败")
	}
	return ups, nil
}

// GetMe 返回机器人自己的用户名，用于识别 /命令@机器人名。
func (t *Telegram) GetMe(ctx context.Context) (string, error) {
	req, err := http.NewRequest(http.MethodGet, t.endpoint("getMe"), nil)
	if err != nil {
		return "", errors.New("构造 Telegram 请求失败")
	}
	raw, err := t.call(ctx, "getMe", req)
	if err != nil {
		return "", err
	}
	var u TGUser
	if err := json.Unmarshal(raw, &u); err != nil {
		return "", errors.New("解析 Telegram 机器人信息失败")
	}
	return u.Username, nil
}

func keyboard(buttons [][]Button) (map[string]any, error) {
	for _, row := range buttons {
		for _, b := range row {
			if len(b.Data) == 0 || len(b.Data) > 64 {
				return nil, fmt.Errorf("按钮回调数据长度应为 1 到 64 字节：%q", b.Data)
			}
		}
	}
	if buttons == nil {
		buttons = [][]Button{}
	}
	return map[string]any{"inline_keyboard": buttons}, nil
}

func (t *Telegram) post(ctx context.Context, method string, payload map[string]any) (json.RawMessage, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, t.endpoint(method), bytes.NewReader(b))
	if err != nil {
		return nil, errors.New("构造 Telegram 请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	return t.call(ctx, method, req)
}

// SendButtons 发一条带内联按钮的消息，返回消息编号（之后可编辑）。
func (t *Telegram) SendButtons(ctx context.Context, chatID int64, text string, buttons [][]Button) (int64, error) {
	kb, err := keyboard(buttons)
	if err != nil {
		return 0, err
	}
	raw, err := t.post(ctx, "sendMessage", map[string]any{
		"chat_id": chatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true, "reply_markup": kb,
	})
	if err != nil {
		return 0, err
	}
	var m TGMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return 0, errors.New("解析 Telegram 消息失败")
	}
	return m.MessageID, nil
}

// EditMessage 修改消息文字；buttons 为 nil 时去掉按钮。内容没变时 Telegram 报错，这里当作成功。
func (t *Telegram) EditMessage(ctx context.Context, chatID, messageID int64, text string, buttons [][]Button) error {
	kb, err := keyboard(buttons)
	if err != nil {
		return err
	}
	_, err = t.post(ctx, "editMessageText", map[string]any{
		"chat_id": chatID, "message_id": messageID, "text": text, "parse_mode": "HTML",
		"disable_web_page_preview": true, "reply_markup": kb,
	})
	if err != nil && strings.Contains(err.Error(), "message is not modified") {
		return nil
	}
	return err
}

// AnswerCallback 回应按钮点击（客户端上的小提示），必须在点击后尽快调用。
func (t *Telegram) AnswerCallback(ctx context.Context, callbackID, text string) error {
	_, err := t.post(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": callbackID, "text": text})
	return err
}

// SendText 往任意聊天发一条 HTML 消息。
func (t *Telegram) SendText(ctx context.Context, chatID int64, text string) error {
	_, err := t.post(ctx, "sendMessage", map[string]any{
		"chat_id": chatID, "text": text, "parse_mode": "HTML", "disable_web_page_preview": true,
	})
	return err
}
