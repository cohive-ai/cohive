package core

import (
	"context"
	"errors"
	"regexp"
	"strings"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// 关注者（CC-1）：抄送、自行关注。关注者收到这张单每个动作的告知（和原收件人在同一条群消息里，见 attachWatchers），
// 并且可以留言；关注与取消关注不改版本号、不写动态，与留言一样不打断别人基于版本的操作。

// resolveCC 把抄送的登录名解析成成员：名字规则同指派（assignable：昵称、重名返回 MemberChoiceError，不存在、停用报错）；
// 抄送自己、重复的忽略。
func resolveCC(ctx context.Context, q store.DBTX, who store.Identity, names []string) ([]store.Member, error) {
	var out []store.Member
	seen := map[int64]bool{who.MemberID: true}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		m, err := assignable(ctx, q, who.WorkspaceID, name)
		if err != nil {
			return nil, err
		}
		if !seen[m.ID] {
			seen[m.ID] = true
			out = append(out, m)
		}
	}
	return out, nil
}

func memberNames(ms []store.Member) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Name
	}
	return out
}

// addWatchers 把成员加为关注者，已经在册的忽略。
func addWatchers(ctx context.Context, q store.DBTX, issueID, addedBy int64, ms []store.Member) error {
	for _, m := range ms {
		if _, err := store.InsertWatcher(ctx, q, issueID, m.ID, addedBy); err != nil {
			return err
		}
	}
	return nil
}

// mentionRe 取留言里 @ 后面的一段字（含中文等文字）：前一个字符不能是字母、数字、下划线、点或 @（免得把邮箱 a@b.com 当成 @）。
// RE2 没有向后断言，前一个字符作为第一组一起匹配。
var mentionRe = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.@])@([\p{L}\p{N}_-]+)`)

// mentionNameRe 是 @ 后面那段字开头的登录名写法：中文里常紧接着写正文（“@bob看一下”），只取前面的登录名部分。
var mentionNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*`)

// mentionMaxRunes 是没识别的 @ 原样列出时的字数上限（昵称后面可能紧接着正文）。
const mentionMaxRunes = 32

// resolveMentions 解析留言里的 @。登录名（不区分大小写）对得上在册成员的进第一个返回值（去重、不含操作者本人）；
// 昵称、重名、不存在、已停用的都不通知，原样（含 @）记进第二个返回值（去重，按出现顺序）。@ 不加关注。
func resolveMentions(ctx context.Context, q store.DBTX, who store.Identity, note string) ([]int64, []string, error) {
	var ids []int64
	var unknown []string
	seenID, seenName := map[int64]bool{who.MemberID: true}, map[string]bool{}
	for _, mt := range mentionRe.FindAllStringSubmatch(note, -1) {
		written := mentionNameRe.FindString(mt[1])
		if written == "" || len(written) > 32 { // 不是登录名写法（如 @昵称）：不通知，原样列出
			written = mt[1]
			if r := []rune(written); len(r) > mentionMaxRunes {
				written = string(r[:mentionMaxRunes])
			}
			if !seenName[written] {
				seenName[written] = true
				unknown = append(unknown, "@"+written)
			}
			continue
		}
		key := strings.ToLower(written)
		if seenName[key] {
			continue
		}
		seenName[key] = true
		m, err := assignable(ctx, q, who.WorkspaceID, key)
		var ie *InputError
		var ce *MemberChoiceError
		switch {
		case err == nil:
			if !seenID[m.ID] {
				seenID[m.ID] = true
				ids = append(ids, m.ID)
			}
		case errors.As(err, &ie), errors.As(err, &ce):
			unknown = append(unknown, "@"+written)
		default:
			return nil, nil, err
		}
	}
	return ids, unknown, nil
}

// Watchers 返回问题单的有效关注者（成员在册且账号未停用），按关注时间排序；可见性同 Get。
func (s *Service) Watchers(ctx context.Context, who store.Identity, issueID int64) ([]store.Member, error) {
	is, err := store.GetIssue(ctx, s.Pool, issueID)
	if errors.Is(err, store.ErrNotFound) || (err == nil && !visible(who, is)) {
		return nil, notFoundIssue(issueID)
	}
	if err != nil {
		return nil, err
	}
	return store.ListWatchers(ctx, s.Pool, issueID)
}

// WatchInput 是关注 / 取消关注的参数。
type WatchInput struct {
	IssueID int64
	Members []string // 登录名；为空 = 操作者本人
	Watch   bool     // true 关注，false 取消
}

// SetWatch 关注或取消关注，返回之后的关注者。能看见这张单的成员可以关注、取消自己；
// 已关闭、已作废、已退回的单照样可以（之后还会有留言），归档的工作空间一律拒绝。
// 让别人关注（抄送）只有发起人、负责人；移除别人只有发起人与网页上账号登录的工作空间管理员。
// 取消一个本来没有关注的人静默成功。
func (s *Service) SetWatch(ctx context.Context, act Actor, who store.Identity, in WatchInput) ([]store.Member, error) {
	var out []store.Member
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
		targets := []store.Member{{ID: who.MemberID}}
		if len(in.Members) > 0 {
			targets = nil
			seen := map[int64]bool{}
			for _, name := range in.Members {
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				m, err := assignable(ctx, tx, who.WorkspaceID, name)
				if err != nil {
					return err
				}
				if !seen[m.ID] {
					seen[m.ID] = true
					targets = append(targets, m)
				}
			}
			if len(targets) == 0 {
				targets = []store.Member{{ID: who.MemberID}}
			}
		}
		isSource := is.SourceMemberID == who.MemberID
		isOwner := is.OwnerID != nil && *is.OwnerID == who.MemberID
		for _, m := range targets {
			if m.ID == who.MemberID {
				continue
			}
			switch {
			case in.Watch && !isSource && !isOwner:
				return inputErr("core.err.watch_add_forbidden")
			case !in.Watch && !isSource:
				if act.Account == nil || canAdmin(ctx, tx, act.Account, is.WorkspaceID) != nil {
					return inputErr("core.err.watch_remove_forbidden")
				}
			}
		}
		for _, m := range targets {
			if in.Watch {
				_, err = store.InsertWatcher(ctx, tx, is.ID, m.ID, who.MemberID)
			} else {
				err = store.DeleteWatcher(ctx, tx, is.ID, m.ID)
			}
			if err != nil {
				return err
			}
		}
		out, err = store.ListWatchers(ctx, tx, is.ID)
		return err
	})
	return out, err
}

// WatchPerm 说明 who 对这张单的关注权限，网页详情据此显示按钮；真正操作时在事务里重新判定。
type WatchPerm struct {
	Watching        bool // 已经在关注
	CanWatch        bool // 能关注 / 取消关注自己
	CanAdd          bool // 能让别人关注（抄送）：发起人、负责人
	CanRemoveOthers bool // 能移除别人的关注：发起人、账号登录的管理员
}

// WatchPerms 算出 who 对问题单的关注权限；account 为空表示不是管理员（编程工具、免密网页会话）。
// 归档的工作空间不能操作（Watching 仍如实给出）；出错时按没有权限处理。
func (s *Service) WatchPerms(ctx context.Context, account *store.Account, who store.Identity, is store.Issue) WatchPerm {
	var p WatchPerm
	if !visible(who, is) {
		return p
	}
	p.Watching, _ = store.IsWatcher(ctx, s.Pool, is.ID, who.MemberID)
	if archived, err := s.workspaceArchived(ctx, who.WorkspaceID); err != nil || archived {
		return p
	}
	isSource := is.SourceMemberID == who.MemberID
	p.CanWatch = true
	p.CanAdd = isSource || (is.OwnerID != nil && *is.OwnerID == who.MemberID)
	p.CanRemoveOthers = isSource || (account != nil && canAdmin(ctx, s.Pool, account, is.WorkspaceID) == nil)
	return p
}
