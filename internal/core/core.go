// Package core 实现问题单的业务操作：每个操作在一个事务里完成
// 身份与可见性校验、版本校验、状态迁移、写事件与幂等记录。
//
// 加锁顺序：一个事务要锁多类行时，一律按 问题单（编号升序）→ 管理员行 → 成员行 的顺序。
// 问题单动作（Act）先锁问题单，写事件、通知时再按外键隐式引用成员行（FOR KEY SHARE），
// 所以凡是既锁成员行又要锁问题单的操作（转交、移出）都必须先把问题单锁齐。
// 成员行与管理员行用 FOR NO KEY UPDATE（见 store.LockMember），不挡外键引用。
// 万一仍被数据库判为死锁或串行化失败，inTx 会转成 RetryError，按版本冲突处理。
package core

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// EscalateReopenCount 是自动标记升级的重开次数。
const EscalateReopenCount = 3

// ActDraft 是起草事件的动作名，不属于状态机动作。
const ActDraft issue.Action = "draft"

type Service struct {
	Pool *pgxpool.Pool
	// DataDir 是服务器数据目录，保存初始管理员密码文件；只有服务端进程需要设置。
	DataDir string
	// EnvExternalURL 是环境变量 COHIVE_EXTERNAL_URL 给的对外地址（已规范化）；非空时锁定，网页与命令行不能改。
	EnvExternalURL string

	external atomic.Pointer[externalCache] // 进程内缓存的对外地址，见 settings.go
}

// 业务层的报错都是可本地化的：错误里只存文案键与参数，由接口、MCP、命令行在出口按调用方的语言渲染
// （i18n.Message 认任何实现了 Localize 的错误）；Error() 用本进程的缺省语言，只给日志和测试用。

// InputError 表示调用参数不完整或不合法。Key 非空时按语言渲染（Data 是模板参数），否则用 Msg。
type InputError struct {
	Msg  string
	Key  string
	Data i18n.D
}

func (e *InputError) Error() string { return e.Localize(i18n.Default()) }

// Localize 按语言渲染，供接口、MCP 按请求方的语言回报错。
func (e *InputError) Localize(l i18n.Lang) string { return localize(l, e.Msg, e.Key, e.Data) }

// inputErr 返回可按语言渲染的参数错误，key 是 core.json 里的文案键。
func inputErr(key string, data ...i18n.D) error {
	e := &InputError{Key: key}
	if len(data) > 0 {
		e.Data = data[0]
	}
	return e
}

// lk 是延迟翻译的文案键：放进错误的参数（i18n.D）里，渲染时才按渲染语言翻译成文字，
// 例如通讯工具的名字。只对本包的错误类型（InputError、StateError、HandoverError）有效。
type lk string

// localize 按语言渲染一条带参数的文案；key 为空时用 msg 原文。
func localize(l i18n.Lang, msg, key string, data i18n.D) string {
	if key == "" {
		return msg
	}
	return l.T(key, resolveData(l, data))
}

// resolveData 把参数里的 lk 翻译成目标语言的文字；没有 lk 时原样返回。
func resolveData(l i18n.Lang, data i18n.D) i18n.D {
	lazy := false
	for _, v := range data {
		if _, ok := v.(lk); ok {
			lazy = true
			break
		}
	}
	if !lazy {
		return data
	}
	out := make(i18n.D, len(data))
	for k, v := range data {
		if key, ok := v.(lk); ok {
			v = l.T(string(key))
		}
		out[k] = v
	}
	return out
}

// ConflictError 表示调用方基于旧版本操作。
type ConflictError struct {
	IssueID           int64
	Expected, Current int
}

func (e *ConflictError) Error() string { return e.Localize(i18n.Default()) }

func (e *ConflictError) Localize(l i18n.Lang) string {
	return l.T("core.err.version_conflict", i18n.D{"ID": e.IssueID, "Expected": e.Expected, "Current": e.Current})
}

func (e *ConflictError) Is(target error) bool { return target == store.ErrVersionConflict }

// RetryError 表示事务因并发被数据库中止（死锁 40P01、串行化失败 40001），本次操作没有生效。
// 与版本冲突一样处理：调用方重读最新内容后再决定是否重试。
type RetryError struct{ Code string }

func (e *RetryError) Error() string { return e.Localize(i18n.Default()) }

func (e *RetryError) Localize(l i18n.Lang) string { return l.T("core.err.retry") }

func (e *RetryError) Is(target error) bool { return target == store.ErrVersionConflict }

// retryable 把数据库因并发中止事务的错误转成 RetryError，其余原样返回。
func retryable(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) && (pe.Code == "40P01" || pe.Code == "40001") {
		return &RetryError{Code: pe.Code}
	}
	return err
}

func notFoundIssue(id int64) error {
	return i18n.Wrap(store.ErrNotFound, "core.err.issue_not_found", i18n.D{"ID": id})
}

// BriefMaxRunes 是群通知简述的字数上限。
const BriefMaxRunes = 80

type DraftInput struct {
	Title, Summary, Expected, Actual string
	Brief                            string // 群通知简述，发出时由人一并确认
	ReproScript, ReproEnv            string
	ReproExitCode                    *int
	Evidence, Repo, Branch, Commit   string
	TargetService                    string
	Owner                            string // 成员名；为空时按 TargetService 查服务负责人
	Severity                         string
	CC                               []string // 抄送：登录名；发出后也成为关注者
	IdempotencyKey                   string
}

type ActInput struct {
	IssueID         int64
	Action          issue.Action
	ExpectedVersion int
	Note            string
	Assignee        string   // 发出时补充负责人，或转交的接收人
	CC              []string // 抄送：登录名；只有发出（ActSubmit）能带
	IdempotencyKey  string
}

// ActInfo 是动作的附带结果：留言里没有认出的 @ 写法，原样列出（含 @，去重，按出现顺序），调用方据此提醒用户改用登录名。
type ActInfo struct {
	UnknownMentions []string
}

type InboxItem struct {
	store.Issue
	NeedsMe string
}

func (s *Service) inTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(tx); err != nil {
		return retryable(err)
	}
	return retryable(tx.Commit(ctx))
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func keyReuseErr(key string, rec store.IdemRecord) error {
	return inputErr("core.err.idem_key_reused", i18n.D{"Key": strconv.Quote(key), "ID": rec.IssueID, "Action": rec.Action})
}

// checkReplay 查幂等记录：记录与本次请求的问题单和动作一致时返回 replay=true；
// 不一致时报错，避免把无关的结果当成本次操作的结果。issueID 为 0 表示新建（起草）。
func checkReplay(ctx context.Context, q store.DBTX, who store.Identity, key string, issueID int64, action issue.Action) (int64, bool, error) {
	if key == "" {
		return 0, false, nil
	}
	rec, ok, err := store.GetIdempotent(ctx, q, who.TokenID, key)
	if err != nil || !ok {
		return 0, false, err
	}
	if rec.Action != string(action) || (issueID != 0 && rec.IssueID != issueID) {
		return 0, false, keyReuseErr(key, rec)
	}
	return rec.IssueID, true, nil
}

func severityOrDefault(v string) (string, error) {
	switch v {
	case "":
		return "normal", nil
	case "low", "normal", "high", "critical":
		return v, nil
	}
	return "", inputErr("core.err.severity_invalid")
}

// Draft 起草问题单，仅发起人可见，需确认后才发出。
func (s *Service) Draft(ctx context.Context, who store.Identity, in DraftInput) (store.Issue, error) {
	if strings.TrimSpace(in.Title) == "" {
		return store.Issue{}, inputErr("core.err.title_required")
	}
	// 入库前扫描密钥：在查幂等记录与开事务之前，被拒绝的请求什么都不写，脱敏后用同一个键重试可以成功
	if err := checkSecrets(draftFields(in)...); err != nil {
		return store.Issue{}, err
	}
	sev, err := severityOrDefault(in.Severity)
	if err != nil {
		return store.Issue{}, err
	}
	brief := strings.Join(strings.Fields(in.Brief), " ")
	if n := len([]rune(brief)); n > BriefMaxRunes {
		return store.Issue{}, inputErr("core.err.brief_too_long", i18n.D{"Max": BriefMaxRunes, "N": n})
	}
	replayDraft := func() (store.Issue, bool, error) {
		id, ok, err := checkReplay(ctx, s.Pool, who, in.IdempotencyKey, 0, ActDraft)
		if err != nil || !ok {
			return store.Issue{}, false, err
		}
		is, err := store.GetIssue(ctx, s.Pool, id)
		return is, err == nil, err
	}
	if is, ok, err := replayDraft(); err != nil || ok {
		return is, err
	}
	var out store.Issue
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := writable(ctx, tx, who.WorkspaceID); err != nil {
			return err
		}
		is := store.Issue{
			WorkspaceID: who.WorkspaceID, SourceMemberID: who.MemberID, SourceTool: who.Tool,
			TargetService: in.TargetService, Severity: sev, Title: in.Title, Brief: brief, Summary: in.Summary,
			Expected: in.Expected, Actual: in.Actual, ReproScript: in.ReproScript, ReproEnv: in.ReproEnv,
			ReproExitCode: in.ReproExitCode, Evidence: in.Evidence, Repo: in.Repo, Branch: in.Branch,
			CommitSHA: in.Commit, Status: issue.StatusDraft,
		}
		switch {
		case in.Owner != "":
			m, err := assignable(ctx, tx, who.WorkspaceID, in.Owner)
			if err != nil {
				return err
			}
			is.OwnerID = &m.ID
		case in.TargetService != "":
			m, err := store.ServiceOwner(ctx, tx, who.WorkspaceID, in.TargetService)
			if err == nil {
				is.OwnerID = &m.ID
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		cc, err := resolveCC(ctx, tx, who, in.CC)
		if err != nil {
			return err
		}
		if err := store.InsertIssue(ctx, tx, &is); err != nil {
			return err
		}
		var payload map[string]any
		if len(cc) > 0 {
			payload = map[string]any{"cc": memberNames(cc)}
		}
		if err := store.InsertEvent(ctx, tx, store.Event{IssueID: is.ID, ActorMemberID: who.MemberID,
			ActorTool: who.Tool, Action: ActDraft, ToStatus: issue.StatusDraft, Payload: payload}); err != nil {
			return err
		}
		if err := addWatchers(ctx, tx, is.ID, who.MemberID, cc); err != nil {
			return err
		}
		if in.IdempotencyKey != "" {
			if err := store.PutIdempotent(ctx, tx, who.TokenID, in.IdempotencyKey,
				store.IdemRecord{IssueID: is.ID, Action: string(ActDraft)}); err != nil {
				return err
			}
		}
		out, err = store.GetIssue(ctx, tx, is.ID)
		return err
	})
	if err != nil && in.IdempotencyKey != "" && isUniqueViolation(err) {
		// 同一个键的并发请求已先一步写入：按记录返回或报告键被挪用
		if is, ok, rerr := replayDraft(); rerr != nil || ok {
			return is, rerr
		}
	}
	return out, err
}

func visible(who store.Identity, is store.Issue) bool {
	if is.WorkspaceID != who.WorkspaceID {
		return false
	}
	return is.Status != issue.StatusDraft || is.SourceMemberID == who.MemberID
}

// Act 对问题单执行一个动作。
func (s *Service) Act(ctx context.Context, who store.Identity, in ActInput) (store.Issue, error) {
	is, _, err := s.ActDetail(ctx, who, in)
	return is, err
}

// ActDetail 同 Act，另返回留言里没有认出的 @（见 ActInfo）。
func (s *Service) ActDetail(ctx context.Context, who store.Identity, in ActInput) (store.Issue, ActInfo, error) {
	var info ActInfo
	// 评论只追加事件，不改状态、归属与正文：不比对也不改变版本号，
	// 免得人刚批准的发出、接单、关闭因为期间多了一条评论而版本冲突。
	comment := in.Action == issue.ActComment
	if !comment && in.ExpectedVersion <= 0 {
		return store.Issue{}, info, inputErr("core.err.expected_version_required")
	}
	if len(in.CC) > 0 && in.Action != issue.ActSubmit {
		return store.Issue{}, info, inputErr("core.err.cc_submit_only")
	}
	if err := checkSecrets(actFields(in)...); err != nil {
		return store.Issue{}, info, err
	}
	var out store.Issue
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := writable(ctx, tx, who.WorkspaceID); err != nil {
			return err
		}
		is, err := store.GetIssueForUpdate(ctx, tx, in.IssueID)
		if errors.Is(err, store.ErrNotFound) || (err == nil && !visible(who, is)) {
			return notFoundIssue(in.IssueID)
		}
		if err != nil {
			return err
		}
		// 拿到行锁之后再查幂等记录：同键的并发重试会在锁上排队，
		// 轮到时能看到首个请求已提交的记录，直接返回同样的结果。
		if _, ok, err := checkReplay(ctx, tx, who, in.IdempotencyKey, in.IssueID, in.Action); err != nil {
			return err
		} else if ok {
			out = is
			return nil
		}
		if !comment && is.Version != in.ExpectedVersion {
			return &ConflictError{IssueID: is.ID, Expected: in.ExpectedVersion, Current: is.Version}
		}
		roles := issue.Roles(is.SourceMemberID == who.MemberID,
			is.OwnerID != nil && *is.OwnerID == who.MemberID)
		if len(roles) == 0 && comment { // 关注者只能留言；其他动作不必查，没有角色本来就拒绝
			if w, err := store.IsWatcher(ctx, tx, is.ID, who.MemberID); err != nil {
				return err
			} else if w {
				roles = []issue.Role{issue.RoleWatcher}
			}
		}
		from := is.Status
		to, err := issue.Apply(from, in.Action, roles)
		if err != nil {
			return err
		}

		payload := map[string]any{}
		if (in.Action == issue.ActDecline || in.Action == issue.ActTransfer) && strings.TrimSpace(in.Note) == "" {
			return inputErr("core.err.note_required")
		}
		if in.Action == issue.ActCancel && strings.TrimSpace(in.Note) == "" {
			return inputErr("core.err.cancel_reason_required")
		}
		var cc []store.Member
		var mentions []int64
		switch in.Action {
		case issue.ActSubmit:
			if cc, err = resolveCC(ctx, tx, who, in.CC); err != nil {
				return err
			}
			if in.Assignee != "" {
				m, err := assignable(ctx, tx, who.WorkspaceID, in.Assignee)
				if err != nil {
					return err
				}
				is.OwnerID = &m.ID
				payload["assignee"] = m.Name
			}
			if is.OwnerID == nil {
				return inputErr("core.err.owner_missing", i18n.D{"Service": strconv.Quote(is.TargetService)})
			}
			if len(cc) > 0 {
				payload["cc"] = memberNames(cc)
			}
		case issue.ActComment:
			if from != issue.StatusDraft { // 草稿只有发起人看得见，不通知也不 @
				if mentions, info.UnknownMentions, err = resolveMentions(ctx, tx, who, in.Note); err != nil {
					return err
				}
			}
		case issue.ActTransfer:
			if in.Assignee == "" {
				return inputErr("core.err.transfer_assignee_required")
			}
			m, err := assignable(ctx, tx, who.WorkspaceID, in.Assignee)
			if err != nil {
				return err
			}
			if m.ID == *is.OwnerID {
				return inputErr("core.err.transfer_same_owner")
			}
			is.OwnerID = &m.ID
			payload["assignee"] = m.Name
		case issue.ActReopen:
			is.ReopenCount++
			if is.ReopenCount >= EscalateReopenCount && is.EscalatedAt == nil {
				now := time.Now()
				is.EscalatedAt = &now
				// 升级原因存进数据库，用服务器缺省语言写
				is.EscalationReason = i18n.T("core.escalation.reopen_count", i18n.D{"Count": is.ReopenCount})
				payload["escalated"] = true
			}
		}

		is.Status = to
		if comment {
			err = store.TouchIssue(ctx, tx, is.ID)
		} else {
			err = store.UpdateIssue(ctx, tx, &is)
		}
		if err != nil {
			return err
		}
		if err := store.InsertEvent(ctx, tx, store.Event{IssueID: is.ID, ActorMemberID: who.MemberID,
			ActorTool: who.Tool, Action: in.Action, FromStatus: from, ToStatus: to,
			Note: in.Note, Payload: payload}); err != nil {
			return err
		}
		if err := addWatchers(ctx, tx, is.ID, who.MemberID, cc); err != nil {
			return err
		}
		items := fanout(who, is, in.Action, payload["escalated"] == true)
		if from != issue.StatusDraft || in.Action == issue.ActSubmit { // 草稿上的留言不通知任何人（含关注者）
			watchers, err := store.WatcherIDs(ctx, tx, is.ID)
			if err != nil {
				return err
			}
			items = attachWatchers(who, items, actEvent(in.Action), watchers, mentions)
		}
		if err := queueNotices(ctx, tx, who, is, items); err != nil {
			return err
		}
		if in.IdempotencyKey != "" {
			if err := store.PutIdempotent(ctx, tx, who.TokenID, in.IdempotencyKey,
				store.IdemRecord{IssueID: is.ID, Action: string(in.Action)}); err != nil {
				return err
			}
		}
		out, err = store.GetIssue(ctx, tx, is.ID)
		return err
	})
	if err != nil && in.IdempotencyKey != "" && isUniqueViolation(err) {
		// 同一个键刚被并发用于另一张问题单
		if rec, ok, rerr := store.GetIdempotent(ctx, s.Pool, who.TokenID, in.IdempotencyKey); rerr == nil && ok {
			return store.Issue{}, info, keyReuseErr(in.IdempotencyKey, rec)
		}
	}
	return out, info, err
}

// Get 返回问题单与全部事件。
func (s *Service) Get(ctx context.Context, who store.Identity, id int64) (store.Issue, []store.Event, error) {
	is, err := store.GetIssue(ctx, s.Pool, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !visible(who, is)) {
		return store.Issue{}, nil, notFoundIssue(id)
	}
	if err != nil {
		return store.Issue{}, nil, err
	}
	evs, err := store.ListEvents(ctx, s.Pool, id)
	return is, evs, err
}

// needsMe 是“现在要你做什么”的一句话，按语言l渲染。
func needsMe(l i18n.Lang, who store.Identity, is store.Issue) string {
	if is.SourceMemberID == who.MemberID {
		switch is.Status {
		case issue.StatusDraft:
			return l.T("core.needs.draft")
		case issue.StatusReadyForVerification:
			return l.T("core.needs.verify")
		case issue.StatusDeclined:
			return l.T("core.needs.declined")
		}
	}
	switch is.Status {
	case issue.StatusOpen:
		return l.T("core.needs.open")
	case issue.StatusAccepted:
		return l.T("core.needs.accepted")
	case issue.StatusFixing:
		return l.T("core.needs.fixing")
	case issue.StatusReopened:
		return l.T("core.needs.reopened")
	}
	return ""
}

// Inbox 返回需要操作者处理的问题单。
func (s *Service) Inbox(ctx context.Context, who store.Identity) ([]InboxItem, error) {
	// 已归档的工作空间没有“待我处理”：归档后不催办、不提醒，收件箱也是空的
	if archived, err := s.workspaceArchived(ctx, who.WorkspaceID); err != nil || archived {
		return []InboxItem{}, err
	}
	list, err := store.ListInbox(ctx, s.Pool, who.MemberID)
	if err != nil {
		return nil, err
	}
	l := i18n.FromContext(ctx)
	out := make([]InboxItem, 0, len(list))
	for _, is := range list {
		if is.WorkspaceID != who.WorkspaceID {
			continue
		}
		item := InboxItem{Issue: is, NeedsMe: needsMe(l, who, is)}
		if is.Status == issue.StatusDeclined {
			reason, err := s.lastNote(ctx, is.ID, issue.ActDecline)
			if err != nil {
				return nil, err
			}
			item.NeedsMe = l.T("core.needs.declined_reason", i18n.D{"Reason": reason})
		}
		out = append(out, item)
	}
	return out, nil
}

// lastNote 返回某张问题单最近一次指定动作的备注。
func (s *Service) lastNote(ctx context.Context, issueID int64, act issue.Action) (string, error) {
	evs, err := store.ListEvents(ctx, s.Pool, issueID)
	if err != nil {
		return "", err
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Action == act {
			return evs[i].Note, nil
		}
	}
	return "", nil
}

// Search 查找相似问题单，用于起草前查重。
func (s *Service) Search(ctx context.Context, who store.Identity, query, service string) ([]store.Issue, error) {
	return store.SearchIssues(ctx, s.Pool, who.WorkspaceID, who.MemberID, query, service, 20)
}

// List 供看板列表使用，返回最近更新的至多 50 张。
func (s *Service) List(ctx context.Context, who store.Identity, query, service string) ([]store.Issue, error) {
	return store.SearchIssues(ctx, s.Pool, who.WorkspaceID, who.MemberID, query, service, 50)
}

// WhoOwns 查询服务的负责人。
func (s *Service) WhoOwns(ctx context.Context, who store.Identity, service string) (store.Member, error) {
	if service == "" {
		return store.Member{}, inputErr("core.err.service_required")
	}
	m, err := store.ServiceOwner(ctx, s.Pool, who.WorkspaceID, service)
	if errors.Is(err, store.ErrNotFound) {
		return m, inputErr("core.err.service_no_owner", i18n.D{"Name": strconv.Quote(service)})
	}
	return m, err
}
