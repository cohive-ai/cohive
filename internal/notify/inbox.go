package notify

import (
	"context"
	"errors"
	"log"
	"strings"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// 机器人收件的统一处理：各通讯工具把群里 @机器人、私聊发来的文字翻译成 Inbound 交给 Inbox.Handle；
// Inbox 认指令、调业务层（私聊发码连接账号、群里发码绑群、我是谁、群编号），返回与格式无关的 Reply；
// 通道再按自己的文案与格式把 Reply 写成文字发回去。审批按钮另走 Hub.Click。

// CodeCore 是收件处理用到的绑定码操作，由 core.Service 实现。
type CodeCore interface {
	SubmitLinkCode(ctx context.Context, provider, code string, who core.ChatSender, private bool) (core.LinkSubmit, error)
	CompleteChatBind(ctx context.Context, provider, chatID, title, code string) (string, error)
}

// InboxCaps 是一种通讯工具在收件上用的做法。
type InboxCaps struct {
	LinkByCode bool // 账号连接：私聊把网页上的码发给机器人（Telegram 用带参数的机器人链接，为假）
	BindByCode bool // 群绑定：群里 @机器人 发网页上的群绑定码
	Help       bool // 认不出的消息回复用法（Telegram 只回应认得的命令，为假）
}

// Inbound 是机器人收到的一条文字消息。除指令外的字段都由通讯工具平台填写，不来自消息内容。
type Inbound struct {
	Provider  string
	ChatID    string // 会话编号：群里是群编号；私聊时不用于绑定与角色
	ChatTitle string // 群名
	Private   bool   // 私聊
	From      ChatUser
	// Cmd 是指令词（已去掉开头的 @机器人 与 /，大小写由通道决定），First 是原样的第一个词，Args 是其余的词。
	Cmd, First string
	Args       []string
}

// ReplyKind 是机器人回复的种类。
type ReplyKind int

const (
	ReplyNone         ReplyKind = iota // 不回复
	ReplyHelp                          // 用法说明（Private 区分私聊与群里）
	ReplyChatID                        // 本群编号；Private 为真时是在私聊里问的
	ReplyWhoami                        // 我是谁：Checked、Linked、Account、Workspace、Role
	ReplyLinkReceived                  // 收到账号绑定码，等本人在网页确认：Account
	ReplyBound                         // 已把本群绑定到工作空间：Workspace
	ReplyError                         // 给人看的业务错误：Err（未转义）
	ReplyTryLater                      // 内部错误，已记日志
)

// Reply 是收件处理的结果，由通道按自己的文案与格式写成文字。
type Reply struct {
	Kind    ReplyKind
	Lang    i18n.Lang // 回复的语言：群里是群所属工作空间的语言，私聊是对方连接的账号的语言，查不到时是服务器缺省语言
	Private bool
	// Checked 为真表示查过对方连接的账号（有业务层）；Linked 表示已连接，Account 是 Cohive 账号名；
	// Workspace、Role 是他在本群对应工作空间里的显示名与角色（私聊、群没绑工作空间时为空）。
	Checked, Linked          bool
	Account, Workspace, Role string
	Err                      string
}

// Inbox 是与通讯工具无关的收件处理。各通道每收到一条消息建一个即可。
type Inbox struct {
	Hub   *Hub     // 为空时不查账号与工作空间（我是谁不带账号，回复用 Lang 或服务器缺省语言）
	Codes CodeCore // 为空时不处理绑定码
	Caps  InboxCaps
	Lang  i18n.Lang // 不为空时固定用这个语言回复（没有业务层时用）
	Logf  func(format string, args ...any)
}

func (x Inbox) logf(format string, args ...any) {
	if x.Logf != nil {
		x.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// parseText 把自由文字拆成指令：去掉开头的 @机器人名 与空白，返回指令词（小写、去掉开头的 /）、原样的第一个词与其余的词。
// 用于群里 @机器人 加文字的通讯工具（钉钉）；Telegram 的 / 命令由它自己解析。
func parseText(text string) (cmd, first string, args []string) {
	fields := strings.Fields(strings.ReplaceAll(text, "\u3000", " "))
	for len(fields) > 0 && strings.HasPrefix(fields[0], "@") {
		fields = fields[1:]
	}
	if len(fields) == 0 {
		return "", "", nil
	}
	return strings.ToLower(strings.TrimPrefix(fields[0], "/")), fields[0], fields[1:]
}

// 指令词：中文指令与英文指令都认（帮助说明按回复语言写其中一种）。
func isLinkCmd(c string) bool   { return c == "绑定" || c == "bind" }       // i18n-ignore 解析用户输入，中文指令继续认
func isBindCmd(c string) bool   { return c == "绑定群" || c == "bindgroup" } // i18n-ignore 解析用户输入，中文指令继续认
func isChatIDCmd(c string) bool { return c == "群编号" || c == "chatid" }    // i18n-ignore 解析用户输入，中文指令继续认
func isWhoamiCmd(c string) bool { return c == "我是谁" || c == "whoami" }    // i18n-ignore 解析用户输入，中文指令继续认

// lang 是回复这条消息用的语言：群里用群所属工作空间的语言，私聊用对方连接的 Cohive 账号的语言；
// 群还没绑定、对方还没连接账号、没有业务层时用服务器缺省语言。
func (x Inbox) lang(ctx context.Context, in Inbound) i18n.Lang {
	switch {
	case x.Lang != "":
		return x.Lang
	case x.Hub == nil:
		return i18n.Default()
	case in.Private && in.From.UserID == "":
		return i18n.Default()
	case in.Private:
		return x.Hub.accountLang(ctx, in.Provider, in.From.Tenant, in.From.UserID)
	}
	return x.Hub.chatLang(ctx, in.Provider, in.ChatID)
}

// Handle 处理一条消息，返回要回复的内容。
func (x Inbox) Handle(ctx context.Context, in Inbound) Reply {
	r := Reply{Lang: x.lang(ctx, in), Private: in.Private}
	code := "" // 消息里的码：账号绑定码或群绑定码，下面按能力开关决定怎么用
	switch {
	case isLinkCmd(in.Cmd) && len(in.Args) == 1:
		code = in.Args[0]
	case len(in.Args) == 0 && core.LooksLikeCode(in.First):
		code = in.First
	}
	who := core.ChatSender{Tenant: in.From.Tenant, UserID: in.From.UserID, Name: in.From.Name}

	if in.Private {
		switch {
		case code != "" && x.Caps.LinkByCode && x.Codes != nil:
			res, err := x.Codes.SubmitLinkCode(ctx, in.Provider, code, who, true)
			if err != nil {
				return x.fail(r, in, err, "link account")
			}
			r.Kind, r.Account = ReplyLinkReceived, res.Account
			return r
		case isWhoamiCmd(in.Cmd):
			return x.whoami(ctx, r, in, "")
		case isChatIDCmd(in.Cmd):
			r.Kind = ReplyChatID
			return r
		}
		return x.help(r)
	}

	switch {
	case isChatIDCmd(in.Cmd):
		r.Kind = ReplyChatID
		return r
	case isWhoamiCmd(in.Cmd):
		return x.whoami(ctx, r, in, in.ChatID)
	case isBindCmd(in.Cmd) && len(in.Args) == 1 && x.Caps.BindByCode && x.Codes != nil:
		label, err := x.Codes.CompleteChatBind(ctx, in.Provider, in.ChatID, in.ChatTitle, in.Args[0])
		if err != nil {
			return x.fail(r, in, err, "bind group")
		}
		return x.bound(ctx, r, in, label)
	case code != "" && x.Codes != nil:
		// 账号绑定码出现在群里：作废（回复里不复述码）；不是账号绑定码时，可能是群绑定码少写了“群”
		if x.Caps.LinkByCode {
			_, err := x.Codes.SubmitLinkCode(ctx, in.Provider, code, who, false)
			if errors.Is(err, core.ErrLinkCodeLeaked) {
				r.Kind, r.Err = ReplyError, i18n.Message(r.Lang, core.ErrLinkCodeLeaked)
				return r
			}
		}
		if x.Caps.BindByCode {
			if label, err := x.Codes.CompleteChatBind(ctx, in.Provider, in.ChatID, in.ChatTitle, code); err == nil {
				return x.bound(ctx, r, in, label)
			}
		}
	}
	return x.help(r)
}

func (x Inbox) help(r Reply) Reply {
	if x.Caps.Help {
		r.Kind = ReplyHelp
	}
	return r
}

// bound 是绑群成功：之后这个群的语言就是工作空间的语言。
func (x Inbox) bound(ctx context.Context, r Reply, in Inbound, label string) Reply {
	r.Kind, r.Workspace, r.Lang = ReplyBound, label, x.lang(ctx, in)
	return r
}

func (x Inbox) whoami(ctx context.Context, r Reply, in Inbound, chat string) Reply {
	r.Kind = ReplyWhoami
	if x.Hub == nil {
		return r
	}
	r.Checked = true
	r.Account, r.Workspace, r.Role, r.Linked = x.Hub.Whoami(ctx, r.Lang, in.From, chat)
	return r
}

// fail 把业务层的错误写成给人看的回复；内部错误只记日志。
func (x Inbox) fail(r Reply, in Inbound, err error, what string) Reply {
	var ie *core.InputError
	var se *core.StateError
	switch {
	case errors.Is(err, core.ErrLinkCodeGone), errors.Is(err, core.ErrLinkCodeLeaked), errors.Is(err, core.ErrBindCodeGone),
		errors.Is(err, store.ErrAccountDisabled), errors.As(err, &ie), errors.As(err, &se):
		r.Kind, r.Err = ReplyError, i18n.Message(r.Lang, err)
		return r
	}
	x.logf("%s bot failed to handle %q: %v", in.Provider, what, err)
	r.Kind = ReplyTryLater
	return r
}
