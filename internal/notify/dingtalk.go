package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// DingTalk 是钉钉开放接口（企业内部应用）的最小客户端，只需要出站访问。
// 应用凭据只放在内存里：不进日志、错误信息，访问令牌放在请求头里，不进地址。
type DingTalk struct {
	ClientID     string // 应用的 Client ID（原 AppKey）
	ClientSecret string // 应用的 Client Secret（原 AppSecret）
	RobotCode    string // 机器人编码；企业内部应用一般等于 Client ID，为空时用 Client ID
	BaseURL      string // 新版接口地址，默认 https://api.dingtalk.com；测试时替换
	HTTP         *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// DingTalkAPI 是默认的钉钉新版接口地址。
const DingTalkAPI = "https://api.dingtalk.com"

func (d *DingTalk) base() string {
	if d.BaseURL != "" {
		return strings.TrimRight(d.BaseURL, "/")
	}
	return DingTalkAPI
}

func (d *DingTalk) robotCode() string {
	if d.RobotCode != "" {
		return d.RobotCode
	}
	return d.ClientID
}

func (d *DingTalk) client() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// dtError 是钉钉新版接口的错误响应。
type dtError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// classify 把钉钉的错误归到通道的四类错误里：限流、通道整体不可用、不值得重试、其余（照常重试）。
func classify(api string, status int, e dtError) error {
	desc := fmt.Sprintf("DingTalk %s failed (HTTP %d, %s): %s", api, status, e.Code, e.Message)
	switch {
	case strings.Contains(e.Code, "QpsLimit") || status == http.StatusTooManyRequests:
		return &RetryAfterError{Seconds: 2}
	case e.Code == "Forbidden.AccessDenied.ApiCountLimitForOrg" || strings.Contains(e.Code, "ApiCountLimit"):
		return &ChannelDownError{Msg: "DingTalk API quota for this month is used up (" + e.Code + "); notifications are kept and will be sent once the quota recovers or is raised; handle approvals on the web instead"}
	case strings.HasPrefix(e.Code, "Forbidden.AccessDenied") || status == http.StatusUnauthorized:
		return &ChannelDownError{Msg: "DingTalk app lacks the required permissions or its credentials are invalid (" + e.Code + "): check the app permissions and release status in the DingTalk developer console"}
	case status == http.StatusBadRequest || status == http.StatusNotFound || status == http.StatusForbidden:
		return &PermanentError{Msg: desc}
	}
	return errors.New(desc)
}

// accessToken 返回缓存的企业内部应用访问令牌，到期前 5 分钟刷新；并发调用只取一次。
func (d *DingTalk) accessToken(ctx context.Context) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.token != "" && time.Until(d.tokenExp) > 5*time.Minute {
		return d.token, nil
	}
	body, _ := json.Marshal(map[string]string{"appKey": d.ClientID, "appSecret": d.ClientSecret})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.base()+"/v1.0/oauth2/accessToken", bytes.NewReader(body))
	if err != nil {
		return "", errors.New("failed to build DingTalk request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get DingTalk access token: %v", unwrapURL(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var out struct {
		AccessToken string `json:"accessToken"`
		ExpireIn    int    `json:"expireIn"`
		dtError
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || out.AccessToken == "" {
		if resp.StatusCode >= 500 {
			return "", fmt.Errorf("failed to get DingTalk access token (HTTP %d)", resp.StatusCode)
		}
		return "", &ChannelDownError{Msg: fmt.Sprintf("DingTalk app credentials are invalid (HTTP %d, %s): check COHIVE_DINGTALK_CLIENT_ID and COHIVE_DINGTALK_CLIENT_SECRET",
			resp.StatusCode, out.Code)}
	}
	if out.ExpireIn <= 0 {
		out.ExpireIn = 7200
	}
	d.token, d.tokenExp = out.AccessToken, time.Now().Add(time.Duration(out.ExpireIn)*time.Second)
	return d.token, nil
}

func (d *DingTalk) dropToken() {
	d.mu.Lock()
	d.token = ""
	d.mu.Unlock()
}

// unwrapURL 去掉网络错误里的地址，只留底层原因（地址本身不含密钥，统一处理便于阅读）。
func unwrapURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

// call 调新版接口。api 是给人看的接口名。
func (d *DingTalk) call(ctx context.Context, api, method, path string, in, out any) error {
	tok, err := d.accessToken(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, d.base()+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("failed to build DingTalk request")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", tok)
	resp, err := d.client().Do(req)
	if err != nil {
		return fmt.Errorf("DingTalk %s request failed: %v", api, unwrapURL(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusOK {
		if out != nil && len(raw) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("failed to parse DingTalk %s response", api)
			}
		}
		return nil
	}
	var e dtError
	_ = json.Unmarshal(raw, &e)
	if resp.StatusCode == http.StatusUnauthorized || e.Code == "InvalidAuthentication" {
		d.dropToken() // 令牌失效：下次重取
	}
	return classify(api, resp.StatusCode, e)
}

// CardDelivery 是投放到群里的一张互动卡片。
type CardDelivery struct {
	ChatID     string            // 群的 openConversationId
	TemplateID string            // 卡片模板编号
	OutTrackID string            // 卡片编号，同一编号只对应一张卡
	Params     map[string]string // 模板变量
	AtUserIDs  map[string]string // @ 的人：userId → 名字
	Callback   bool              // 按钮回调走 Stream
	Alert      string            // 推送通知与会话列表摘要
}

// DeliverCard 创建并投放一张互动卡片到群里。
func (d *DingTalk) DeliverCard(ctx context.Context, c CardDelivery) error {
	in := map[string]any{
		"cardTemplateId": c.TemplateID,
		"outTrackId":     c.OutTrackID,
		"cardData":       map[string]any{"cardParamMap": c.Params},
		"openSpaceId":    "dtv1.card//IM_GROUP." + c.ChatID,
		"userIdType":     1,
		"imGroupOpenSpaceModel": map[string]any{
			"supportForward":  false,
			"lastMessageI18n": map[string]string{"ZH_CN": c.Alert},
			"notification":    map[string]any{"alertContent": c.Alert, "notificationOff": false},
		},
		"imGroupOpenDeliverModel": map[string]any{"robotCode": d.robotCode(), "atUserIds": c.AtUserIDs},
	}
	// 卡片内 @ 与群消息提醒分别声明，使用同一批钉钉 userId。
	if len(c.AtUserIDs) > 0 {
		ids := make([]string, 0, len(c.AtUserIDs))
		for id := range c.AtUserIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		in["cardAtUserIds"] = ids
	}
	if c.Callback {
		in["callbackType"] = "STREAM"
	}
	if len(c.AtUserIDs) == 0 {
		in["imGroupOpenDeliverModel"] = map[string]any{"robotCode": d.robotCode()}
	}
	return d.call(ctx, "deliver card", http.MethodPost, "/v1.0/card/instances/createAndDeliver", in, nil)
}

// UpdateCard 按卡片编号更新卡片的模板变量（只改给出的变量）。
func (d *DingTalk) UpdateCard(ctx context.Context, outTrackID string, params map[string]string) error {
	in := map[string]any{
		"outTrackId":        outTrackID,
		"cardData":          map[string]any{"cardParamMap": params},
		"cardUpdateOptions": map[string]bool{"updateCardDataByKey": true},
		"userIdType":        1,
	}
	return d.call(ctx, "update card", http.MethodPut, "/v1.0/card/instances", in, nil)
}

// 卡片专用标签不能发到普通 Markdown；用户输入的尖括号已由 Formatter 转义。
var dtCardMention = regexp.MustCompile(`<a atId="[^"]*">([^<]*)</a>`)

// GroupMarkdown 以机器人身份往群里发一条 Markdown 消息（不支持 @，降级时用）。
func (d *DingTalk) GroupMarkdown(ctx context.Context, chatID, title, text string) error {
	text = dtCardMention.ReplaceAllString(text, "$1")
	param, _ := json.Marshal(map[string]string{"title": title, "text": text})
	in := map[string]any{"msgKey": "sampleMarkdown", "msgParam": string(param), "openConversationId": chatID, "robotCode": d.robotCode()}
	return d.call(ctx, "send group message", http.MethodPost, "/v1.0/robot/groupMessages/send", in, nil)
}

// DirectMarkdown 以机器人身份给一个人单聊发 Markdown 消息（机器人单聊批量发送接口，只发给一个人）。
// 用户编号无效或不在应用可用范围内是不值得重试的错误；被流控时让调用方稍后重试。
func (d *DingTalk) DirectMarkdown(ctx context.Context, userID, title, text string) error {
	text = dtCardMention.ReplaceAllString(text, "$1")
	param, _ := json.Marshal(map[string]string{"title": title, "text": text})
	in := map[string]any{"robotCode": d.robotCode(), "userIds": []string{userID}, "msgKey": "sampleMarkdown", "msgParam": string(param)}
	var out struct {
		InvalidStaffIDList        []string `json:"invalidStaffIdList"`
		FlowControlledStaffIDList []string `json:"flowControlledStaffIdList"`
	}
	if err := d.call(ctx, "send direct message", http.MethodPost, "/v1.0/robot/oToMessages/batchSend", in, &out); err != nil {
		return err
	}
	for _, id := range out.InvalidStaffIDList {
		if id == userID {
			return &PermanentError{Msg: "DingTalk direct message rejected: user ID is invalid or the user is outside the app's availability scope (" + userID + ")"}
		}
	}
	for _, id := range out.FlowControlledStaffIDList {
		if id == userID {
			return &RetryAfterError{Seconds: 60}
		}
	}
	return nil
}

// webhookHostOK 判断回调里带的会话 Webhook 是否指向钉钉（或测试时配置的接口地址），防止被引到别处。
func (d *DingTalk) webhookHostOK(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if b, err := url.Parse(d.base()); err == nil && d.BaseURL != "" && u.Scheme == b.Scheme && strings.EqualFold(u.Host, b.Host) {
		return true
	}
	return u.Scheme == "https" && (host == "dingtalk.com" || strings.HasSuffix(host, ".dingtalk.com"))
}

// Reply 用机器人消息回调里带的会话 Webhook 回复一条 Markdown 消息（不需要访问令牌）。
func (d *DingTalk) Reply(ctx context.Context, webhook, title, text string) error {
	if !d.webhookHostOK(webhook) {
		return errors.New("session webhook is not a DingTalk address, refusing to send")
	}
	body, _ := json.Marshal(map[string]any{"msgtype": "markdown", "markdown": map[string]string{"title": title, "text": text}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return errors.New("failed to build DingTalk request")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client().Do(req)
	if err != nil {
		return fmt.Errorf("failed to reply to DingTalk message: %v", unwrapURL(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || out.ErrCode != 0 {
		return fmt.Errorf("failed to reply to DingTalk message (HTTP %d, %d): %s", resp.StatusCode, out.ErrCode, out.ErrMsg)
	}
	return nil
}

// DingTalkFormat 是钉钉卡片与 Markdown 消息的格式。用户输入里的 Markdown 记号换成全角字符：
// 钉钉 Markdown 对反斜杠转义的支持未核实，全角字符读起来一样、不会被当成格式。
type DingTalkFormat struct{}

var dtEscaper = strings.NewReplacer(
	"\\", "＼", "*", "＊", "_", "＿", "`", "｀", "[", "［", "]", "］", "(", "（", ")", "）", // i18n-ignore 转义用的全角字符，不是文案
	"#", "＃", "<", "＜", ">", "＞", "|", "｜", "~", "～", "@", "＠", "!", "！", // i18n-ignore 转义用的全角字符，不是文案
	"\r\n", " ", "\n", " ", "\r", " ",
)

func (DingTalkFormat) Esc(s string) string { return dtEscaper.Replace(s) }
func (DingTalkFormat) Code(s string) string {
	return "`" + strings.ReplaceAll(dtEscaper.Replace(s), "｀", "") + "`" // i18n-ignore 转义用的全角字符，不是文案
}
func (DingTalkFormat) Link(l i18n.Lang, u string) string {
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return dtEscaper.Replace(u)
	}
	return "[" + l.T("notify.link_text") + "](" + strings.NewReplacer("(", "%28", ")", "%29", " ", "%20").Replace(u) + ")"
}

// Mention 用绑定的钉钉 userId 生成卡片内 @ 标签；未连接时只写名字。
func (DingTalkFormat) Mention(_ i18n.Lang, label string, id *store.ChatIdentity, _ bool) string {
	if id == nil || id.UserID == "" {
		return label
	}
	if id.DisplayName != "" {
		label = (DingTalkFormat{}).Esc(id.DisplayName)
	}
	return `<a atId="` + html.EscapeString(id.UserID) + `">` + label + `</a>`
}
