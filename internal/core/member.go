package core

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// MemberSearchLimit 是按名字查成员一次最多返回的条数。
const MemberSearchLimit = 20

// FindMembers 在调用方工作空间的在册、账号未停用的成员里按登录名、昵称检索（不区分大小写，
// 完全相同的在前，其次是包含检索词的）；检索词为空时列出全部。第二个返回值表示超出上限、还有没返回的。
func (s *Service) FindMembers(ctx context.Context, who store.Identity, query string) ([]store.MemberMatch, bool, error) {
	return store.FindMembers(ctx, s.Pool, who.WorkspaceID, strings.TrimSpace(query), MemberSearchLimit)
}

// 指派时名字对不上登录名、或登录名与别人的昵称撞了的错误码，是接口约定，接口与 MCP 的报错都带上。
const (
	CodeMemberNickname  = "member_nickname"  // 不是任何人的登录名，但与成员的昵称（或不同大小写的登录名）相同
	CodeMemberAmbiguous = "member_ambiguous" // 是某位成员的登录名，同时又是其他成员的昵称
)

// MemberChoiceError 表示指派用的名字不能确定是哪位成员，需要用户先确认，再用登录名重试。
// 不会凭昵称直接指派：昵称不唯一，还可能恰好等于另一位成员的登录名。
type MemberChoiceError struct {
	Code       string
	Name       string         // 调用方传入的名字
	Candidates []store.Member // 候选成员：登录名匹配的在前
}

func (e *MemberChoiceError) Error() string { return e.Localize(i18n.Default()) }

// ErrorCode 返回稳定的错误码（CodeMemberNickname 或 CodeMemberAmbiguous）。
func (e *MemberChoiceError) ErrorCode() string { return e.Code }

func (e *MemberChoiceError) Localize(l i18n.Lang) string {
	lines := make([]string, 0, len(e.Candidates))
	for _, m := range e.Candidates {
		if m.DisplayName == "" {
			lines = append(lines, l.T("core.member.candidate_plain", i18n.D{"Name": m.Name}))
		} else {
			lines = append(lines, l.T("core.member.candidate", i18n.D{"Name": m.Name, "Display": m.DisplayName}))
		}
	}
	key := "core.err.member_nickname"
	if e.Code == CodeMemberAmbiguous {
		key = "core.err.member_ambiguous"
	}
	return l.T(key, i18n.D{"Name": strconv.Quote(e.Name), "Candidates": strings.Join(lines, "\n")})
}

// assignable 返回可以指派的成员：本工作空间在册、账号未停用，且 name 是确定的登录名。
// name 只是昵称（或大小写不同的登录名）时，或登录名同时是别人的昵称时，返回 MemberChoiceError 列出候选。
func assignable(ctx context.Context, q store.DBTX, wsID int64, name string) (store.Member, error) {
	m, err := store.MemberByName(ctx, q, wsID, name)
	found := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return m, err
	}
	like, err := store.MembersNamedLike(ctx, q, wsID, name)
	if err != nil {
		return store.Member{}, err
	}
	var others []store.Member
	for _, o := range like {
		if !found || o.ID != m.ID {
			others = append(others, o)
		}
	}
	switch {
	case !found && len(others) > 0:
		return store.Member{}, &MemberChoiceError{Code: CodeMemberNickname, Name: name, Candidates: others}
	case !found:
		return store.Member{}, inputErr("core.err.member_not_found", i18n.D{"Name": strconv.Quote(name)})
	case len(others) > 0:
		cands := others
		if !m.AccountDisabled {
			cands = append([]store.Member{m}, others...)
		}
		return store.Member{}, &MemberChoiceError{Code: CodeMemberAmbiguous, Name: name, Candidates: cands}
	case m.AccountDisabled:
		return m, inputErr("core.err.member_disabled_assign", i18n.D{"Name": strconv.Quote(name)})
	}
	return m, nil
}
