package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func workspaceCmd(ctx context.Context, out io.Writer, pool *pgxpool.Pool, sub string, args []string) error {
	switch sub {
	case "add":
		var name, display, adminName string
		if err := parse("workspace add", args, func(fs *flag.FlagSet) {
			fs.StringVar(&name, "name", "", "工作空间路径，出现在链接与项目配置里，通常对应一个项目（一个群）；建后不能改")
			fs.StringVar(&display, "display", "", "显示名（可选，可随时改；缺省与路径相同）")
			fs.StringVar(&adminName, "admin", "", "首位管理员的登录名（可选）")
		}); err != nil {
			return err
		}
		if err := required("name", name); err != nil {
			return err
		}
		w, err := (&core.Service{Pool: pool}).CreateWorkspaceNamed(ctx, core.ServerCLI(), name, display, adminName)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "已创建工作空间 %s（显示名 %s，编号 %d）\n", w.Name, w.Label(), w.ID)
		return nil
	case "set":
		var name, display string
		var openH, verifyH int
		set := map[string]bool{}
		fs := flag.NewFlagSet("workspace set", flag.ContinueOnError)
		fs.StringVar(&name, "name", "", "工作空间路径")
		fs.StringVar(&display, "display", "", "显示名；传空字符串恢复为与路径相同")
		fs.IntVar(&openH, "remind-open-hours", 0, "待接单超时催办时长（小时，0 表示关闭）")
		fs.IntVar(&verifyH, "remind-verify-hours", 0, "待验证超时催办时长（小时，0 表示关闭）")
		if err := fs.Parse(args); err != nil {
			return err
		}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if err := required("name", name); err != nil {
			return err
		}
		if !set["display"] && !set["remind-open-hours"] && !set["remind-verify-hours"] {
			return fmt.Errorf("没有要修改的项：用 --display（路径不能改）、--remind-open-hours 或 --remind-verify-hours")
		}
		wsID, err := resolveWorkspace(ctx, pool, name)
		if err != nil {
			return err
		}
		svc := &core.Service{Pool: pool}
		if set["display"] {
			if err := svc.SetWorkspaceDisplayName(ctx, core.ServerCLI(), wsID, display); err != nil {
				return err
			}
		}
		if set["remind-open-hours"] || set["remind-verify-hours"] {
			in := core.WorkspaceSettings{}
			if set["remind-open-hours"] {
				in.RemindOpenHours = &openH
			}
			if set["remind-verify-hours"] {
				in.RemindVerifyHours = &verifyH
			}
			if err := svc.SetRemindHours(ctx, core.ServerCLI(), wsID, in.RemindOpenHours, in.RemindVerifyHours); err != nil {
				return err
			}
		}
		w, err := store.GetWorkspace(ctx, pool, wsID)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "工作空间 %s：显示名 %s，超时催办 待接单 %s、待验证 %s\n", w.Name, w.Label(),
			hoursLabel(w.Remind.OpenHours), hoursLabel(w.Remind.VerifyHours))
		return nil
	case "remove":
		var name string
		if err := parse("workspace remove", args, func(fs *flag.FlagSet) {
			fs.StringVar(&name, "name", "", "工作空间名")
		}); err != nil {
			return err
		}
		if err := required("name", name); err != nil {
			return err
		}
		if err := (&core.Service{Pool: pool}).DeleteWorkspace(ctx, core.ServerCLI(), name); errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("工作空间 %q 不存在", name)
		} else if err != nil {
			return err
		}
		fmt.Fprintf(out, "已删除工作空间 %s\n", name)
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
		fmt.Fprintln(tw, "编号\t路径\t显示名\t群通知\t待发/失败")
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
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d/%d\n", w.ID, w.Name, w.Label(), notify, pending, failed)
		}
		return tw.Flush()
	case "notify":
		return workspaceNotify(ctx, out, pool, args)
	}
	fmt.Fprint(out, usage)
	return fmt.Errorf("未知命令 %q", "workspace "+sub)
}

func hoursLabel(h int) string {
	if h == 0 {
		return "关闭"
	}
	return fmt.Sprintf("%d 小时", h)
}

func onOff(b bool) string {
	if b {
		return "开"
	}
	return "关"
}

func workspaceNotify(ctx context.Context, out io.Writer, pool *pgxpool.Pool, args []string) error {
	var name, chat, dtChat, publicURL string
	var showTitle, showBrief, off bool
	if err := parse("workspace notify", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "workspace", "default", "工作空间名")
		fs.StringVar(&chat, "telegram-chat", "", "Telegram 群聊编号（群里发 /chatid 获取）")
		fs.StringVar(&dtChat, "dingtalk-chat", "", "钉钉群编号（群里 @机器人 发“群编号”获取；也可以在网页上用群绑定码绑定）")
		fs.BoolVar(&showTitle, "show-title", true, "群消息里显示标题")
		fs.BoolVar(&showBrief, "show-brief", true, "群消息里显示群通知简述")
		fs.StringVar(&publicURL, "public-url", "", "已废弃：对外地址改为服务器级，用 server set --external-url")
		fs.BoolVar(&off, "off", false, "关闭群通知")
	}); err != nil {
		return err
	}
	wsID, err := resolveWorkspace(ctx, pool, name)
	if err != nil {
		return err
	}
	svc := &core.Service{Pool: pool}
	if dtChat != "" {
		if chat != "" || off {
			return fmt.Errorf("--dingtalk-chat 不能与 --telegram-chat、--off 同时使用")
		}
		if err := svc.BindChat(ctx, core.ServerCLI(), wsID, core.ProviderDingTalk, dtChat); err != nil {
			return err
		}
		fmt.Fprintf(out, "工作空间 %s 的群通知发往钉钉群 %s\n", name, dtChat)
		return nil
	}
	if !off {
		if chat == "" {
			return fmt.Errorf("缺少参数 --telegram-chat 或 --dingtalk-chat；关闭群通知请用 --off")
		}
		if _, err := fmt.Sscan(chat, new(int64)); err != nil {
			return fmt.Errorf("Telegram 群聊编号应为数字，例如 -1001234567890")
		}
	} else {
		chat = ""
	}
	if publicURL != "" {
		return fmt.Errorf("--public-url 已废弃：对外地址改为服务器级，只设一个，请用 cohive-server server set --external-url %s", publicURL)
	}
	// 经业务层写入：一个群只能绑一个工作空间
	if off { // 关闭群通知：钉钉群也一并解绑
		if err := svc.UnbindChat(ctx, core.ServerCLI(), wsID, core.ProviderDingTalk); err != nil {
			return err
		}
	}
	if err := svc.UpdateWorkspace(ctx, core.ServerCLI(), wsID, core.WorkspaceSettings{
		TelegramChat: chat, ShowTitle: showTitle, ShowBrief: showBrief}); err != nil {
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
	var name, role string
	set := map[string]bool{}
	fs := flag.NewFlagSet("member set", flag.ContinueOnError)
	fs.StringVar(&name, "name", "", "登录名")
	fs.StringVar(&role, "role", "", "角色：admin（可发邀请、审批注册与设备）或 member")
	var tgOld, tgIDOld string
	fs.StringVar(&tgOld, "telegram", "", "已移到 account set")
	fs.StringVar(&tgIDOld, "telegram-id", "", "已移到 account set")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["telegram"] || set["telegram-id"] {
		return fmt.Errorf("Telegram 连接现在是账号级的，全部工作空间通用：请用 account set --name %s --telegram …", name)
	}
	if err := required("name", name); err != nil {
		return err
	}
	if !set["role"] {
		return fmt.Errorf("没有要修改的项：用 --role 设置角色")
	}
	m, err := store.MemberByName(ctx, pool, ws, name)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("成员 %q 不存在", name)
	}
	if err != nil {
		return err
	}
	if err := (&core.Service{Pool: pool}).SetRole(ctx, core.ServerCLI(), ws, m.ID, role); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s 的角色为 %s\n", name, roleLabel(role))
	return nil
}

func accountCmd(ctx context.Context, out io.Writer, pool *pgxpool.Pool, sub string, args []string) error {
	svc := &core.Service{Pool: pool}
	named := func(cmd string, extra func(fs *flag.FlagSet)) (store.Account, error) {
		var name string
		if err := parse(cmd, args, func(fs *flag.FlagSet) {
			fs.StringVar(&name, "name", "", "登录名")
			if extra != nil {
				extra(fs)
			}
		}); err != nil {
			return store.Account{}, err
		}
		if err := required("name", name); err != nil {
			return store.Account{}, err
		}
		a, err := store.AccountByName(ctx, pool, name)
		if errors.Is(err, store.ErrNotFound) {
			return a, fmt.Errorf("没有登录名为 %s 的账号", name)
		}
		return a, err
	}
	switch sub {
	case "list":
		var prefix string
		if err := parse("account list", args, func(fs *flag.FlagSet) {
			fs.StringVar(&prefix, "prefix", "", "登录名前缀")
		}); err != nil {
			return err
		}
		list, err := store.ListAccountSummaries(ctx, pool, prefix)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "登录名\t显示名\t服务器管理员\t状态\tTelegram\t设备\t工作空间")
		for _, a := range list {
			state := "正常"
			if a.Disabled() {
				state = "已停用"
			}
			tg := a.TelegramUsername
			if a.TelegramUserID != nil && tg == "" {
				tg = "已连接"
			}
			var wss []string
			for _, m := range a.Memberships {
				if m.Status == store.MemberActive {
					wss = append(wss, m.Workspace+"（"+roleLabel(m.Role)+"）")
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", a.Name, a.DisplayName, yesNo(a.ServerAdmin), state, tg, a.Devices,
				strings.Join(wss, "、"))
		}
		return tw.Flush()
	case "set":
		var display, tg, tgID string
		set := map[string]bool{}
		fs := flag.NewFlagSet("account set", flag.ContinueOnError)
		var name string
		fs.StringVar(&name, "name", "", "登录名")
		fs.StringVar(&display, "display", "", "显示名")
		fs.StringVar(&tg, "telegram", "", "Telegram 用户名，群通知里用于 @ 提醒；传空字符串可清除")
		fs.StringVar(&tgID, "telegram-id", "", "Telegram 用户编号（私聊机器人 /whoami 获取），用于识别群里点审批按钮的人；传空字符串可解绑。一般由本人在网页个人中心自行连接")
		if err := fs.Parse(args); err != nil {
			return err
		}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if err := required("name", name); err != nil {
			return err
		}
		if !set["display"] && !set["telegram"] && !set["telegram-id"] {
			return fmt.Errorf("没有要修改的项：用 --display、--telegram 或 --telegram-id")
		}
		var in core.AccountUpdate
		var msgs []string
		if set["display"] {
			d := strings.TrimSpace(display)
			in.DisplayName = &d
			msgs = append(msgs, fmt.Sprintf("%s 的显示名为 %s", name, d))
		}
		if set["telegram"] {
			tg = strings.TrimPrefix(strings.TrimSpace(tg), "@")
			if tg != "" && !telegramUsername.MatchString(tg) {
				return fmt.Errorf("Telegram 用户名 %q 不合法：5 到 32 位，字母开头，只含字母、数字和下划线", tg)
			}
			in.TelegramUsername = &tg
			if tg == "" {
				msgs = append(msgs, fmt.Sprintf("已清除 %s 的 Telegram 用户名", name))
			} else {
				msgs = append(msgs, fmt.Sprintf("%s 的 Telegram 用户名为 %s", name, tg))
			}
		}
		if set["telegram-id"] {
			if tgID = strings.TrimSpace(tgID); tgID != "" {
				v, err := strconv.ParseInt(tgID, 10, 64)
				if err != nil || v <= 0 {
					return fmt.Errorf("Telegram 用户编号应为正整数，私聊机器人发 /whoami 可以查到")
				}
				in.TelegramUserID = &v
				msgs = append(msgs, fmt.Sprintf("%s 已绑定 Telegram 用户编号 %d", name, v))
			} else {
				in.ClearTelegramID = true
				msgs = append(msgs, fmt.Sprintf("已解绑 %s 的 Telegram 用户编号", name))
			}
		}
		if err := svc.UpdateAccount(ctx, core.ServerCLI(), name, in); errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("没有登录名为 %s 的账号", name)
		} else if err != nil {
			return err
		}
		for _, m := range msgs {
			fmt.Fprintln(out, m)
		}
		return nil
	case "disable":
		a, err := named("account disable", nil)
		if err != nil {
			return err
		}
		if err := svc.DisableAccount(ctx, core.ServerCLI(), a.ID); err != nil {
			return err
		}
		fmt.Fprintf(out, "已停用 %s：全部设备与令牌已吊销，网页登录已注销，Telegram 已断开\n", a.Name)
		return nil
	case "enable":
		a, err := named("account enable", nil)
		if err != nil {
			return err
		}
		if err := svc.EnableAccount(ctx, core.ServerCLI(), a.ID); err != nil {
			return err
		}
		fmt.Fprintf(out, "已启用 %s。设备不会恢复：他需要重新执行 cohive login，并由管理员批准\n", a.Name)
		return nil
	case "server-admin":
		var on, off bool
		a, err := named("account server-admin", func(fs *flag.FlagSet) {
			fs.BoolVar(&on, "on", false, "授予")
			fs.BoolVar(&off, "off", false, "撤销")
		})
		if err != nil {
			return err
		}
		if on == off {
			return fmt.Errorf("请用 --on 授予或 --off 撤销")
		}
		if err := svc.SetServerAdmin(ctx, core.ServerCLI(), a.ID, on); err != nil {
			return err
		}
		if on {
			fmt.Fprintf(out, "%s 已是服务器管理员\n", a.Name)
		} else {
			fmt.Fprintf(out, "已撤销 %s 的服务器管理员\n", a.Name)
		}
		return nil
	}
	fmt.Fprint(out, usage)
	return fmt.Errorf("未知命令 %q", "account "+sub)
}

func yesNo(b bool) string {
	if b {
		return "是"
	}
	return ""
}

func roleLabel(role string) string {
	if role == store.RoleAdmin {
		return "管理员"
	}
	return "成员"
}
