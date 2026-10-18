package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// admin 处理 workspace、account、member、token、service 几组管理命令。
// member、token、service 组可用 --workspace 指定工作空间，缺省为 default。
func admin(ctx context.Context, out io.Writer, args []string) error {
	if len(args) < 2 {
		fmt.Fprint(out, usage())
		return i18n.Errorf("server.subcommand_missing", i18n.D{"Group": args[0]})
	}
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	group, sub, rest := args[0], args[1], args[2:]
	if group == "workspace" {
		return workspaceCmd(ctx, out, pool, sub, rest)
	}
	if group == "account" {
		return accountCmd(ctx, out, pool, sub, rest)
	}
	wsName, rest, err := extractWorkspace(rest)
	if err != nil {
		return err
	}
	ws, err := resolveWorkspace(ctx, pool, wsName)
	if err != nil {
		return err
	}

	switch group + " " + sub {
	case "member add":
		return memberAdd(ctx, out, pool, ws, rest)
	case "member list":
		return memberList(ctx, out, pool, ws, rest)
	case "member remove":
		return memberRemove(ctx, out, pool, ws, rest)
	case "member set":
		return memberSet(ctx, out, pool, ws, rest)
	case "token issue":
		return tokenIssue(ctx, out, pool, ws, rest)
	case "token list":
		return tokenList(ctx, out, pool, ws)
	case "token revoke":
		return tokenRevoke(ctx, out, pool, ws, rest)
	case "service add":
		return serviceAdd(ctx, out, pool, ws, rest)
	case "service list":
		return serviceList(ctx, out, pool, ws)
	}
	fmt.Fprint(out, usage())
	return i18n.Errorf("server.unknown_command", i18n.D{"Command": strconv.Quote(group + " " + sub)})
}

// extractWorkspace 从参数中取出 --workspace，返回其值与剩余参数。
// 漏写值时报错，而不是悄悄落回 default（否则令牌可能签到错误的工作空间）。
func extractWorkspace(args []string) (string, []string, error) {
	name := "default"
	missing := i18n.Errorf("server.workspace.name_missing")
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--workspace" || a == "-workspace":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				return "", nil, missing
			}
			name = args[i+1]
			i++
		case strings.HasPrefix(a, "--workspace=") || strings.HasPrefix(a, "-workspace="):
			_, name, _ = strings.Cut(a, "=")
			if name == "" {
				return "", nil, missing
			}
		default:
			rest = append(rest, a)
		}
	}
	return name, rest, nil
}

func resolveWorkspace(ctx context.Context, pool *pgxpool.Pool, name string) (int64, error) {
	if name == "default" {
		return store.DefaultWorkspace(ctx, pool)
	}
	w, err := store.WorkspaceByName(ctx, pool, name)
	if errors.Is(err, store.ErrNotFound) {
		return 0, i18n.Errorf("server.workspace.not_found_hint", i18n.D{"Name": strconv.Quote(name)})
	}
	return w.ID, err
}

func parse(name string, args []string, define func(fs *flag.FlagSet)) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	define(fs)
	return fs.Parse(args)
}

func required(pairs ...string) error {
	for i := 0; i < len(pairs); i += 2 {
		if pairs[i+1] == "" {
			return i18n.Errorf("server.arg_missing", i18n.D{"Name": pairs[i]})
		}
	}
	return nil
}

func memberAdd(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var name, display, role string
	if err := parse("member add", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", i18n.T("server.flag.username_unique"))
		fs.StringVar(&display, "display", "", i18n.T("server.flag.display_new_account"))
		fs.StringVar(&role, "role", store.RoleMember, i18n.T("server.flag.role_add"))
	}); err != nil {
		return err
	}
	if err := required("name", name); err != nil {
		return err
	}
	if role != store.RoleAdmin && role != store.RoleMember {
		return i18n.Errorf("server.member.role_invalid", i18n.D{"Role": strconv.Quote(role)})
	}
	m, created, err := (&core.Service{Pool: pool}).AddMemberCLI(ctx, core.ServerCLI(), ws, name, display, role)
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintln(out, i18n.T("server.member.created", i18n.D{"Name": m.Name, "ID": m.ID}))
	} else {
		fmt.Fprintln(out, i18n.T("server.member.added", i18n.D{"Name": m.Name, "ID": m.ID}))
	}
	return nil
}

func memberList(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var all bool
	if err := parse("member list", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&all, "all", false, i18n.T("server.flag.include_removed"))
	}); err != nil {
		return err
	}
	list, err := store.ListAllMembers(ctx, pool, ws)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, i18n.T("server.table.members"))
	for _, m := range list {
		if !all && m.Status != store.MemberActive {
			continue
		}
		tgID := ""
		if m.TelegramUserID != nil {
			tgID = strconv.FormatInt(*m.TelegramUserID, 10)
		}
		state := i18n.T("server.state.active")
		switch {
		case m.Status != store.MemberActive:
			state = i18n.T("server.state.removed")
		case m.AccountDisabled:
			state = i18n.T("server.state.disabled")
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", m.ID, m.Name, m.DisplayName, roleLabel(m.Role), state, m.TelegramUsername, tgID)
	}
	return tw.Flush()
}

func memberRemove(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var name, to, as string
	if err := parse("member remove", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", i18n.T("server.flag.remove_username"))
		fs.StringVar(&to, "handover-to", "", i18n.T("server.flag.handover_to"))
		fs.StringVar(&as, "as", "", i18n.T("server.flag.as_admin"))
	}); err != nil {
		return err
	}
	if err := required("name", name, "handover-to", to, "as", as); err != nil {
		return err
	}
	svc := &core.Service{Pool: pool}
	m, err := store.MemberByName(ctx, pool, ws, name)
	if errors.Is(err, store.ErrNotFound) {
		return i18n.Errorf("server.member.not_found_or_removed", i18n.D{"Name": strconv.Quote(name)})
	}
	if err != nil {
		return err
	}
	r, err := store.MemberByName(ctx, pool, ws, to)
	if errors.Is(err, store.ErrNotFound) {
		return i18n.Errorf("server.member.handover_not_member", i18n.D{"Name": strconv.Quote(to)})
	}
	if err != nil {
		return err
	}
	h, err := svc.GetHandover(ctx, nil, ws, m.ID)
	if err != nil {
		return err
	}
	plan := core.HandoverPlan{As: as}
	for _, it := range h.Issues {
		plan.Issues = append(plan.Issues, core.PlanIssue{IssueID: it.IssueID, As: it.As, ToMemberID: r.ID, ExpectedVersion: it.Version})
	}
	for _, n := range h.Services {
		plan.Services = append(plan.Services, core.PlanService{Name: n, ToMemberID: r.ID})
	}
	if err := svc.ApplyHandover(ctx, core.ServerCLI(), ws, m.ID, plan, true); err != nil {
		return err
	}
	fmt.Fprintln(out, i18n.T("server.member.removed", i18n.D{"Name": name, "Issues": len(plan.Issues), "Services": len(plan.Services), "To": to}))
	return nil
}

func tokenIssue(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var member, tool string
	if err := parse("token issue", args, func(fs *flag.FlagSet) {
		fs.StringVar(&member, "member", "", i18n.T("server.flag.username"))
		fs.StringVar(&tool, "tool", "", i18n.T("server.flag.tool"))
	}); err != nil {
		return err
	}
	if err := required("member", member, "tool", tool); err != nil {
		return err
	}
	t, err := (&core.Service{Pool: pool}).IssueToken(ctx, core.ServerCLI(), ws, member, tool)
	if errors.Is(err, store.ErrNotFound) {
		return i18n.Errorf("server.member.not_found", i18n.D{"Name": strconv.Quote(member)})
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(out, i18n.T("server.token.issued", i18n.D{"Member": t.Member, "Tool": tool, "ID": t.ID, "Token": t.Plain}))
	return nil
}

func tokenList(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64) error {
	list, err := store.ListTokens(ctx, pool, ws)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, i18n.T("server.table.tokens"))
	for _, t := range list {
		state := i18n.T("server.state.valid")
		if t.RevokedAt != nil {
			state = i18n.T("server.state.revoked")
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", t.ID, t.MemberName, t.Tool, t.CreatedAt.Format("2006-01-02 15:04"), state)
	}
	return tw.Flush()
}

func tokenRevoke(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var id int64
	if err := parse("token revoke", args, func(fs *flag.FlagSet) {
		fs.Int64Var(&id, "id", 0, i18n.T("server.flag.token_id"))
	}); err != nil {
		return err
	}
	if id <= 0 {
		return i18n.Errorf("server.arg_missing", i18n.D{"Name": "id"})
	}
	if err := (&core.Service{Pool: pool}).RevokeToken(ctx, core.ServerCLI(), ws, id); errors.Is(err, store.ErrNotFound) {
		return i18n.Errorf("server.token.not_found", i18n.D{"ID": id})
	} else if err != nil {
		return err
	}
	fmt.Fprintln(out, i18n.T("server.token.revoked", i18n.D{"ID": id}))
	return nil
}

func serviceAdd(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var name, owner string
	if err := parse("service add", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", i18n.T("server.flag.service_name"))
		fs.StringVar(&owner, "owner", "", i18n.T("server.flag.owner_username"))
	}); err != nil {
		return err
	}
	if err := required("name", name, "owner", owner); err != nil {
		return err
	}
	m, err := (&core.Service{Pool: pool}).SetServiceOwner(ctx, core.ServerCLI(), ws, name, owner)
	if errors.Is(err, store.ErrNotFound) {
		return i18n.Errorf("server.member.not_found", i18n.D{"Name": strconv.Quote(owner)})
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(out, i18n.T("server.service.owner_set", i18n.D{"Name": name, "Owner": m.Name}))
	return nil
}

func serviceList(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64) error {
	list, err := store.ListServices(ctx, pool, ws)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, i18n.T("server.table.services"))
	for _, s := range list {
		fmt.Fprintf(tw, "%s\t%s\n", s.Name, s.OwnerName)
	}
	return tw.Flush()
}
