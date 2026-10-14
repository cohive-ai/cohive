// Package core 实现问题单的业务操作：每个操作在一个事务里完成
// 身份与可见性校验、版本校验、状态迁移、写事件与幂等记录。
package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
}

// InputError 表示调用参数不完整或不合法。
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func inputErr(format string, args ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, args...)}
}

// ConflictError 表示调用方基于旧版本操作。
type ConflictError struct {
	IssueID           int64
	Expected, Current int
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("问题单 #%d 的版本已变化：你提供的是 %d，当前是 %d。请先读取最新内容再决定是否继续。",
		e.IssueID, e.Expected, e.Current)
}

func (e *ConflictError) Is(target error) bool { return target == store.ErrVersionConflict }

func notFoundIssue(id int64) error {
	return fmt.Errorf("问题单 #%d 不存在或你无权查看：%w", id, store.ErrNotFound)
}

type DraftInput struct {
	Title, Summary, Expected, Actual string
	ReproScript, ReproEnv            string
	ReproExitCode                    *int
	Evidence, Repo, Branch, Commit   string
	TargetService                    string
	Owner                            string // 成员名；为空时按 TargetService 查服务负责人
	Severity                         string
	IdempotencyKey                   string
}

type ActInput struct {
	IssueID         int64
	Action          issue.Action
	ExpectedVersion int
	Note            string
	Assignee        string // 发出时补充负责人，或转交的接收人
	IdempotencyKey  string
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
		return err
	}
	return tx.Commit(ctx)
}

func isUniqueViolation(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "23505"
}

func keyReuseErr(key string, rec store.IdemRecord) error {
	return inputErr("幂等键 %q 已用于问题单 #%d 的 %s 操作，不能用于本次请求，请换一个新的键", key, rec.IssueID, rec.Action)
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
	return "", inputErr("severity 只能是 low、normal、high、critical 之一")
}

// Draft 起草问题单，仅发起人可见，需确认后才发出。
func (s *Service) Draft(ctx context.Context, who store.Identity, in DraftInput) (store.Issue, error) {
	if strings.TrimSpace(in.Title) == "" {
		return store.Issue{}, inputErr("title 不能为空")
	}
	sev, err := severityOrDefault(in.Severity)
	if err != nil {
		return store.Issue{}, err
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
		is := store.Issue{
			WorkspaceID: who.WorkspaceID, SourceMemberID: who.MemberID, SourceTool: who.Tool,
			TargetService: in.TargetService, Severity: sev, Title: in.Title, Summary: in.Summary,
			Expected: in.Expected, Actual: in.Actual, ReproScript: in.ReproScript, ReproEnv: in.ReproEnv,
			ReproExitCode: in.ReproExitCode, Evidence: in.Evidence, Repo: in.Repo, Branch: in.Branch,
			CommitSHA: in.Commit, Status: issue.StatusDraft,
		}
		switch {
		case in.Owner != "":
			m, err := store.MemberByName(ctx, tx, who.WorkspaceID, in.Owner)
			if err != nil {
				return inputErr("成员 %q 不存在", in.Owner)
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
		if err := store.InsertIssue(ctx, tx, &is); err != nil {
			return err
		}
		if err := store.InsertEvent(ctx, tx, store.Event{IssueID: is.ID, ActorMemberID: who.MemberID,
			ActorTool: who.Tool, Action: ActDraft, ToStatus: issue.StatusDraft}); err != nil {
			return err
		}
		if in.IdempotencyKey != "" {
			if err := store.PutIdempotent(ctx, tx, who.TokenID, in.IdempotencyKey,
				store.IdemRecord{IssueID: is.ID, Action: string(ActDraft)}); err != nil {
				return err
			}
		}
		var err error
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
	if in.ExpectedVersion <= 0 {
		return store.Issue{}, inputErr("必须提供 expected_version（问题单当前版本号），可先读取问题单获得")
	}
	var out store.Issue
	err := s.inTx(ctx, func(tx pgx.Tx) error {
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
		if is.Version != in.ExpectedVersion {
			return &ConflictError{IssueID: is.ID, Expected: in.ExpectedVersion, Current: is.Version}
		}
		roles := issue.Roles(is.SourceMemberID == who.MemberID,
			is.OwnerID != nil && *is.OwnerID == who.MemberID)
		from := is.Status
		to, err := issue.Apply(from, in.Action, roles)
		if err != nil {
			return err
		}

		payload := map[string]any{}
		if (in.Action == issue.ActDecline || in.Action == issue.ActTransfer) && strings.TrimSpace(in.Note) == "" {
			return inputErr("退回或转交必须在 note 中说明理由，发起人会看到")
		}
		switch in.Action {
		case issue.ActSubmit:
			if in.Assignee != "" {
				m, err := store.MemberByName(ctx, tx, who.WorkspaceID, in.Assignee)
				if err != nil {
					return inputErr("成员 %q 不存在", in.Assignee)
				}
				is.OwnerID = &m.ID
				payload["assignee"] = m.Name
			}
			if is.OwnerID == nil {
				return inputErr("问题单还没有负责人：请在 assignee 中指定成员，或先登记服务 %q 的负责人", is.TargetService)
			}
		case issue.ActTransfer:
			if in.Assignee == "" {
				return inputErr("转交必须在 assignee 中指定接收人")
			}
			m, err := store.MemberByName(ctx, tx, who.WorkspaceID, in.Assignee)
			if err != nil {
				return inputErr("成员 %q 不存在", in.Assignee)
			}
			if m.ID == *is.OwnerID {
				return inputErr("接收人与当前负责人相同")
			}
			is.OwnerID = &m.ID
			payload["assignee"] = m.Name
		case issue.ActReopen:
			is.ReopenCount++
			if is.ReopenCount >= EscalateReopenCount && is.EscalatedAt == nil {
				now := time.Now()
				is.EscalatedAt = &now
				is.EscalationReason = fmt.Sprintf("重开次数达到 %d 次", is.ReopenCount)
				payload["escalated"] = true
			}
		}

		is.Status = to
		if err := store.UpdateIssue(ctx, tx, &is); err != nil {
			return err
		}
		if err := store.InsertEvent(ctx, tx, store.Event{IssueID: is.ID, ActorMemberID: who.MemberID,
			ActorTool: who.Tool, Action: in.Action, FromStatus: from, ToStatus: to,
			Note: in.Note, Payload: payload}); err != nil {
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
			return store.Issue{}, keyReuseErr(in.IdempotencyKey, rec)
		}
	}
	return out, err
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

func needsMe(who store.Identity, is store.Issue) string {
	if is.SourceMemberID == who.MemberID {
		switch is.Status {
		case issue.StatusDraft:
			return "草稿待你确认发出"
		case issue.StatusReadyForVerification:
			return "待你验证并确认关闭或重开"
		case issue.StatusDeclined:
			return "已被负责人退回，请看理由后决定改派他人、补充信息重新起草，或放弃"
		}
	}
	switch is.Status {
	case issue.StatusOpen:
		return "待你接单、退回或转交"
	case issue.StatusAccepted:
		return "你已接单，待开始修复"
	case issue.StatusFixing:
		return "你正在修复"
	case issue.StatusReopened:
		return "发起人已重开，待你继续修复"
	}
	return ""
}

// Inbox 返回需要操作者处理的问题单。
func (s *Service) Inbox(ctx context.Context, who store.Identity) ([]InboxItem, error) {
	list, err := store.ListInbox(ctx, s.Pool, who.MemberID)
	if err != nil {
		return nil, err
	}
	out := make([]InboxItem, 0, len(list))
	for _, is := range list {
		if is.WorkspaceID != who.WorkspaceID {
			continue
		}
		item := InboxItem{Issue: is, NeedsMe: needsMe(who, is)}
		if is.Status == issue.StatusDeclined {
			reason, err := s.lastNote(ctx, is.ID, issue.ActDecline)
			if err != nil {
				return nil, err
			}
			item.NeedsMe = "已被负责人退回，理由：" + reason + "。请决定改派他人、补充信息重新起草，或放弃"
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

// WhoOwns 查询服务的负责人。
func (s *Service) WhoOwns(ctx context.Context, who store.Identity, service string) (store.Member, error) {
	if service == "" {
		return store.Member{}, inputErr("service 不能为空")
	}
	m, err := store.ServiceOwner(ctx, s.Pool, who.WorkspaceID, service)
	if errors.Is(err, store.ErrNotFound) {
		return m, inputErr("服务 %q 未登记负责人，请向工作空间管理员确认，或在发出时直接指定负责人", service)
	}
	return m, err
}
