package notify

import (
	"context"
	"strings"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// Channel 是一种通讯工具（Telegram、钉钉、Slack）。服务器为每种启用的通讯工具建一个实例，可以同时启用多种；
// 工作空间绑定哪种通讯工具的群，通知和审批消息就经哪个实例发出。收件循环（长轮询、长连接）由各实现自己跑，
// 收到文字消息时交给 Inbox.Handle（见 inbox.go），收到按钮点击时交给 Hub.Click。
type Channel interface {
	Provider() string  // store.ProviderTelegram、store.ProviderDingTalk、store.ProviderSlack
	Format() Formatter // 本通讯工具的文本格式与 @ 写法
	// Send 往群里发一条通知（问题单通知、加入通知）。
	Send(ctx context.Context, chat string, m Rendered) error
	// SendApproval 发一条带“批准 / 拒绝”按钮的审批消息，返回消息编号（之后据此改成结果）。
	SendApproval(ctx context.Context, chat string, m Rendered, a Approval) (msgRef string, err error)
	// FinishApproval 把审批消息改成结果、去掉按钮。
	FinishApproval(ctx context.Context, chat, msgRef string, m Rendered) error
}

// StatusReporter 是通道可选的能力：收件循环（长轮询、长连接）是否在线，不在线时给出原因，供服务器管理页显示。
type StatusReporter interface {
	Status() (online bool, detail string)
}

// DirectSender 是通道可选的能力：机器人主动私聊一个人（关注者、留言里被 @ 的人收到的个人通知）。
// to 是此人在本通讯工具上的连接（UserID 必有；钉钉、Slack 另带 Tenant）。m 按本通讯工具的格式写好，
// 私聊里不 @ 人，m.Mentions 为空。错误沿用群通知的分类：限流 *RetryAfterError、通道整体不可用 *ChannelDownError、
// 不值得重试 *PermanentError（对方屏蔽机器人、没开单聊权限、用户编号无效等），其余为普通错误。
type DirectSender interface {
	SendDirect(ctx context.Context, to store.ChatIdentity, m Rendered) error
}

var (
	_ Channel        = (*Bot)(nil)
	_ Channel        = (*DingTalkChannel)(nil)
	_ Channel        = (*SlackChannel)(nil)
	_ StatusReporter = (*Bot)(nil)
	_ StatusReporter = (*DingTalkChannel)(nil)
	_ StatusReporter = (*SlackChannel)(nil)
	_ DirectSender   = (*Bot)(nil)
	_ DirectSender   = (*DingTalkChannel)(nil)
	_ DirectSender   = (*SlackChannel)(nil)
)

// Formatter 是一种通讯工具的消息格式。
type Formatter interface {
	Esc(s string) string                 // 转义纯文本
	Code(s string) string                // 等宽显示（参数为未转义的纯文本）
	Link(l i18n.Lang, url string) string // 消息末尾的问题单链接；l 是消息的语言（链接文字用）
	// Mention 是提到一个人。label 为已转义的名字；id 为这个人在本通讯工具上的连接，没有时为空。
	// withLabel 为真时，按用户名提及后再括注 label（审批消息里用）；l 是消息的语言（括号写法随语言）。
	Mention(l i18n.Lang, label string, id *store.ChatIdentity, withLabel bool) string
}

// Rendered 是渲染好的一条消息。
type Rendered struct {
	Title    string               // 钉钉卡片标题与会话列表摘要；Telegram 不用
	Text     string               // 按本通讯工具格式写好的正文
	Mentions []store.ChatIdentity // 正文里按编号 @ 到的人（钉钉要单独传）
	// Key 是幂等键：同一条通知重发时不变（钉钉用作卡片编号，重发不会出现第二张卡）；为空时由通道自己生成
	Key string
	// Lang 是消息的语言（群所属工作空间的语言）；通道发降级提示等附加文字时用。为空时用服务器缺省语言。
	Lang i18n.Lang
}

// lang 是消息的语言；没记录时用服务器缺省语言。
func (r Rendered) lang() i18n.Lang {
	if r.Lang == "" {
		return i18n.Default()
	}
	return r.Lang
}

// Approval 是审批消息对应的申请：按钮数据与审批消息记录都据此生成。
type Approval struct {
	Kind        string // su（注册与加入申请）、da（添加设备）
	WorkspaceID int64
	RefID       int64
}

// ChatUser 是通讯工具上的一个人，字段由通讯工具平台填写（不来自消息内容）。
type ChatUser struct {
	Provider string
	Tenant   string
	UserID   string
	Handle   string
	Name     string
}

// Click 是群里有人点了审批按钮。
type Click struct {
	// ChatID 是按钮所在的群（群编号全局唯一，不带企业）；钉钉卡片回调不带群，为空，以审批消息记录为准
	Provider, ChatID, MsgRef string
	From                     ChatUser // 点击者；钉钉的 Tenant 为 corpId
	// Approval 来自按钮数据（Telegram）；钉钉卡片回调不带，为零值，由 MsgRef 在审批消息记录里反查。
	Approval Approval
	Approve  bool
}

// ClickResult 是点击的处理结果：Toast 是给点击者的提示；Result 不为空时，通道用它改被点的那条消息；
// After 不为空时在回应点击之后调用（把其他群里的同一条审批消息改成结果、发加入通知），钉钉要异步调用。
type ClickResult struct {
	Toast  string
	Result *Rendered
	After  func(ctx context.Context)
}

// Person 是消息里提到的一个人：名字与他在各通讯工具上的连接。
type Person struct {
	Name, DisplayName string
	Chats             []store.ChatIdentity
}

// identity 返回此人在某种通讯工具上的连接；没有时为空。
func (p Person) identity(provider string) *store.ChatIdentity {
	for i := range p.Chats {
		if p.Chats[i].Provider == provider {
			return &p.Chats[i]
		}
	}
	return nil
}

// personOf 由成员资格生成 Person：Telegram 连接取自成员字段，其他通讯工具的连接由 extra 给出。
func personOf(m store.Member, extra []store.ChatIdentity) Person {
	p := Person{Name: m.Name, DisplayName: m.DisplayName}
	if m.TelegramUserID != nil || m.TelegramUsername != "" {
		id := store.ChatIdentity{AccountID: m.AccountID, Provider: store.ProviderTelegram, Handle: m.TelegramUsername}
		if m.TelegramUserID != nil {
			id.UserID = int64String(*m.TelegramUserID)
		}
		p.Chats = append(p.Chats, id)
	}
	for _, c := range extra {
		if c.Provider != store.ProviderTelegram {
			p.Chats = append(p.Chats, c)
		}
	}
	return p
}

// writer 按某种通讯工具的格式拼一条消息，并记下按编号 @ 到的人。
type writer struct {
	f        Formatter
	provider string
	lang     i18n.Lang
	mentions []store.ChatIdentity
}

func newWriter(f Formatter, provider string, lang i18n.Lang) *writer {
	return &writer{f: f, provider: provider, lang: lang}
}

// parens 给 label 加括注：中文用全角括号紧贴，英文用空格加半角括号。
func parens(l i18n.Lang, label string) string { return l.T("notify.parens", i18n.D{"Text": label}) }

func (w *writer) esc(s string) string { return w.f.Esc(s) }

// mention 提到一个人；label 为已转义的名字。
func (w *writer) mention(p Person, label string, withLabel bool) string {
	id := p.identity(w.provider)
	if id != nil && id.UserID != "" {
		dup := false
		for _, m := range w.mentions {
			if m.UserID == id.UserID && m.Tenant == id.Tenant {
				dup = true
			}
		}
		if !dup {
			w.mentions = append(w.mentions, *id)
		}
	}
	return w.f.Mention(w.lang, label, id, withLabel)
}

// nameLabel 是审批消息里展示的登录名与显示名（已转义）。
func (w *writer) nameLabel(name, display string) string {
	s := w.esc(name)
	if display != "" && display != name {
		s += parens(w.lang, w.esc(display))
	}
	return s
}

func (w *writer) done(title string, lines ...string) Rendered {
	return Rendered{Title: title, Text: strings.Join(lines, "\n"), Mentions: w.mentions, Lang: w.lang}
}
