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
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func workspaceCmd(ctx context.Context, out io.Writer, pool *pgxpool.Pool, sub string, args []string) error {
	switch sub {
	case "add":
		var name, display, adminName string
		if err := parse("workspace add", args, func(fs *flag.FlagSet) {
			fs.StringVar(&name, "name", "", i18n.T("server.flag.ws_path_add"))
			fs.StringVar(&display, "display", "", i18n.T("server.flag.ws_display_add"))
			fs.StringVar(&adminName, "admin", "", i18n.T("server.flag.ws_first_admin"))
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
		fmt.Fprintln(out, i18n.T("server.workspace.created", i18n.D{"Name": w.Name, "Display": w.Label(), "ID": w.ID}))
		return nil
	case "set":
		var name, display, lang string
		var openH, verifyH int
		set := map[string]bool{}
		fs := flag.NewFlagSet("workspace set", flag.ContinueOnError)
		fs.StringVar(&lang, "language", "", i18n.T("server.flag.ws_language"))
		fs.StringVar(&name, "name", "", i18n.T("server.flag.ws_path"))
		fs.StringVar(&display, "display", "", i18n.T("server.flag.ws_display_set"))
		fs.IntVar(&openH, "remind-open-hours", 0, i18n.T("server.flag.remind_open_hours"))
		fs.IntVar(&verifyH, "remind-verify-hours", 0, i18n.T("server.flag.remind_verify_hours"))
		if err := fs.Parse(args); err != nil {
			return err
		}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if err := required("name", name); err != nil {
			return err
		}
		if !set["display"] && !set["remind-open-hours"] && !set["remind-verify-hours"] && !set["language"] {
			return i18n.Errorf("server.workspace.nothing_to_change")
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
		if set["language"] {
			if err := svc.SetWorkspaceLanguage(ctx, core.ServerCLI(), wsID, lang); err != nil {
				return err
			}
		}
		w, err := store.GetWorkspace(ctx, pool, wsID)
		if err != nil {
			return err
		}
		if set["language"] {
			shown := w.Language
			if shown == "" {
				shown = i18n.T("server.workspace.language_default", i18n.D{"Default": string(i18n.Default())})
			}
			fmt.Fprintln(out, i18n.T("server.workspace.language_set", i18n.D{"Name": w.Name, "Language": shown}))
		}
		fmt.Fprintln(out, i18n.T("server.workspace.settings_set", i18n.D{"Name": w.Name, "Display": w.Label(),
			"Open": hoursLabel(w.Remind.OpenHours), "Verify": hoursLabel(w.Remind.VerifyHours)}))
		return nil
	case "remove":
		var name string
		if err := parse("workspace remove", args, func(fs *flag.FlagSet) {
			fs.StringVar(&name, "name", "", i18n.T("server.flag.ws_name"))
		}); err != nil {
			return err
		}
		if err := required("name", name); err != nil {
			return err
		}
		if err := (&core.Service{Pool: pool}).DeleteWorkspace(ctx, core.ServerCLI(), name); errors.Is(err, store.ErrNotFound) {
			return i18n.Errorf("server.workspace.not_found", i18n.D{"Name": strconv.Quote(name)})
		} else if err != nil {
			return err
		}
		fmt.Fprintln(out, i18n.T("server.workspace.deleted", i18n.D{"Name": name}))
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
		fmt.Fprintln(tw, i18n.T("server.table.workspaces"))
		for _, w := range list {
			pending, failed, err := store.CountNotifications(ctx, pool, w.ID)
			if err != nil {
				return err
			}
			notify := i18n.T("server.workspace.notify_none")
			if w.Notify.Channel != "" {
				notify = i18n.T("server.workspace.notify_summary", i18n.D{"Channel": w.Notify.Channel, "Target": w.Notify.Target,
					"Title": onOff(w.Notify.ShowTitle), "Brief": onOff(w.Notify.ShowBrief)})
			}
			fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%d/%d\n", w.ID, w.Name, w.Label(), notify, pending, failed)
		}
		return tw.Flush()
	case "notify":
		return workspaceNotify(ctx, out, pool, args)
	}
	fmt.Fprint(out, usage())
	return i18n.Errorf("server.unknown_command", i18n.D{"Command": strconv.Quote("workspace " + sub)})
}

func hoursLabel(h int) string {
	if h == 0 {
		return i18n.T("server.hours_off")
	}
	return i18n.T("server.hours", i18n.D{"Count": h})
}

func onOff(b bool) string {
	if b {
		return i18n.T("server.on")
	}
	return i18n.T("server.off")
}

func workspaceNotify(ctx context.Context, out io.Writer, pool *pgxpool.Pool, args []string) error {
	var name, chat, dtChat, publicURL string
	var showTitle, showBrief, off bool
	if err := parse("workspace notify", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "workspace", "default", i18n.T("server.flag.ws_name"))
		fs.StringVar(&chat, "telegram-chat", "", i18n.T("server.flag.telegram_chat"))
		fs.StringVar(&dtChat, "dingtalk-chat", "", i18n.T("server.flag.dingtalk_chat"))
		fs.BoolVar(&showTitle, "show-title", true, i18n.T("server.flag.show_title"))
		fs.BoolVar(&showBrief, "show-brief", true, i18n.T("server.flag.show_brief"))
		fs.StringVar(&publicURL, "public-url", "", i18n.T("server.flag.public_url"))
		fs.BoolVar(&off, "off", false, i18n.T("server.flag.notify_off"))
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
			return i18n.Errorf("server.workspace.dingtalk_conflict")
		}
		if err := svc.BindChat(ctx, core.ServerCLI(), wsID, core.ProviderDingTalk, dtChat); err != nil {
			return err
		}
		fmt.Fprintln(out, i18n.T("server.workspace.notify_dingtalk", i18n.D{"Name": name, "Chat": dtChat}))
		return nil
	}
	if !off {
		if chat == "" {
			return i18n.Errorf("server.workspace.chat_missing")
		}
		if _, err := fmt.Sscan(chat, new(int64)); err != nil {
			return i18n.Errorf("server.workspace.telegram_chat_invalid")
		}
	} else {
		chat = ""
	}
	if publicURL != "" {
		return i18n.Errorf("server.workspace.public_url_removed", i18n.D{"URL": publicURL})
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
		fmt.Fprintln(out, i18n.T("server.workspace.notify_off", i18n.D{"Name": name}))
	} else {
		fmt.Fprintln(out, i18n.T("server.workspace.notify_telegram", i18n.D{"Name": name, "Chat": chat}))
	}
	return nil
}

var telegramUsername = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{3,31}$`)

func memberSet(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var name, role string
	set := map[string]bool{}
	fs := flag.NewFlagSet("member set", flag.ContinueOnError)
	fs.StringVar(&name, "name", "", i18n.T("server.flag.username"))
	fs.StringVar(&role, "role", "", i18n.T("server.flag.role_set"))
	var tgOld, tgIDOld string
	fs.StringVar(&tgOld, "telegram", "", i18n.T("server.flag.moved_to_account_set"))
	fs.StringVar(&tgIDOld, "telegram-id", "", i18n.T("server.flag.moved_to_account_set"))
	if err := fs.Parse(args); err != nil {
		return err
	}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	if set["telegram"] || set["telegram-id"] {
		return i18n.Errorf("server.member.telegram_moved", i18n.D{"Name": name})
	}
	if err := required("name", name); err != nil {
		return err
	}
	if !set["role"] {
		return i18n.Errorf("server.member.nothing_to_change")
	}
	m, err := store.MemberByName(ctx, pool, ws, name)
	if errors.Is(err, store.ErrNotFound) {
		return i18n.Errorf("server.member.not_found", i18n.D{"Name": strconv.Quote(name)})
	}
	if err != nil {
		return err
	}
	if err := (&core.Service{Pool: pool}).SetRole(ctx, core.ServerCLI(), ws, m.ID, role); err != nil {
		return err
	}
	fmt.Fprintln(out, i18n.T("server.member.role_set", i18n.D{"Name": name, "Role": roleLabel(role)}))
	return nil
}

func accountCmd(ctx context.Context, out io.Writer, pool *pgxpool.Pool, sub string, args []string) error {
	svc := &core.Service{Pool: pool}
	named := func(cmd string, extra func(fs *flag.FlagSet)) (store.Account, error) {
		var name string
		if err := parse(cmd, args, func(fs *flag.FlagSet) {
			fs.StringVar(&name, "name", "", i18n.T("server.flag.username"))
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
			return a, i18n.Errorf("server.account.not_found", i18n.D{"Name": name})
		}
		return a, err
	}
	switch sub {
	case "list":
		var prefix string
		if err := parse("account list", args, func(fs *flag.FlagSet) {
			fs.StringVar(&prefix, "prefix", "", i18n.T("server.flag.username_prefix"))
		}); err != nil {
			return err
		}
		list, err := store.ListAccountSummaries(ctx, pool, prefix)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, i18n.T("server.table.accounts"))
		for _, a := range list {
			state := i18n.T("server.state.normal")
			if a.Disabled() {
				state = i18n.T("server.state.disabled")
			}
			tg := a.TelegramUsername
			if a.TelegramUserID != nil && tg == "" {
				tg = i18n.T("server.account.telegram_connected")
			}
			var wss []string
			for _, m := range a.Memberships {
				if m.Status == store.MemberActive {
					wss = append(wss, i18n.T("server.account.workspace_role", i18n.D{"Workspace": m.Workspace, "Role": roleLabel(m.Role)}))
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%s\n", a.Name, a.DisplayName, yesNo(a.ServerAdmin), state, tg, a.Devices,
				strings.Join(wss, i18n.T("server.list_separator")))
		}
		return tw.Flush()
	case "set":
		var display, tg, tgID string
		set := map[string]bool{}
		fs := flag.NewFlagSet("account set", flag.ContinueOnError)
		var name string
		fs.StringVar(&name, "name", "", i18n.T("server.flag.username"))
		fs.StringVar(&display, "display", "", i18n.T("server.flag.display"))
		fs.StringVar(&tg, "telegram", "", i18n.T("server.flag.telegram_username"))
		fs.StringVar(&tgID, "telegram-id", "", i18n.T("server.flag.telegram_id"))
		if err := fs.Parse(args); err != nil {
			return err
		}
		fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
		if err := required("name", name); err != nil {
			return err
		}
		if !set["display"] && !set["telegram"] && !set["telegram-id"] {
			return i18n.Errorf("server.account.nothing_to_change")
		}
		var in core.AccountUpdate
		var msgs []string
		if set["display"] {
			d := strings.TrimSpace(display)
			in.DisplayName = &d
			msgs = append(msgs, i18n.T("server.account.display_set", i18n.D{"Name": name, "Display": d}))
		}
		if set["telegram"] {
			tg = strings.TrimPrefix(strings.TrimSpace(tg), "@")
			if tg != "" && !telegramUsername.MatchString(tg) {
				return i18n.Errorf("server.account.telegram_username_invalid", i18n.D{"Name": strconv.Quote(tg)})
			}
			in.TelegramUsername = &tg
			if tg == "" {
				msgs = append(msgs, i18n.T("server.account.telegram_username_cleared", i18n.D{"Name": name}))
			} else {
				msgs = append(msgs, i18n.T("server.account.telegram_username_set", i18n.D{"Name": name, "Username": tg}))
			}
		}
		if set["telegram-id"] {
			if tgID = strings.TrimSpace(tgID); tgID != "" {
				v, err := strconv.ParseInt(tgID, 10, 64)
				if err != nil || v <= 0 {
					return i18n.Errorf("server.account.telegram_id_invalid")
				}
				in.TelegramUserID = &v
				msgs = append(msgs, i18n.T("server.account.telegram_id_set", i18n.D{"Name": name, "ID": v}))
			} else {
				in.ClearTelegramID = true
				msgs = append(msgs, i18n.T("server.account.telegram_id_cleared", i18n.D{"Name": name}))
			}
		}
		if err := svc.UpdateAccount(ctx, core.ServerCLI(), name, in); errors.Is(err, store.ErrNotFound) {
			return i18n.Errorf("server.account.not_found", i18n.D{"Name": name})
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
		fmt.Fprintln(out, i18n.T("server.account.disabled", i18n.D{"Name": a.Name}))
		return nil
	case "enable":
		a, err := named("account enable", nil)
		if err != nil {
			return err
		}
		if err := svc.EnableAccount(ctx, core.ServerCLI(), a.ID); err != nil {
			return err
		}
		fmt.Fprintln(out, i18n.T("server.account.enabled", i18n.D{"Name": a.Name}))
		return nil
	case "server-admin":
		var on, off bool
		a, err := named("account server-admin", func(fs *flag.FlagSet) {
			fs.BoolVar(&on, "on", false, i18n.T("server.flag.grant"))
			fs.BoolVar(&off, "off", false, i18n.T("server.flag.revoke"))
		})
		if err != nil {
			return err
		}
		if on == off {
			return i18n.Errorf("server.account.on_off_required")
		}
		if err := svc.SetServerAdmin(ctx, core.ServerCLI(), a.ID, on); err != nil {
			return err
		}
		if on {
			fmt.Fprintln(out, i18n.T("server.account.server_admin_on", i18n.D{"Name": a.Name}))
		} else {
			fmt.Fprintln(out, i18n.T("server.account.server_admin_off", i18n.D{"Name": a.Name}))
		}
		return nil
	}
	fmt.Fprint(out, usage())
	return i18n.Errorf("server.unknown_command", i18n.D{"Command": strconv.Quote("account " + sub)})
}

func yesNo(b bool) string {
	if b {
		return i18n.T("server.yes")
	}
	return ""
}

func roleLabel(role string) string {
	if role == store.RoleAdmin {
		return i18n.T("server.role.admin")
	}
	return i18n.T("server.role.member")
}
