package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// Slack 是 Slack 网页接口的最小客户端，只需要出站访问。两个令牌只放在内存里：放在请求头里，不进地址、日志与错误信息。
type Slack struct {
	BotToken string // 机器人令牌（xoxb- 开头）：发消息、改消息、查频道与用户
	AppToken string // 应用级令牌（xapp- 开头，带 connections:write）：只用来换 Socket 模式的连接地址
	BaseURL  string // 接口地址，默认 https://slack.com/api；测试时替换
	HTTP     *http.Client
}

// SlackAPI 是默认的 Slack 网页接口地址。
const SlackAPI = "https://slack.com/api"

func (s *Slack) base() string {
	if s.BaseURL != "" {
		return strings.TrimRight(s.BaseURL, "/")
	}
	return SlackAPI
}

func (s *Slack) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// slackErrors 是按错误码归类的 Slack 接口错误。
var (
	// 令牌错了、被吊销、应用被卸载：整个通道不可用
	slackDown = map[string]bool{"invalid_auth": true, "not_authed": true, "account_inactive": true, "token_revoked": true,
		"token_expired": true, "no_permission": true, "missing_scope": true, "not_allowed_token_type": true, "team_access_not_granted": true}
	// 频道不对、机器人不在频道里、消息太长：重试也没用
	slackPermanent = map[string]bool{"channel_not_found": true, "not_in_channel": true, "is_archived": true, "msg_too_long": true,
		"invalid_blocks": true, "invalid_blocks_format": true, "message_not_found": true, "cant_update_message": true,
		"edit_window_closed": true, "restricted_action": true, "user_not_in_channel": true}
)

// call 调一个网页接口（JSON 请求体），把结果解到 out。api 是接口名，token 是所用的令牌。
func (s *Slack) call(ctx context.Context, api, token string, in, out any) error {
	body, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.base()+"/"+api, bytes.NewReader(body))
	if err != nil {
		return errors.New("failed to build Slack request")
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.client().Do(req)
	if err != nil {
		return fmt.Errorf("Slack %s failed: %v", api, unwrapURL(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusTooManyRequests {
		secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		if secs <= 0 {
			secs = 1
		}
		return &RetryAfterError{Seconds: secs}
	}
	if resp.StatusCode >= 500 {
		return fmt.Errorf("Slack %s failed (HTTP %d)", api, resp.StatusCode)
	}
	var head struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &head) != nil {
		return fmt.Errorf("Slack %s returned an unreadable response (HTTP %d)", api, resp.StatusCode)
	}
	if !head.OK {
		desc := fmt.Sprintf("Slack %s failed: %s", api, head.Error)
		switch {
		case head.Error == "ratelimited":
			return &RetryAfterError{Seconds: 1}
		case slackDown[head.Error]:
			return &ChannelDownError{Msg: desc + " (check the bot token, app-level token and the app's scopes; reinstall the app after changing scopes)"}
		case slackPermanent[head.Error]:
			return &PermanentError{Msg: desc}
		}
		return errors.New(desc)
	}
	if out != nil {
		_ = json.Unmarshal(raw, out)
	}
	return nil
}

// slackSelf 是机器人令牌对应的机器人与所在工作区。
type slackSelf struct {
	UserID string `json:"user_id"`
	TeamID string `json:"team_id"`
	Team   string `json:"team"`
}

// AuthTest 查机器人令牌对应的机器人用户编号与工作区。
func (s *Slack) AuthTest(ctx context.Context) (slackSelf, error) {
	var out slackSelf
	err := s.call(ctx, "auth.test", s.BotToken, map[string]any{}, &out)
	return out, err
}

// openSocket 用应用级令牌换一个 Socket 模式的连接地址（地址里带一次性票据，不能进日志）。
func (s *Slack) openSocket(ctx context.Context) (string, error) {
	var out struct {
		URL string `json:"url"`
	}
	if err := s.call(ctx, "apps.connections.open", s.AppToken, map[string]any{}, &out); err != nil {
		return "", err
	}
	if !strings.HasPrefix(out.URL, "wss://") && !strings.HasPrefix(out.URL, "ws://") {
		return "", errors.New("Slack returned a connection address that is not a WebSocket address")
	}
	return out.URL, nil
}

// slackMessage 是要发出的一条消息：text 是通知摘要与不支持消息块时的退路，blocks 是正文与按钮。
type slackMessage struct {
	Channel     string `json:"channel"`
	TS          string `json:"ts,omitempty"`
	ThreadTS    string `json:"thread_ts,omitempty"`
	User        string `json:"user,omitempty"` // 只给某人看的临时消息
	Text        string `json:"text"`
	Blocks      []any  `json:"blocks,omitempty"`
	UnfurlLinks bool   `json:"unfurl_links"`
	UnfurlMedia bool   `json:"unfurl_media"`
}

// PostMessage 发一条消息，返回消息时间戳（Slack 的消息编号，频道内唯一）。
func (s *Slack) PostMessage(ctx context.Context, m slackMessage) (string, error) {
	var out struct {
		TS string `json:"ts"`
	}
	if err := s.call(ctx, "chat.postMessage", s.BotToken, m, &out); err != nil {
		return "", err
	}
	return out.TS, nil
}

// UpdateMessage 改一条机器人自己发的消息。
func (s *Slack) UpdateMessage(ctx context.Context, m slackMessage) error {
	return s.call(ctx, "chat.update", s.BotToken, m, nil)
}

// PostEphemeral 在频道里发一条只给某人看的临时消息。
func (s *Slack) PostEphemeral(ctx context.Context, m slackMessage) error {
	return s.call(ctx, "chat.postEphemeral", s.BotToken, m, nil)
}

// ChannelName 查频道名（需要 channels:read、groups:read）；查不到时返回空串。
func (s *Slack) ChannelName(ctx context.Context, channel string) string {
	var out struct {
		Channel struct {
			Name string `json:"name"`
		} `json:"channel"`
	}
	if s.call(ctx, "conversations.info", s.BotToken, map[string]string{"channel": channel}, &out) != nil {
		return ""
	}
	return out.Channel.Name
}

// UserName 查用户在 Slack 上显示的名字（需要 users:read）；查不到时返回空串。
func (s *Slack) UserName(ctx context.Context, user string) string {
	var out struct {
		User struct {
			Name    string `json:"name"`
			Profile struct {
				DisplayName string `json:"display_name"`
				RealName    string `json:"real_name"`
			} `json:"profile"`
		} `json:"user"`
	}
	if s.call(ctx, "users.info", s.BotToken, map[string]string{"user": user}, &out) != nil {
		return ""
	}
	switch {
	case out.User.Profile.DisplayName != "":
		return out.User.Profile.DisplayName
	case out.User.Profile.RealName != "":
		return out.User.Profile.RealName
	}
	return out.User.Name
}

// SlackFormat 是 Slack 的 mrkdwn 格式。用户输入只转义 &、<、>（Slack 的规定）：这样拼不出 <!channel>、<@用户>、链接。
type SlackFormat struct{}

var slackEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func (SlackFormat) Esc(s string) string { return slackEscaper.Replace(s) }
func (SlackFormat) Code(s string) string {
	return "`" + slackEscaper.Replace(strings.ReplaceAll(s, "`", "'")) + "`"
}

// Link 写成 <地址|查看问题单>；地址里会破坏写法的字符先编码。
func (SlackFormat) Link(l i18n.Lang, u string) string {
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return slackEscaper.Replace(u)
	}
	u = strings.NewReplacer("|", "%7C", "<", "%3C", ">", "%3E", " ", "%20").Replace(u)
	return "<" + u + "|" + slackEscaper.Replace(l.T("notify.link_text")) + ">"
}

// Mention 连接了 Slack 时写成 <@用户编号>（Slack 显示对方的名字并提醒到人）；审批消息里再括注 label；没连接时只写名字。
func (SlackFormat) Mention(l i18n.Lang, label string, id *store.ChatIdentity, withLabel bool) string {
	if id == nil || id.UserID == "" {
		return label
	}
	at := "<@" + id.UserID + ">"
	if withLabel {
		return at + parens(l, label)
	}
	return at
}

// slackSection 是一段 mrkdwn 正文。Slack 限每段 3000 个字符，超出时截断。
func slackSection(text string) map[string]any {
	if r := []rune(text); len(r) > 3000 {
		text = string(r[:2999]) + "…"
	}
	return map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": text}}
}
