// Package issue 定义问题单的状态、动作与迁移规则，不依赖存储。
package issue

import (
	"fmt"
	"strings"

	"github.com/cohive-ai/cohive/internal/i18n"
)

// Status 是问题单的具体状态。
type Status string

const (
	StatusDraft                Status = "DRAFT"
	StatusOpen                 Status = "OPEN"
	StatusAccepted             Status = "ACCEPTED"
	StatusFixing               Status = "FIXING"
	StatusReadyForVerification Status = "READY_FOR_VERIFICATION"
	StatusReopened             Status = "REOPENED"
	StatusClosed               Status = "CLOSED"
	StatusDeclined             Status = "DECLINED"
)

// statusKey 是各状态名称在语言包里的文案键（新增状态时必须补齐，测试会遍历检查）。
var statusKey = map[Status]string{
	StatusDraft:                "issue.status.draft",
	StatusOpen:                 "issue.status.open",
	StatusAccepted:             "issue.status.accepted",
	StatusFixing:               "issue.status.fixing",
	StatusReadyForVerification: "issue.status.ready_for_verification",
	StatusReopened:             "issue.status.reopened",
	StatusClosed:               "issue.status.closed",
	StatusDeclined:             "issue.status.declined",
}

// StatusLabelIn 返回状态按指定语言的名称；未知状态原样返回。
func StatusLabelIn(l i18n.Lang, s Status) string {
	if k, ok := statusKey[s]; ok {
		return l.T(k)
	}
	return string(s)
}

// ParseStatus 校验并转换数据库中的状态值。
func ParseStatus(v string) (Status, error) {
	s := Status(v)
	if _, ok := statusKey[s]; !ok {
		return "", fmt.Errorf("unknown issue status %q", v)
	}
	return s, nil
}

// Category 是状态的大类，收件箱与路由按类别工作。
type Category string

const (
	CategoryDraft      Category = "draft"
	CategoryInProgress Category = "in_progress"
	CategoryDone       Category = "done"
)

// Category 返回状态所属的大类。
func (s Status) Category() Category {
	switch s {
	case StatusDraft:
		return CategoryDraft
	case StatusClosed, StatusDeclined:
		return CategoryDone
	default:
		return CategoryInProgress
	}
}

// Action 是可以对问题单执行的动作。
type Action string

const (
	ActSubmit              Action = "submit"
	ActAccept              Action = "accept"
	ActDecline             Action = "decline"
	ActTransfer            Action = "transfer"
	ActStart               Action = "start"
	ActRequestVerification Action = "request_verification"
	ActClose               Action = "close"
	ActReopen              Action = "reopen"
	ActComment             Action = "comment"
	// ActReassign 是管理员改派（移出成员时转交），不属于状态机动作，不经编程工具，状态不变。
	ActReassign Action = "reassign"
)

// Role 是操作者相对于某张问题单的角色。
type Role int

const (
	RoleSource Role = iota // 发起人
	RoleOwner              // 负责人
)

func (r Role) labelIn(l i18n.Lang) string {
	if r == RoleSource {
		return l.T("issue.role.source")
	}
	return l.T("issue.role.owner")
}

// Roles 根据操作者是否为发起人、负责人得到角色列表。
func Roles(isSource, isOwner bool) []Role {
	var rs []Role
	if isSource {
		rs = append(rs, RoleSource)
	}
	if isOwner {
		rs = append(rs, RoleOwner)
	}
	return rs
}

type rule struct {
	from []Status
	to   Status // 空表示状态不变
	role Role
}

var rules = map[Action]rule{
	ActSubmit:              {from: []Status{StatusDraft}, to: StatusOpen, role: RoleSource},
	ActAccept:              {from: []Status{StatusOpen}, to: StatusAccepted, role: RoleOwner},
	ActDecline:             {from: []Status{StatusOpen}, to: StatusDeclined, role: RoleOwner},
	ActTransfer:            {from: []Status{StatusOpen}, to: "", role: RoleOwner},
	ActStart:               {from: []Status{StatusAccepted, StatusReopened}, to: StatusFixing, role: RoleOwner},
	ActRequestVerification: {from: []Status{StatusAccepted, StatusFixing, StatusReopened}, to: StatusReadyForVerification, role: RoleOwner},
	ActClose:               {from: []Status{StatusReadyForVerification}, to: StatusClosed, role: RoleSource},
	ActReopen:              {from: []Status{StatusReadyForVerification}, to: StatusReopened, role: RoleSource},
}

// 动作的固定展示顺序，保证错误信息稳定。
var actionOrder = []Action{
	ActSubmit, ActAccept, ActDecline, ActTransfer, ActStart,
	ActRequestVerification, ActClose, ActReopen, ActComment,
}

// TransitionError 说明为什么动作不能执行，以及当前允许做什么。
type TransitionError struct {
	From   Status
	Action Action
	// Reason 是中文的原因（固定中文，保留给旧调用方）；按语言展示用 Localize。
	Reason  string
	Allowed []Action

	reason reason
}

// Error 用本进程的缺省语言渲染；接口层和 MCP 用 Localize 按请求或账号的语言展示。
func (e *TransitionError) Error() string { return e.Localize(i18n.Default()) }

// Localize 按指定语言渲染完整的说明。与 *i18n.Error 一样有 Localize(Lang) string，
// 接口层和 MCP 据此按请求或账号的语言展示。
func (e *TransitionError) Localize(l i18n.Lang) string {
	allowed := make([]string, len(e.Allowed))
	for i, a := range e.Allowed {
		allowed[i] = string(a)
	}
	list := l.T("issue.transition.none")
	if len(allowed) > 0 {
		list = strings.Join(allowed, l.T("issue.list_sep"))
	}
	return l.T("issue.transition.error", i18n.D{
		"Label": StatusLabelIn(l, e.From), "Status": string(e.From), "Action": string(e.Action),
		"Reason": e.reason.in(l), "Allowed": list,
	})
}

// reason 是不能执行的原因：文案键加参数，按语言展示时才渲染。
type reason struct {
	key      string
	statuses []Status // wrong_status：允许的状态
	role     Role     // role_only：需要的角色
}

func (r reason) in(l i18n.Lang) string {
	d := i18n.D{}
	switch r.key {
	case "issue.reason.wrong_status":
		names := make([]string, len(r.statuses))
		for i, s := range r.statuses {
			names[i] = StatusLabelIn(l, s)
		}
		d["Statuses"] = strings.Join(names, l.T("issue.reason.status_join"))
	case "issue.reason.role_only":
		d["Role"] = r.role.labelIn(l)
	}
	return l.T(r.key, d)
}

func hasRole(roles []Role, r Role) bool {
	for _, x := range roles {
		if x == r {
			return true
		}
	}
	return false
}

// check 返回执行动作后的状态；不允许时 reason.key 非空。
func check(from Status, act Action, roles []Role) (Status, reason) {
	if act == ActComment {
		if from == StatusDraft {
			if hasRole(roles, RoleSource) {
				return from, reason{}
			}
			return "", reason{key: "issue.reason.draft_source_only"}
		}
		if len(roles) > 0 {
			return from, reason{}
		}
		return "", reason{key: "issue.reason.comment_roles"}
	}
	r, ok := rules[act]
	if !ok {
		return "", reason{key: "issue.reason.unknown_action"}
	}
	inFrom := false
	for _, s := range r.from {
		if s == from {
			inFrom = true
			break
		}
	}
	if !inFrom {
		return "", reason{key: "issue.reason.wrong_status", statuses: r.from}
	}
	if !hasRole(roles, r.role) {
		return "", reason{key: "issue.reason.role_only", role: r.role}
	}
	if r.to == "" {
		return from, reason{}
	}
	return r.to, reason{}
}

// Apply 计算执行动作后的状态。不允许时返回 *TransitionError。
func Apply(from Status, act Action, roles []Role) (Status, error) {
	to, why := check(from, act, roles)
	if why.key != "" {
		return "", &TransitionError{From: from, Action: act, Reason: why.in(i18n.ZhCN), Allowed: Allowed(from, roles), reason: why}
	}
	return to, nil
}

// Allowed 返回给定角色在当前状态下可执行的动作。
func Allowed(from Status, roles []Role) []Action {
	var out []Action
	for _, a := range actionOrder {
		if _, why := check(from, a, roles); why.key == "" {
			out = append(out, a)
		}
	}
	return out
}
