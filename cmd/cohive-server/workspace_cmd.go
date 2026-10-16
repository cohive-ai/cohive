package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func workspaceCmd(ctx context.Context, out io.Writer, pool *pgxpool.Pool, sub string, args []string) error {
	switch sub {
	case "add":
		var name string
		if err := parse("workspace add", args, func(fs *flag.FlagSet) {
			fs.StringVar(&name, "name", "", "工作空间名，通常对应一个业务组")
		}); err != nil {
			return err
		}
		if err := required("name", name); err != nil {
			return err
		}
		w, err := store.CreateWorkspace(ctx, pool, name)
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return fmt.Errorf("工作空间 %q 已存在", name)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "已创建工作空间 %s（编号 %d）\n", w.Name, w.ID)
		return nil
	case "list":
		if _, err := store.DefaultWorkspace(ctx, pool); err != nil {
			return err
		}
		list, err := store.ListWorkspaces(ctx, pool)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "编号\t名称\t群通知\t加入方式\t待发/失败\t看板地址")
		for _, w := range list {
			pending, failed, err := store.CountNotifications(ctx, pool, w.ID)
			if err != nil {
				return err
			}
			notify := "未配置"
			if w.Notify.Channel != "" {
				notify = fmt.Sprintf("%s %s（标题 %s，简述 %s）", w.Notify.Channel, w.Notify.Target,
					onOff(w.Notify.ShowTitle), onOff(w.Notify.ShowBrief))
			}
			join := "加入需审批"
			if !w.JoinRequiresApproval {
				join = "自助加入"
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d/%d\t%s\n", w.ID, w.Name, notify, join, pending, failed, w.PublicURL)
		}
		return tw.Flush()
	case "notify":
		return workspaceNotify(ctx, out, pool, args)
	case "set":
		var name string
		var approval bool
		set := false
		fs := flag.NewFlagSet("workspace set", flag.ContinueOnError)
		fs.StringVar(&name, "workspace", "default", "工作空间名")
		fs.BoolVar(&approval, "join-approval", true, "群里 /join 加入是否需要管理员批准")
		if err := fs.Parse(args); err != nil {
			return err
		}
		fs.Visit(func(f *flag.Flag) { set = set || f.Name == "join-approval" })
		if !set {
			return fmt.Errorf("没有要修改的项：例如 --join-approval=false")
		}
		wsID, err := resolveWorkspace(ctx, pool, name)
		if err != nil {
			return err
		}
		if err := store.SetJoinApproval(ctx, pool, wsID, approval); err != nil {
			return err
		}
		if approval {
			fmt.Fprintf(out, "工作空间 %s：群成员发 /join 后需管理员批准\n", name)
		} else {
			fmt.Fprintf(out, "工作空间 %s：群成员发 /join 直接加入\n", name)
		}
		return nil
	}
	fmt.Fprint(out, usage)
	return fmt.Errorf("未知命令 %q", "workspace "+sub)
}

func onOff(b bool) string {
	if b {
		return "开"
	}
	return "关"
}

func workspaceNotify(ctx context.Context, out io.Writer, pool *pgxpool.Pool, args []string) error {
	var name, chat, publicURL string
	var showTitle, showBrief, off bool
	if err := parse("workspace notify", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "workspace", "default", "工作空间名")
		fs.StringVar(&chat, "telegram-chat", "", "Telegram 群聊编号（群里发 /chatid 获取）")
		fs.BoolVar(&showTitle, "show-title", true, "群消息里显示标题")
		fs.BoolVar(&showBrief, "show-brief", true, "群消息里显示群通知简述")
		fs.StringVar(&publicURL, "public-url", "", "看板的外部地址，用于群消息里的链接")
		fs.BoolVar(&off, "off", false, "关闭群通知")
	}); err != nil {
		return err
	}
	wsID, err := resolveWorkspace(ctx, pool, name)
	if err != nil {
		return err
	}
	cfg := store.NotifyConfig{ShowTitle: showTitle, ShowBrief: showBrief}
	if !off {
		if chat == "" {
			return fmt.Errorf("缺少参数 --telegram-chat；关闭群通知请用 --off")
		}
		if _, err := fmt.Sscan(chat, new(int64)); err != nil {
			return fmt.Errorf("Telegram 群聊编号应为数字，例如 -1001234567890")
		}
		cfg.Channel, cfg.Target = "telegram", chat
	}
	publicURL = strings.TrimRight(publicURL, "/")
	if publicURL != "" {
		if u, err := url.Parse(publicURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("--public-url 应为 http 或 https 地址")
		}
	}
	if err := store.SetWorkspaceNotify(ctx, pool, wsID, cfg, publicURL); err != nil {
		return err
	}
	if off {
		fmt.Fprintf(out, "已关闭工作空间 %s 的群通知\n", name)
	} else {
		fmt.Fprintf(out, "工作空间 %s 的群通知发往 Telegram 群 %s\n", name, chat)
	}
	return nil
}

var telegramUsername = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)

func memberSet(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var name, tg, tgID string
	set := map[string]bool{}
	fs := flag.NewFlagSet("member set", flag.ContinueOnError)
	fs.StringVar(&name, "name", "", "成员名")
	fs.StringVar(&tg, "telegram", "", "Telegram 用户名，群通知里用于 @ 提醒；传空字符串可清除")
	fs.StringVar(&tgID, "telegram-id", "", "Telegram 用户编号（私聊机器人 /whoami 获取），用于领取登录码；传空字符串可解绑")
	var role string
	fs.StringVar(&role, "role", "", "角色：admin（可审批加入申请）或 member")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if err := required("name", name); err != nil {
		return err
	}
	if !set["telegram"] && !set["telegram-id"] && !set["role"] {
		return fmt.Errorf("没有要修改的项：用 --telegram 设置用户名、--telegram-id 绑定用户编号，或 --role 设置角色")
	}
	if set["role"] && role != store.RoleAdmin && role != store.RoleMember {
		return fmt.Errorf("角色只能是 admin 或 member，收到 %q", role)
	}
	tg = strings.TrimPrefix(strings.TrimSpace(tg), "@")
	if tg != "" && !telegramUsername.MatchString(tg) {
		return fmt.Errorf("Telegram 用户名 %q 不合法：5 到 32 位，字母开头，只含字母、数字和下划线", tg)
	}
	var userID *int64
	if tgID = strings.TrimSpace(tgID); tgID != "" {
		v, err := strconv.ParseInt(tgID, 10, 64)
		if err != nil || v <= 0 {
			return fmt.Errorf("Telegram 用户编号应为正整数，私聊机器人发 /whoami 可以查到")
		}
		userID = &v
	}
	m, err := store.MemberByName(ctx, pool, ws, name)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("成员 %q 不存在", name)
	}
	if err != nil {
		return err
	}
	if set["telegram"] {
		if err := store.SetMemberTelegram(ctx, pool, m.ID, tg); err != nil {
			return err
		}
		if tg == "" {
			fmt.Fprintf(out, "已清除 %s 的 Telegram 用户名\n", name)
		} else {
			fmt.Fprintf(out, "%s 的 Telegram 用户名为 %s\n", name, tg)
		}
	}
	if set["role"] {
		if err := store.SetMemberRole(ctx, pool, m.ID, role); err != nil {
			return err
		}
		fmt.Fprintf(out, "%s 的角色为 %s\n", name, roleLabel(role))
	}
	if set["telegram-id"] {
		if userID != nil {
			list, err := store.ListMembers(ctx, pool, ws)
			if err != nil {
				return err
			}
			for _, other := range list {
				if other.ID != m.ID && other.TelegramUserID != nil && *other.TelegramUserID == *userID {
					return fmt.Errorf("这个 Telegram 账号已经绑定给本工作空间的成员 %s。一个人在一个工作空间里只需要一个成员身份，不同编程工具会各自拿到令牌", other.Name)
				}
			}
		}
		if err := store.SetMemberTelegramUserID(ctx, pool, m.ID, userID); err != nil {
			return err
		}
		if userID == nil {
			fmt.Fprintf(out, "已解绑 %s 的 Telegram 用户编号\n", name)
		} else {
			fmt.Fprintf(out, "%s 已绑定 Telegram 用户编号 %d，可以私聊机器人发 /login 领取登录码\n", name, *userID)
		}
	}
	return nil
}

func roleLabel(role string) string {
	if role == store.RoleAdmin {
		return "管理员"
	}
	return "成员"
}
