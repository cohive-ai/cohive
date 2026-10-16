// Package issue 定义问题单的状态、动作与迁移规则，不依赖存储。
package issue

import (
	"fmt"
	"strings"
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

var statusLabel = map[Status]string{
	StatusDraft:                "草稿",
	StatusOpen:                 "待接单",
	StatusAccepted:             "已接单",
	StatusFixing:               "修复中",
	StatusReadyForVerification: "待验证",
	StatusReopened:             "已重开",
	StatusClosed:               "已关闭",
	StatusDeclined:             "已退回",
}

// Label 返回状态的中文名。
func (s Status) Label() string {
	if l, ok := statusLabel[s]; ok {
		return l
	}
	return string(s)
}

// ParseStatus 校验并转换数据库中的状态值。
func ParseStatus(v string) (Status, error) {
	s := Status(v)
	if _, ok := statusLabel[s]; !ok {
		return "", fmt.Errorf("未知的问题单状态 %q", v)
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

func (r Role) label() string {
	if r == RoleSource {
		return "发起人"
	}
	return "负责人"
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
	From    Status
	Action  Action
	Reason  string
	Allowed []Action
}

func (e *TransitionError) Error() string {
	allowed := make([]string, len(e.Allowed))
	for i, a := range e.Allowed {
		allowed[i] = string(a)
	}
	list := "无"
	if len(allowed) > 0 {
		list = strings.Join(allowed, "、")
	}
	return fmt.Sprintf("问题单当前状态为“%s”（%s），不能执行 %s：%s。你当前允许的动作：%s",
		e.From.Label(), e.From, e.Action, e.Reason, list)
}

func hasRole(roles []Role, r Role) bool {
	for _, x := range roles {
		if x == r {
			return true
		}
	}
	return false
}

func check(from Status, act Action, roles []Role) (Status, string) {
	if act == ActComment {
		if from == StatusDraft {
			if hasRole(roles, RoleSource) {
				return from, ""
			}
			return "", "草稿只有发起人可以修改"
		}
		if len(roles) > 0 {
			return from, ""
		}
		return "", "只有发起人或负责人可以评论"
	}
	r, ok := rules[act]
	if !ok {
		return "", "未知动作"
	}
	inFrom := false
	for _, s := range r.from {
		if s == from {
			inFrom = true
			break
		}
	}
	if !inFrom {
		names := make([]string, len(r.from))
		for i, s := range r.from {
			names[i] = s.Label()
		}
		return "", fmt.Sprintf("该动作只能在“%s”状态下执行", strings.Join(names, "”或“"))
	}
	if !hasRole(roles, r.role) {
		return "", fmt.Sprintf("只有%s可以执行", r.role.label())
	}
	if r.to == "" {
		return from, ""
	}
	return r.to, ""
}

// Apply 计算执行动作后的状态。不允许时返回 *TransitionError。
func Apply(from Status, act Action, roles []Role) (Status, error) {
	to, reason := check(from, act, roles)
	if reason != "" {
		return "", &TransitionError{From: from, Action: act, Reason: reason, Allowed: Allowed(from, roles)}
	}
	return to, nil
}

// Allowed 返回给定角色在当前状态下可执行的动作。
func Allowed(from Status, roles []Role) []Action {
	var out []Action
	for _, a := range actionOrder {
		if _, reason := check(from, a, roles); reason == "" {
			out = append(out, a)
		}
	}
	return out
}
