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
	"strings"
	"sync"
	"time"

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
	desc := fmt.Sprintf("钉钉 %s 失败（HTTP %d，%s）：%s", api, status, e.Code, e.Message)
	switch {
	case strings.Contains(e.Code, "QpsLimit") || status == http.StatusTooManyRequests:
		return &RetryAfterError{Seconds: 2}
	case e.Code == "Forbidden.AccessDenied.ApiCountLimitForOrg" || strings.Contains(e.Code, "ApiCountLimit"):
		return &ChannelDownError{Msg: "钉钉接口本月调用额度已用完（" + e.Code + "），通知会保留，额度恢复或扩容后自动补发；审批请在网页上处理"}
	case strings.HasPrefix(e.Code, "Forbidden.AccessDenied") || status == http.StatusUnauthorized:
		return &ChannelDownError{Msg: "钉钉应用没有所需权限或凭据无效（" + e.Code + "）：请在钉钉开发者后台检查应用权限与发布状态"}
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
		return "", errors.New("构造钉钉请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("获取钉钉访问令牌失败：%v", unwrapURL(err))
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
			return "", fmt.Errorf("获取钉钉访问令牌失败（HTTP %d）", resp.StatusCode)
		}
		return "", &ChannelDownError{Msg: fmt.Sprintf("钉钉应用凭据无效（HTTP %d，%s）：请检查 COHIVE_DINGTALK_CLIENT_ID 与 COHIVE_DINGTALK_CLIENT_SECRET",
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
		return errors.New("构造钉钉请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-acs-dingtalk-access-token", tok)
	resp, err := d.client().Do(req)
	if err != nil {
		return fmt.Errorf("调用钉钉 %s 失败：%v", api, unwrapURL(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusOK {
		if out != nil && len(raw) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				return fmt.Errorf("解析钉钉 %s 的响应失败", api)
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
	if c.Callback {
		in["callbackType"] = "STREAM"
	}
	if len(c.AtUserIDs) == 0 {
		in["imGroupOpenDeliverModel"] = map[string]any{"robotCode": d.robotCode()}
	}
	return d.call(ctx, "投放卡片", http.MethodPost, "/v1.0/card/instances/createAndDeliver", in, nil)
}

// UpdateCard 按卡片编号更新卡片的模板变量（只改给出的变量）。
func (d *DingTalk) UpdateCard(ctx context.Context, outTrackID string, params map[string]string) error {
	in := map[string]any{
		"outTrackId":        outTrackID,
		"cardData":          map[string]any{"cardParamMap": params},
		"cardUpdateOptions": map[string]bool{"updateCardDataByKey": true},
		"userIdType":        1,
	}
	return d.call(ctx, "更新卡片", http.MethodPut, "/v1.0/card/instances", in, nil)
}

// GroupMarkdown 以机器人身份往群里发一条 Markdown 消息（不支持 @，降级时用）。
func (d *DingTalk) GroupMarkdown(ctx context.Context, chatID, title, text string) error {
	param, _ := json.Marshal(map[string]string{"title": title, "text": text})
	in := map[string]any{"msgKey": "sampleMarkdown", "msgParam": string(param), "openConversationId": chatID, "robotCode": d.robotCode()}
	return d.call(ctx, "发送群消息", http.MethodPost, "/v1.0/robot/groupMessages/send", in, nil)
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
		return errors.New("会话 Webhook 不是钉钉的地址，已拒绝发送")
	}
	body, _ := json.Marshal(map[string]any{"msgtype": "markdown", "markdown": map[string]string{"title": title, "text": text}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(body))
	if err != nil {
		return errors.New("构造钉钉请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := d.client().Do(req)
	if err != nil {
		return fmt.Errorf("回复钉钉消息失败：%v", unwrapURL(err))
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK || out.ErrCode != 0 {
		return fmt.Errorf("回复钉钉消息失败（HTTP %d，%d）：%s", resp.StatusCode, out.ErrCode, out.ErrMsg)
	}
	return nil
}

// DingTalkFormat 是钉钉卡片与 Markdown 消息的格式。用户输入里的 Markdown 记号换成全角字符：
// 钉钉 Markdown 对反斜杠转义的支持未核实，全角字符读起来一样、不会被当成格式。
type DingTalkFormat struct{}

var dtEscaper = strings.NewReplacer(
	"\\", "＼", "*", "＊", "_", "＿", "`", "｀", "[", "［", "]", "］", "(", "（", ")", "）",
	"#", "＃", "<", "＜", ">", "＞", "|", "｜", "~", "～", "@", "＠", "!", "！",
	"\r\n", " ", "\n", " ", "\r", " ",
)

func (DingTalkFormat) Esc(s string) string { return dtEscaper.Replace(s) }
func (DingTalkFormat) Code(s string) string {
	return "`" + strings.ReplaceAll(dtEscaper.Replace(s), "｀", "") + "`"
}
func (DingTalkFormat) Link(u string) string {
	if !strings.HasPrefix(u, "https://") && !strings.HasPrefix(u, "http://") {
		return dtEscaper.Replace(u)
	}
	return "[查看问题单](" + strings.NewReplacer("(", "%28", ")", "%29", " ", "%20").Replace(u) + ")"
}

// Mention 连接了钉钉时写成 @名字（钉钉按卡片的 atUserIds 提醒到人），没连接时只写名字。
func (DingTalkFormat) Mention(label string, id *store.ChatIdentity, _ bool) string {
	if id == nil || id.UserID == "" {
		return label
	}
	return "@" + label
}
