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
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// admin 处理 workspace、account、member、token、service 几组管理命令。
// member、token、service 组可用 --workspace 指定工作空间，缺省为 default。
func admin(ctx context.Context, out io.Writer, args []string) error {
	if len(args) < 2 {
		fmt.Fprint(out, usage)
		return fmt.Errorf("%s 缺少子命令", args[0])
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
	fmt.Fprint(out, usage)
	return fmt.Errorf("未知命令 %q", group+" "+sub)
}

// extractWorkspace 从参数中取出 --workspace，返回其值与剩余参数。
// 漏写值时报错，而不是悄悄落回 default（否则令牌可能签到错误的工作空间）。
func extractWorkspace(args []string) (string, []string, error) {
	name := "default"
	missing := errors.New("--workspace 缺少工作空间名")
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
		return 0, fmt.Errorf("工作空间 %q 不存在，可用 workspace add 创建", name)
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
			return fmt.Errorf("缺少参数 --%s", pairs[i])
		}
	}
	return nil
}

func memberAdd(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var name, display, role string
	if err := parse("member add", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", "登录名（全服务器唯一，登录与指派时使用）")
		fs.StringVar(&display, "display", "", "显示名（新建账号时使用）")
		fs.StringVar(&role, "role", store.RoleMember, "角色：admin 或 member")
	}); err != nil {
		return err
	}
	if err := required("name", name); err != nil {
		return err
	}
	if role != store.RoleAdmin && role != store.RoleMember {
		return fmt.Errorf("角色只能是 admin 或 member，收到 %q", role)
	}
	if err := core.CheckName(name); err != nil {
		return err
	}
	acc, err := store.AccountByName(ctx, pool, name)
	created := false
	if errors.Is(err, store.ErrNotFound) {
		acc, err = store.CreateAccount(ctx, pool, name, display)
		created = true
	}
	if err != nil {
		return err
	}
	if acc.Disabled() {
		return fmt.Errorf("账号 %s 已停用，不能加入", name)
	}
	m, err := store.AddMembership(ctx, pool, acc.ID, ws, role, nil)
	if errors.Is(err, store.ErrAlreadyMember) {
		return fmt.Errorf("%s 已经是这个工作空间的成员", name)
	}
	if err != nil {
		return err
	}
	if created {
		fmt.Fprintf(out, "已新建账号 %s 并加入工作空间（成员编号 %d）。账号还没有密码，可用 admin reset-password 生成\n", m.Name, m.ID)
	} else {
		fmt.Fprintf(out, "已把账号 %s 加入工作空间（成员编号 %d）\n", m.Name, m.ID)
	}
	return nil
}

func memberList(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var all bool
	if err := parse("member list", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&all, "all", false, "包括已移出的")
	}); err != nil {
		return err
	}
	list, err := store.ListAllMembers(ctx, pool, ws)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "编号\t登录名\t显示名\t角色\t状态\tTelegram 用户名\tTelegram 编号")
	for _, m := range list {
		if !all && m.Status != store.MemberActive {
			continue
		}
		tgID := ""
		if m.TelegramUserID != nil {
			tgID = strconv.FormatInt(*m.TelegramUserID, 10)
		}
		state := "在册"
		switch {
		case m.Status != store.MemberActive:
			state = "已移出"
		case m.AccountDisabled:
			state = "已停用"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n", m.ID, m.Name, m.DisplayName, roleLabel(m.Role), state, m.TelegramUsername, tgID)
	}
	return tw.Flush()
}

func memberRemove(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var name, to, as string
	if err := parse("member remove", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", "要移出的成员登录名")
		fs.StringVar(&to, "handover-to", "", "接手人登录名：名下待处理问题单与服务全部转给他")
		fs.StringVar(&as, "as", "", "记作操作人的本工作空间管理员登录名")
	}); err != nil {
		return err
	}
	if err := required("name", name, "handover-to", to, "as", as); err != nil {
		return err
	}
	svc := &core.Service{Pool: pool}
	m, err := store.MemberByName(ctx, pool, ws, name)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("成员 %q 不存在或已移出", name)
	}
	if err != nil {
		return err
	}
	r, err := store.MemberByName(ctx, pool, ws, to)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("接手人 %q 不是这个工作空间的成员", to)
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
	if err := svc.ApplyHandover(ctx, nil, ws, m.ID, plan, true); err != nil {
		return err
	}
	fmt.Fprintf(out, "已移出 %s：%d 张问题单、%d 个服务转给 %s，他在这个工作空间的令牌已吊销\n", name, len(plan.Issues), len(plan.Services), to)
	return nil
}

func tokenIssue(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var member, tool string
	if err := parse("token issue", args, func(fs *flag.FlagSet) {
		fs.StringVar(&member, "member", "", "成员名")
		fs.StringVar(&tool, "tool", "", "工具名，例如 claude-code、codex")
	}); err != nil {
		return err
	}
	if err := required("member", member, "tool", tool); err != nil {
		return err
	}
	m, err := store.MemberByName(ctx, pool, ws, member)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("成员 %q 不存在", member)
	}
	if err != nil {
		return err
	}
	plain, id, err := store.IssueToken(ctx, pool, m.ID, tool)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "已为 %s 的 %s 签发令牌（编号 %d）。令牌只显示这一次，请妥善保存：\n%s\n", m.Name, tool, id, plain)
	return nil
}

func tokenList(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64) error {
	list, err := store.ListTokens(ctx, pool, ws)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "编号\t成员\t工具\t签发时间\t状态")
	for _, t := range list {
		state := "有效"
		if t.RevokedAt != nil {
			state = "已吊销"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\n", t.ID, t.MemberName, t.Tool, t.CreatedAt.Format("2006-01-02 15:04"), state)
	}
	return tw.Flush()
}

func tokenRevoke(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var id int64
	if err := parse("token revoke", args, func(fs *flag.FlagSet) {
		fs.Int64Var(&id, "id", 0, "令牌编号")
	}); err != nil {
		return err
	}
	if id <= 0 {
		return fmt.Errorf("缺少参数 --id")
	}
	if err := store.RevokeTokenInWorkspace(ctx, pool, ws, id); errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("令牌 %d 不存在、已吊销，或不属于这个工作空间", id)
	} else if err != nil {
		return err
	}
	fmt.Fprintf(out, "已吊销令牌 %d\n", id)
	return nil
}

func serviceAdd(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64, args []string) error {
	var name, owner string
	if err := parse("service add", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", "服务名")
		fs.StringVar(&owner, "owner", "", "负责人成员名")
	}); err != nil {
		return err
	}
	if err := required("name", name, "owner", owner); err != nil {
		return err
	}
	m, err := store.MemberByName(ctx, pool, ws, owner)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("成员 %q 不存在", owner)
	}
	if err != nil {
		return err
	}
	if m.AccountDisabled {
		return fmt.Errorf("成员 %q 已停用，不能登记为负责人", owner)
	}
	if err := store.UpsertService(ctx, pool, ws, name, m.ID); err != nil {
		return err
	}
	fmt.Fprintf(out, "服务 %s 的负责人为 %s\n", name, m.Name)
	return nil
}

func serviceList(ctx context.Context, out io.Writer, pool *pgxpool.Pool, ws int64) error {
	list, err := store.ListServices(ctx, pool, ws)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "服务\t负责人")
	for _, s := range list {
		fmt.Fprintf(tw, "%s\t%s\n", s.Name, s.OwnerName)
	}
	return tw.Flush()
}
