package core

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// StateError 表示请求与当前状态冲突（已是成员、群已被占用、工作空间不空等），接口返回 409。
type StateError struct{ Msg string }

func (e *StateError) Error() string { return e.Msg }

func stateErr(format string, args ...any) error {
	return &StateError{Msg: fmt.Sprintf(format, args...)}
}

// ---- 角色与工作空间设置 ----

// SetRole 由管理员修改成员角色；工作空间至少保留一名在册管理员。wsID 非 0 时成员必须属于该工作空间。
func (s *Service) SetRole(ctx context.Context, act Actor, wsID, memberID int64, role string) error {
	actor := act.Account
	if role != store.RoleAdmin && role != store.RoleMember {
		return inputErr("角色应为管理员或成员")
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		m, err := store.MemberByID(ctx, tx, memberID)
		if err != nil {
			return err
		}
		if (wsID != 0 && m.WorkspaceID != wsID) || m.Status != store.MemberActive {
			return fmt.Errorf("成员 %d：%w", memberID, store.ErrNotFound)
		}
		// 锁住本工作空间的全部管理员行：两名管理员同时互相降级时排队执行，后一个会看到前一个的结果
		if err := store.LockAdmins(ctx, tx, m.WorkspaceID); err != nil {
			return err
		}
		// 拿到锁之后再判定操作者（按库里当前的角色）
		if err := canAdmin(ctx, tx, actor, m.WorkspaceID); err != nil {
			return err
		}
		if m.Role == role {
			return nil
		}
		if err := store.SetMemberRole(ctx, tx, m.ID, role); err != nil {
			return err
		}
		n, err := store.CountAdmins(ctx, tx, m.WorkspaceID)
		if err != nil {
			return err
		}
		if n == 0 {
			return inputErr("工作空间至少要保留一名管理员")
		}
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(m.WorkspaceID), Action: ActMemberRole,
			TargetKind: "member", TargetID: idp(m.ID), TargetName: m.Name,
			Detail: map[string]any{"from": m.Role, "to": role}})
	})
}

// WorkspaceSettings 是管理页可修改的工作空间设置。对外地址已改为服务器级（SetExternalURL）。
type WorkspaceSettings struct {
	TelegramChat string
	ShowTitle    bool
	ShowBrief    bool
	DisplayName  *string // 为空时不改显示名；指向空串表示恢复为路径
}

// UpdateWorkspace 由管理员修改工作空间设置。群编号为空表示关闭群通知；一个群只能绑一个工作空间。
func (s *Service) UpdateWorkspace(ctx context.Context, act Actor, wsID int64, in WorkspaceSettings) error {
	actor := act.Account
	var display string
	if in.DisplayName != nil {
		var err error
		if display, err = cleanWorkspaceDisplayName(*in.DisplayName); err != nil {
			return err
		}
	}
	chat := strings.TrimSpace(in.TelegramChat)
	if chat != "" {
		if _, err := strconv.ParseInt(chat, 10, 64); err != nil {
			return inputErr("Telegram 群编号应为数字，例如 -1001234567890（群里发 /chatid 获取）")
		}
	}
	cfg := store.NotifyConfig{Target: chat, ShowTitle: in.ShowTitle, ShowBrief: in.ShowBrief}
	if chat != "" {
		cfg.Channel = "telegram"
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := canAdmin(ctx, tx, actor, wsID); err != nil {
			return err
		}
		if chat != "" {
			if other, err := store.WorkspaceByNotifyTarget(ctx, tx, "telegram", chat); err == nil && other.ID != wsID {
				return stateErr("这个群已经绑定了工作空间 %s：一个群只能对应一个工作空间", other.Name)
			} else if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		old, err := store.LockWorkspace(ctx, tx, wsID)
		if err != nil {
			return err
		}
		// 这里只管 Telegram 群：绑的是别的通讯工具的群（钉钉用群绑定码绑定）时，Telegram 群编号留空不解除它
		oldTG := ""
		if old.Notify.Channel == store.ProviderTelegram {
			oldTG = old.Notify.Target
		} else if old.Notify.Channel != "" {
			if chat != "" { // 第一期一个工作空间只绑一个群
				return stateErr("工作空间已绑定%s群，请先解绑再绑 Telegram 群", providerName(old.Notify.Channel))
			}
			cfg.Channel, cfg.Target = old.Notify.Channel, old.Notify.Target
		}
		err = store.SetWorkspaceNotify(ctx, tx, wsID, cfg)
		if isUniqueViolation(err) {
			return stateErr("这个群已经绑定了别的工作空间：一个群只能对应一个工作空间")
		}
		if err != nil {
			return err
		}
		changes := map[string]any{}
		diff := func(k string, from, to any) {
			if from != to {
				changes[k] = map[string]any{"from": from, "to": to}
			}
		}
		diff("telegram_chat", oldTG, chat)
		diff("show_title", old.Notify.ShowTitle, in.ShowTitle)
		diff("show_brief", old.Notify.ShowBrief, in.ShowBrief)
		if len(changes) > 0 {
			if err := record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(wsID), Action: ActWorkspaceSettings,
				TargetKind: "workspace", TargetID: idp(wsID), TargetName: old.Name, Detail: changes}); err != nil {
				return err
			}
		}
		if in.DisplayName == nil {
			return nil
		}
		return setDisplayName(ctx, tx, act, old, display)
	})
}

// providerName 是通讯工具的中文名。
func providerName(provider string) string {
	switch provider {
	case store.ProviderTelegram:
		return " Telegram "
	case store.ProviderDingTalk:
		return "钉钉"
	}
	return provider
}

// MaxWorkspaceDisplayName 是工作空间显示名的最大字符数。
const MaxWorkspaceDisplayName = 40

// cleanWorkspaceDisplayName 去掉首尾空白后校验：最多 40 个字符，不含控制字符；空串表示恢复为路径。
func cleanWorkspaceDisplayName(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len([]rune(s)) > MaxWorkspaceDisplayName {
		return "", inputErr("显示名最多 %d 个字符", MaxWorkspaceDisplayName)
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return "", inputErr("显示名不能含换行、制表符等控制字符")
		}
	}
	return s, nil
}

// setDisplayName 在事务里改显示名并写记录；值没变（含“与路径相同”的两种写法）不写。
func setDisplayName(ctx context.Context, tx pgx.Tx, act Actor, ws store.Workspace, display string) error {
	if display == ws.Name {
		display = "" // 与路径相同就存空，以后路径不变、显示名跟着路径
	}
	if display == ws.DisplayName {
		return nil
	}
	if err := store.SetWorkspaceDisplayName(ctx, tx, ws.ID, display); err != nil {
		return err
	}
	to := ws
	to.DisplayName = display
	return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(ws.ID), WorkspaceName: ws.Name, Action: ActWorkspaceDisplayName,
		TargetKind: "workspace", TargetID: idp(ws.ID), TargetName: ws.Name,
		Detail: map[string]any{"display_name": map[string]any{"from": ws.Label(), "to": to.Label()}}})
}

// SetWorkspaceDisplayName 由管理员修改工作空间显示名（空串恢复为路径）。路径不可改。
func (s *Service) SetWorkspaceDisplayName(ctx context.Context, act Actor, wsID int64, display string) error {
	display, err := cleanWorkspaceDisplayName(display)
	if err != nil {
		return err
	}
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := canAdmin(ctx, tx, act.Account, wsID); err != nil {
			return err
		}
		ws, err := store.LockWorkspace(ctx, tx, wsID)
		if err != nil {
			return err
		}
		return setDisplayName(ctx, tx, act, ws, display)
	})
}

// Workspace 返回工作空间。
func (s *Service) Workspace(ctx context.Context, id int64) (store.Workspace, error) {
	return store.GetWorkspace(ctx, s.Pool, id)
}

// AdminWorkspace 由管理员读取工作空间设置。
func (s *Service) AdminWorkspace(ctx context.Context, actor *store.Account, wsID int64) (store.Workspace, error) {
	if err := canAdmin(ctx, s.Pool, actor, wsID); err != nil {
		return store.Workspace{}, err
	}
	return store.GetWorkspace(ctx, s.Pool, wsID)
}

// WorkspaceByName 按名字返回工作空间。
func (s *Service) WorkspaceByName(ctx context.Context, name string) (store.Workspace, error) {
	return store.WorkspaceByName(ctx, s.Pool, name)
}

// AccountByTelegram 返回连接到该 Telegram 账号的 Cohive 账号（机器人识别点按钮的人）。
func (s *Service) AccountByTelegram(ctx context.Context, tgUserID int64) (store.Account, error) {
	return store.AccountByTelegramUserID(ctx, s.Pool, tgUserID)
}

// WorkspaceByGroup 返回绑定了该 Telegram 群的工作空间。
func (s *Service) WorkspaceByGroup(ctx context.Context, chatID int64) (store.Workspace, error) {
	return s.WorkspaceByChat(ctx, store.ProviderTelegram, "", strconv.FormatInt(chatID, 10))
}

// WorkspaceByChat 返回绑定了该群的工作空间（一个群只绑一个工作空间）。
func (s *Service) WorkspaceByChat(ctx context.Context, provider, tenant, chatID string) (store.Workspace, error) {
	c, err := store.WorkspaceChatOf(ctx, s.Pool, provider, tenant, chatID)
	if err != nil {
		return store.Workspace{}, err
	}
	return store.GetWorkspace(ctx, s.Pool, c.WorkspaceID)
}

// AccountByChat 返回连接到该通讯账号的 Cohive 账号（机器人识别点按钮的人）。
func (s *Service) AccountByChat(ctx context.Context, provider, tenant, userID string) (store.Account, error) {
	return store.AccountByChatIdentity(ctx, s.Pool, provider, tenant, userID)
}

// ChatIdentities 返回一批账号在各通讯工具上的连接（群消息里按编号 @ 人用）。
func (s *Service) ChatIdentities(ctx context.Context, accountIDs []int64) (map[int64][]store.ChatIdentity, error) {
	return store.ChatIdentitiesOf(ctx, s.Pool, accountIDs)
}

// ---- 成员 ----

// AdminMembers 由管理员列出工作空间的成员资格（status 为空列在册的，removed 列已移出的）。
func (s *Service) AdminMembers(ctx context.Context, actor *store.Account, wsID int64, status string) ([]store.MemberSummary, error) {
	if status != "" && status != store.MemberActive && status != store.MemberRemoved {
		return nil, inputErr("status 只能是 active 或 removed")
	}
	if err := canAdmin(ctx, s.Pool, actor, wsID); err != nil {
		return nil, err
	}
	return store.ListMemberSummaries(ctx, s.Pool, wsID, status)
}

// AccountLookup 是管理员按登录名查到的账号。
type AccountLookup struct {
	Account      store.Account
	MemberStatus string // none、active、removed
}

// LookupAccount 由工作空间管理员按登录名精确查找账号（不能浏览全部账号）。
func (s *Service) LookupAccount(ctx context.Context, actor *store.Account, wsID int64, name string) (AccountLookup, error) {
	if err := canAdmin(ctx, s.Pool, actor, wsID); err != nil {
		return AccountLookup{}, err
	}
	a, err := store.AccountByName(ctx, s.Pool, strings.TrimSpace(name))
	if errors.Is(err, store.ErrNotFound) {
		return AccountLookup{}, fmt.Errorf("没有登录名为 %s 的账号：%w", name, store.ErrNotFound)
	}
	if err != nil {
		return AccountLookup{}, err
	}
	out := AccountLookup{Account: a, MemberStatus: "none"}
	m, err := store.MemberOf(ctx, s.Pool, a.ID, wsID)
	if err == nil {
		out.MemberStatus = m.Status
	} else if !errors.Is(err, store.ErrNotFound) {
		return AccountLookup{}, err
	}
	return out, nil
}

// AddMember 由管理员把已有账号直接加入工作空间；被移出过的复用原成员资格。
func (s *Service) AddMember(ctx context.Context, act Actor, wsID int64, accountName, role string) (store.Member, error) {
	actor := act.Account
	if role == "" {
		role = store.RoleMember
	}
	if role != store.RoleAdmin && role != store.RoleMember {
		return store.Member{}, inputErr("角色应为管理员或成员")
	}
	var out store.Member
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := canAdmin(ctx, tx, actor, wsID); err != nil {
			return err
		}
		a, err := store.AccountByName(ctx, tx, strings.TrimSpace(accountName))
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("没有登录名为 %s 的账号：%w", accountName, store.ErrNotFound)
		}
		if err != nil {
			return err
		}
		if a, err = store.LockAccount(ctx, tx, a.ID); err != nil {
			return err
		}
		if a.Disabled() {
			return inputErr("账号 %s 已停用，不能加入", a.Name)
		}
		out, err = store.AddMembership(ctx, tx, a.ID, wsID, role, actorID(actor))
		if errors.Is(err, store.ErrAlreadyMember) {
			return stateErr("%s 已经是这个工作空间的成员", a.Name)
		}
		if err != nil {
			return err
		}
		return recordMemberAdd(ctx, tx, act, out, a.Name, role, false)
	})
	return out, err
}

func recordMemberAdd(ctx context.Context, tx pgx.Tx, act Actor, m store.Member, name, role string, created bool) error {
	d := map[string]any{"role": role}
	if created {
		d["account_created"] = true
	}
	return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(m.WorkspaceID), Action: ActMemberAdd,
		TargetKind: "member", TargetID: idp(m.ID), TargetName: name, Detail: d})
}

// AddMemberCLI 由服务器命令行把账号加入工作空间；账号不存在时新建（没有密码）。返回是否新建了账号。
func (s *Service) AddMemberCLI(ctx context.Context, act Actor, wsID int64, name, display, role string) (store.Member, bool, error) {
	if role != store.RoleAdmin && role != store.RoleMember {
		return store.Member{}, false, inputErr("角色只能是 admin 或 member，收到 %q", role)
	}
	if err := checkMemberName(name); err != nil {
		return store.Member{}, false, err
	}
	var out store.Member
	created := false
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := requireServerAdmin(ctx, tx, act.Account); err != nil {
			return err
		}
		a, err := store.AccountByName(ctx, tx, name)
		if errors.Is(err, store.ErrNotFound) {
			a, err = store.CreateAccount(ctx, tx, name, cleanDisplayName(display))
			created = true
		}
		if err != nil {
			return err
		}
		if a.Disabled() {
			return inputErr("账号 %s 已停用，不能加入", name)
		}
		out, err = store.AddMembership(ctx, tx, a.ID, wsID, role, actorID(act.Account))
		if errors.Is(err, store.ErrAlreadyMember) {
			return stateErr("%s 已经是这个工作空间的成员", name)
		}
		if err != nil {
			return err
		}
		return recordMemberAdd(ctx, tx, act, out, a.Name, role, created)
	})
	return out, created, err
}

// Handover 是成员名下需要转交的问题单与服务。
type Handover struct {
	Member   store.Member
	Issues   []store.HandoverIssue
	Services []string
}

// HandoverPlan 是管理员为每一项选的接手人。必须覆盖 Handover 的全部条目。
type HandoverPlan struct {
	Issues   []PlanIssue
	Services []PlanService
	// As 只在服务器命令行使用：记作操作人的本空间管理员登录名（问题单事件的操作人必须是成员资格）。
	As string
}

// PlanIssue 是一张问题单的接手人。
type PlanIssue struct {
	IssueID         int64
	As              string // owner 或 source
	ToMemberID      int64
	ExpectedVersion int
}

// PlanService 是一个服务的新负责人。
type PlanService struct {
	Name       string
	ToMemberID int64
}

// HandoverError 表示转交清单不完整或问题单已变化，附最新清单，接口返回 409。
type HandoverError struct {
	Msg      string
	Handover Handover
}

func (e *HandoverError) Error() string { return e.Msg }

func loadHandover(ctx context.Context, q store.DBTX, m store.Member) (Handover, error) {
	h := Handover{Member: m}
	var err error
	if h.Issues, err = store.PendingIssuesOf(ctx, q, m.ID); err != nil {
		return h, err
	}
	h.Services, err = store.ServicesOwnedBy(ctx, q, m.ID)
	return h, err
}

// GetHandover 由管理员查看成员名下要转交的问题单与服务。
func (s *Service) GetHandover(ctx context.Context, actor *store.Account, wsID, memberID int64) (Handover, error) {
	m, err := store.MemberByID(ctx, s.Pool, memberID)
	if err != nil {
		return Handover{}, err
	}
	if m.WorkspaceID != wsID {
		return Handover{}, fmt.Errorf("成员 %d：%w", memberID, store.ErrNotFound)
	}
	if err := canAdmin(ctx, s.Pool, actor, wsID); err != nil {
		return Handover{}, err
	}
	return loadHandover(ctx, s.Pool, m)
}

// ApplyHandover 由管理员把成员名下的问题单与服务转交给别人；remove 为真时随后把他移出工作空间
// 并吊销他在该工作空间的全部令牌。全部在一个事务里：清单不完整或任何一张单的版本已变化时整体不做。
func (s *Service) ApplyHandover(ctx context.Context, act Actor, wsID, memberID int64, plan HandoverPlan, remove bool) error {
	actor := act.Account
	return s.inTx(ctx, func(tx pgx.Tx) error {
		// 按包说明的加锁顺序：先按编号升序锁齐清单里的问题单，再锁管理员行与成员行。
		// 负责人接单等动作先锁问题单、写事件时再引用成员行；这里若先锁成员行再等问题单，就会与之互等。
		ids := make([]int64, 0, len(plan.Issues))
		for _, p := range plan.Issues {
			ids = append(ids, p.IssueID)
		}
		if err := store.LockIssues(ctx, tx, wsID, ids); err != nil {
			return err
		}
		if remove {
			// 先锁本空间的管理员行再锁被移出的人，与并发降级、互相移出排队（顺序一致，不会互相等待）
			if err := store.LockAdmins(ctx, tx, wsID); err != nil {
				return err
			}
		}
		m, err := store.LockMember(ctx, tx, memberID)
		if err != nil {
			return err
		}
		if m.WorkspaceID != wsID || m.Status != store.MemberActive {
			return fmt.Errorf("成员 %d：%w", memberID, store.ErrNotFound)
		}
		if err := canAdmin(ctx, tx, actor, wsID); err != nil {
			return err
		}
		// 改派要写问题单事件，操作人必须是本空间在册成员
		var op store.Member
		switch {
		case actor != nil:
			if op, err = store.MemberOf(ctx, tx, actor.ID, wsID); err != nil || op.Status != store.MemberActive {
				if err != nil && !errors.Is(err, store.ErrNotFound) {
					return err
				}
				return inputErr("你不是这个工作空间的成员：转交要记操作人，请先在管理页把自己加入这个工作空间")
			}
		default:
			if op, err = store.MemberByName(ctx, tx, wsID, plan.As); err != nil || !op.IsAdmin() {
				return inputErr("命令行转交需要用 --as 指定本工作空间的一名在册管理员")
			}
		}
		if op.ID == m.ID && remove {
			return inputErr("不能移出自己")
		}
		if remove && m.IsAdmin() {
			n, err := store.CountAdmins(ctx, tx, wsID)
			if err != nil {
				return err
			}
			if n <= 1 {
				return inputErr("不能移出这个工作空间的最后一名管理员")
			}
		}
		h, err := loadHandover(ctx, tx, m)
		if err != nil {
			return err
		}
		if err := checkPlan(ctx, tx, h, plan); err != nil {
			return err
		}
		// 同一张单可能两行（他既是负责人又是发起人），合并成一次更新
		type change struct {
			owner, source *int64
			version       int
		}
		changes := map[int64]*change{}
		var order []int64
		for _, p := range plan.Issues {
			c := changes[p.IssueID]
			if c == nil {
				c = &change{version: p.ExpectedVersion}
				changes[p.IssueID] = c
				order = append(order, p.IssueID)
			}
			to := p.ToMemberID
			if p.As == store.AsOwner {
				c.owner = &to
			} else {
				c.source = &to
			}
		}
		for _, id := range order {
			c := changes[id]
			is, err := store.GetIssueForUpdate(ctx, tx, id)
			if err != nil {
				return err
			}
			if is.Version != c.version {
				return &HandoverError{Msg: fmt.Sprintf("问题单 #%d 已有新进展，请刷新转交清单后重试", id), Handover: h}
			}
			if err := reassign(ctx, tx, op, is, c.owner, c.source); err != nil {
				return err
			}
		}
		for _, p := range plan.Services {
			if err := store.SetServiceOwner(ctx, tx, wsID, p.Name, p.ToMemberID); err != nil {
				return err
			}
		}
		names := map[int64]string{}
		nameOf := func(id int64) (string, error) {
			if n, ok := names[id]; ok {
				return n, nil
			}
			r, err := store.MemberByID(ctx, tx, id)
			names[id] = r.Name
			return r.Name, err
		}
		issues := make([]map[string]any, 0, len(plan.Issues))
		for _, p := range plan.Issues {
			to, err := nameOf(p.ToMemberID)
			if err != nil {
				return err
			}
			issues = append(issues, map[string]any{"id": p.IssueID, "as": p.As, "to": to})
		}
		services := make([]map[string]any, 0, len(plan.Services))
		for _, p := range plan.Services {
			to, err := nameOf(p.ToMemberID)
			if err != nil {
				return err
			}
			services = append(services, map[string]any{"name": p.Name, "to": to})
		}
		detail := map[string]any{"issues": issues, "services": services}
		if actor == nil {
			detail["as"] = op.Name
		}
		action := ActMemberHandover
		if remove {
			action = ActMemberRemove
			n, err := store.CountActiveMemberTokens(ctx, tx, m.ID)
			if err != nil {
				return err
			}
			detail["tokens_revoked"] = n
			if err := store.RemoveMembership(ctx, tx, m.ID, actorID(actor)); err != nil {
				return err
			}
		} else if len(issues) == 0 && len(services) == 0 {
			return nil
		}
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(wsID), Action: action, TargetKind: "member",
			TargetID: idp(m.ID), TargetName: m.Name, Detail: detail})
	})
}

// checkPlan 校验转交清单完整、版本一致、接手人合格。
func checkPlan(ctx context.Context, tx pgx.Tx, h Handover, plan HandoverPlan) error {
	stale := func(msg string) error { return &HandoverError{Msg: msg, Handover: h} }
	want := map[string]int{}
	for _, it := range h.Issues {
		want[fmt.Sprintf("%d/%s", it.IssueID, it.As)] = it.Version
	}
	seen := map[string]bool{}
	receivers := map[int64]bool{}
	for _, p := range plan.Issues {
		if p.As != store.AsOwner && p.As != store.AsSource {
			return inputErr("as 只能是 owner 或 source")
		}
		k := fmt.Sprintf("%d/%s", p.IssueID, p.As)
		v, ok := want[k]
		if !ok || seen[k] {
			return stale(fmt.Sprintf("转交清单与当前情况不一致（问题单 #%d），请刷新后重试", p.IssueID))
		}
		if v != p.ExpectedVersion {
			return stale(fmt.Sprintf("问题单 #%d 已有新进展，请刷新转交清单后重试", p.IssueID))
		}
		seen[k] = true
		receivers[p.ToMemberID] = true
	}
	if len(seen) != len(want) {
		return stale("还有问题单没有选接手人，请为每一张选择接手人")
	}
	wantSvc := map[string]bool{}
	for _, n := range h.Services {
		wantSvc[n] = true
	}
	seenSvc := map[string]bool{}
	for _, p := range plan.Services {
		if !wantSvc[p.Name] || seenSvc[p.Name] {
			return stale(fmt.Sprintf("转交清单与当前情况不一致（服务 %s），请刷新后重试", p.Name))
		}
		seenSvc[p.Name] = true
		receivers[p.ToMemberID] = true
	}
	if len(seenSvc) != len(wantSvc) {
		return stale("还有服务没有选新的负责人")
	}
	for id := range receivers {
		r, err := store.MemberByID(ctx, tx, id)
		if errors.Is(err, store.ErrNotFound) {
			return inputErr("接手人 %d 不存在", id)
		}
		if err != nil {
			return err
		}
		if r.WorkspaceID != h.Member.WorkspaceID || !r.Active() || r.ID == h.Member.ID {
			return inputErr("接手人 %s 不是这个工作空间在册、未停用的其他成员", r.Name)
		}
	}
	return nil
}

// reassign 由管理员改派问题单的负责人与 / 或发起人：版本号加一，写事件，给新接手人入队群通知。
func reassign(ctx context.Context, tx pgx.Tx, op store.Member, is store.Issue, owner, source *int64) error {
	payload := map[string]any{"reason": "removed"}
	name := func(id int64) string {
		if m, err := store.MemberByID(ctx, tx, id); err == nil {
			return m.Name
		}
		return ""
	}
	if owner != nil {
		if is.OwnerID != nil {
			payload["owner_from"] = *is.OwnerID
		}
		payload["owner_to"] = *owner
		payload["owner_to_name"] = name(*owner)
		is.OwnerID = owner
	}
	if source != nil {
		payload["source_from"] = is.SourceMemberID
		payload["source_to"] = *source
		payload["source_to_name"] = name(*source)
		if err := store.SetIssueSource(ctx, tx, is.ID, *source); err != nil {
			return err
		}
		is.SourceMemberID = *source
	}
	if err := store.UpdateIssue(ctx, tx, &is); err != nil {
		return err
	}
	who := store.Identity{AccountID: op.AccountID, MemberID: op.ID, WorkspaceID: op.WorkspaceID, MemberName: op.Name, Tool: "web"}
	if err := store.InsertEvent(ctx, tx, store.Event{IssueID: is.ID, ActorMemberID: op.ID, ActorTool: who.Tool,
		Action: issue.ActReassign, FromStatus: is.Status, ToStatus: is.Status, Note: "管理员转交", Payload: payload}); err != nil {
		return err
	}
	return queueReassign(ctx, tx, who, is, owner != nil, source != nil)
}

// ---- 停用与启用、服务器管理员 ----

// DisableAccount 由服务器管理员停用账号（作用于整个人）：吊销全部设备与令牌、注销全部网页登录、
// 断开 Telegram、作废等待中的设备申请。不能停用自己，不能停用最后一名服务器管理员。
func (s *Service) DisableAccount(ctx context.Context, act Actor, accountID int64) error {
	actor := act.Account
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := store.LockServerAdmins(ctx, tx); err != nil {
			return err
		}
		if err := requireServerAdmin(ctx, tx, actor); err != nil {
			return err
		}
		if actor != nil && actor.ID == accountID {
			return inputErr("不能停用自己")
		}
		a, err := store.LockAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if a.Disabled() {
			return stateErr("账号 %s 已经停用", a.Name)
		}
		if a.ServerAdmin {
			n, err := store.CountActiveServerAdmins(ctx, tx)
			if err != nil {
				return err
			}
			if n <= 1 {
				return inputErr("不能停用最后一名服务器管理员")
			}
		}
		if err := store.SetAccountDisabled(ctx, tx, a.ID, true, actorID(actor)); err != nil {
			return err
		}
		devices, err := store.CountActiveDevices(ctx, tx, a.ID)
		if err != nil {
			return err
		}
		if err := store.RevokeAccountDevices(ctx, tx, a.ID); err != nil {
			return err
		}
		if err := store.DeleteAccountSessions(ctx, tx, a.ID, nil); err != nil {
			return err
		}
		if err := store.DeleteAccountWebLoginCodes(ctx, tx, a.ID); err != nil {
			return err
		}
		if err := store.SetAccountTelegram(ctx, tx, a.ID, nil, ""); err != nil {
			return err
		}
		if err := store.DenyPendingDeviceAuths(ctx, tx, a.ID); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{Action: ActAccountDisable, TargetKind: "account",
			TargetID: idp(a.ID), TargetName: a.Name, Detail: map[string]any{"devices_revoked": devices}})
	})
}

// EnableAccount 由服务器管理员启用账号。设备不恢复：要重新配对，并因“有过设备”需管理员批准。
func (s *Service) EnableAccount(ctx context.Context, act Actor, accountID int64) error {
	actor := act.Account
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := requireServerAdmin(ctx, tx, actor); err != nil {
			return err
		}
		a, err := store.LockAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if !a.Disabled() {
			return stateErr("账号 %s 没有停用", a.Name)
		}
		if err := store.SetAccountDisabled(ctx, tx, a.ID, false, nil); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{Action: ActAccountEnable, TargetKind: "account",
			TargetID: idp(a.ID), TargetName: a.Name})
	})
}

// SetServerAdmin 由服务器管理员授予或撤销服务器管理员；至少保留一名未停用的服务器管理员。
func (s *Service) SetServerAdmin(ctx context.Context, act Actor, accountID int64, on bool) error {
	actor := act.Account
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := store.LockServerAdmins(ctx, tx); err != nil {
			return err
		}
		if err := requireServerAdmin(ctx, tx, actor); err != nil {
			return err
		}
		a, err := store.LockAccount(ctx, tx, accountID)
		if err != nil {
			return err
		}
		if a.ServerAdmin == on {
			return nil
		}
		if err := store.SetServerAdmin(ctx, tx, a.ID, on); err != nil {
			return err
		}
		n, err := store.CountActiveServerAdmins(ctx, tx)
		if err != nil {
			return err
		}
		if n == 0 {
			return inputErr("服务器上至少要保留一名未停用的服务器管理员")
		}
		action := ActServerAdminRevoke
		if on {
			action = ActServerAdminGrant
		}
		return record(ctx, tx, act, store.AdminEvent{Action: action, TargetKind: "account", TargetID: idp(a.ID), TargetName: a.Name})
	})
}

// ServerAccounts 由服务器管理员列出账号。
func (s *Service) ServerAccounts(ctx context.Context, actor *store.Account, prefix string) ([]store.AccountSummary, error) {
	if err := requireServerAdmin(ctx, s.Pool, actor); err != nil {
		return nil, err
	}
	return store.ListAccountSummaries(ctx, s.Pool, prefix)
}

// AccountByName 按登录名返回账号。
func (s *Service) AccountByName(ctx context.Context, name string) (store.Account, error) {
	return store.AccountByName(ctx, s.Pool, name)
}

// ---- 工作空间 ----

// ServerWorkspaces 由服务器管理员列出全部工作空间。
func (s *Service) ServerWorkspaces(ctx context.Context, actor *store.Account) ([]store.WorkspaceSummary, error) {
	if err := requireServerAdmin(ctx, s.Pool, actor); err != nil {
		return nil, err
	}
	return store.ListWorkspaceSummaries(ctx, s.Pool)
}

// CreateWorkspace 由服务器管理员建工作空间，并指定首位管理员（登录名，空则为操作者自己）。
func (s *Service) CreateWorkspace(ctx context.Context, act Actor, name, adminName string) (store.Workspace, error) {
	return s.CreateWorkspaceNamed(ctx, act, name, "", adminName)
}

// CreateWorkspaceNamed 同 CreateWorkspace，并一并设置显示名（空则与路径相同）。
func (s *Service) CreateWorkspaceNamed(ctx context.Context, act Actor, name, display, adminName string) (store.Workspace, error) {
	actor := act.Account
	name = strings.TrimSpace(name)
	if err := checkMemberName(name); err != nil {
		return store.Workspace{}, inputErr("工作空间路径 %q 不合法：小写字母、数字、下划线、横线，1 到 32 个字符", name)
	}
	display, err := cleanWorkspaceDisplayName(display)
	if err != nil {
		return store.Workspace{}, err
	}
	if display == name {
		display = ""
	}
	var ws store.Workspace
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := requireServerAdmin(ctx, tx, actor); err != nil {
			return err
		}
		var err error
		ws, err = store.CreateWorkspace(ctx, tx, name)
		if isUniqueViolation(err) {
			return stateErr("工作空间 %s 已存在", name)
		}
		if err != nil {
			return err
		}
		if actor != nil {
			if err := store.SetWorkspaceCreator(ctx, tx, ws.ID, actor.ID); err != nil {
				return err
			}
		}
		ev := store.AdminEvent{WorkspaceID: idp(ws.ID), WorkspaceName: ws.Name, Action: ActWorkspaceCreate,
			TargetKind: "workspace", TargetID: idp(ws.ID), TargetName: ws.Name, Detail: map[string]any{}}
		if display != "" {
			if err := store.SetWorkspaceDisplayName(ctx, tx, ws.ID, display); err != nil {
				return err
			}
			ws.DisplayName = display
			ev.Detail["display_name"] = display
		}
		var admin store.Account
		switch {
		case strings.TrimSpace(adminName) != "":
			if admin, err = store.AccountByName(ctx, tx, strings.TrimSpace(adminName)); errors.Is(err, store.ErrNotFound) {
				return inputErr("没有登录名为 %s 的账号", adminName)
			} else if err != nil {
				return err
			}
		case actor != nil:
			admin = *actor
		default:
			return record(ctx, tx, act, ev)
		}
		if admin.Disabled() {
			return inputErr("账号 %s 已停用", admin.Name)
		}
		if _, err = store.AddMembership(ctx, tx, admin.ID, ws.ID, store.RoleAdmin, actorID(actor)); err != nil {
			return err
		}
		ev.Detail["admin"] = admin.Name
		return record(ctx, tx, act, ev)
	})
	return ws, err
}

// DeleteWorkspace 由服务器管理员删除没有任何问题单（含草稿）的工作空间。
func (s *Service) DeleteWorkspace(ctx context.Context, act Actor, name string) error {
	actor := act.Account
	return s.inTx(ctx, func(tx pgx.Tx) error {
		if err := requireServerAdmin(ctx, tx, actor); err != nil {
			return err
		}
		ws, err := store.WorkspaceByName(ctx, tx, name)
		if err != nil {
			return err
		}
		if _, err := store.LockWorkspace(ctx, tx, ws.ID); err != nil {
			return err
		}
		n, err := store.CountIssues(ctx, tx, ws.ID)
		if err != nil {
			return err
		}
		if n > 0 {
			return stateErr("工作空间 %s 里还有 %d 张问题单，不能删除", name, n)
		}
		if err := store.DeleteEmptyWorkspace(ctx, tx, ws.ID); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{WorkspaceID: idp(ws.ID), WorkspaceName: ws.Name,
			Action: ActWorkspaceDelete, TargetKind: "workspace", TargetID: idp(ws.ID), TargetName: ws.Name})
	})
}

// RecordBotPost 记下机器人发的审批消息。
func (s *Service) RecordBotPost(ctx context.Context, p store.BotPost) error {
	return store.InsertBotPost(ctx, s.Pool, p)
}

// BotPostByMessage 按通讯工具与消息编号找审批消息（群里点按钮时反查是哪条申请）。
func (s *Service) BotPostByMessage(ctx context.Context, provider, chatID, msgRef string) (store.BotPost, error) {
	return store.BotPostByMessage(ctx, s.Pool, provider, chatID, msgRef)
}

// BotPosts 列出某条申请的审批消息。
func (s *Service) BotPosts(ctx context.Context, kind string, refID int64) ([]store.BotPost, error) {
	return store.ListBotPosts(ctx, s.Pool, kind, refID)
}

// WorkspaceChats 返回工作空间绑定的群。
func (s *Service) WorkspaceChats(ctx context.Context, wsID int64) ([]store.WorkspaceChat, error) {
	return store.WorkspaceChats(ctx, s.Pool, wsID)
}
