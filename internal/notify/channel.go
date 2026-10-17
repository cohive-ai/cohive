package notify

import (
	"context"
	"strings"

	"github.com/cohive-ai/cohive/internal/store"
)

// Channel 是一种通讯工具（Telegram、钉钉）。服务器为每种启用的通讯工具建一个实例，可以同时启用多种；
// 工作空间绑定哪种通讯工具的群，通知和审批消息就经哪个实例发出。收件循环（长轮询、长连接）由各实现自己跑，
// 收到按钮点击时交给 Hub.Click。
type Channel interface {
	Provider() string  // store.ProviderTelegram、store.ProviderDingTalk
	Format() Formatter // 本通讯工具的文本格式与 @ 写法
	// Send 往群里发一条通知（问题单通知、加入通知）。
	Send(ctx context.Context, chat string, m Rendered) error
	// SendApproval 发一条带“批准 / 拒绝”按钮的审批消息，返回消息编号（之后据此改成结果）。
	SendApproval(ctx context.Context, chat string, m Rendered, a Approval) (msgRef string, err error)
	// FinishApproval 把审批消息改成结果、去掉按钮。
	FinishApproval(ctx context.Context, chat, msgRef string, m Rendered) error
}

// Formatter 是一种通讯工具的消息格式。
type Formatter interface {
	Esc(s string) string    // 转义纯文本
	Code(s string) string   // 等宽显示（参数为未转义的纯文本）
	Link(url string) string // 消息末尾的问题单链接
	// Mention 是提到一个人。label 为已转义的名字；id 为这个人在本通讯工具上的连接，没有时为空。
	// withLabel 为真时，按用户名提及后再括注 label（审批消息里用）。
	Mention(label string, id *store.ChatIdentity, withLabel bool) string
}

// Rendered 是渲染好的一条消息。
type Rendered struct {
	Title    string               // 钉钉卡片标题与会话列表摘要；Telegram 不用
	Text     string               // 按本通讯工具格式写好的正文
	Mentions []store.ChatIdentity // 正文里按编号 @ 到的人（钉钉要单独传）
	// Key 是幂等键：同一条通知重发时不变（钉钉用作卡片编号，重发不会出现第二张卡）；为空时由通道自己生成
	Key string
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
	mentions []store.ChatIdentity
}

func newWriter(f Formatter, provider string) *writer { return &writer{f: f, provider: provider} }

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
	return w.f.Mention(label, id, withLabel)
}

// nameLabel 是审批消息里展示的登录名与显示名（已转义）。
func (w *writer) nameLabel(name, display string) string {
	s := w.esc(name)
	if display != "" && display != name {
		s += "（" + w.esc(display) + "）"
	}
	return s
}

func (w *writer) done(title string, lines ...string) Rendered {
	return Rendered{Title: title, Text: strings.Join(lines, "\n"), Mentions: w.mentions}
}
