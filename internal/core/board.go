package core

import (
	"context"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
)

// BoardItem 是看板上的一张问题单，附带“现在等谁处理”。
type BoardItem struct {
	store.Issue
	HolderID   *int64
	HolderName string
	Needs      string
}

// Holder 返回当前需要采取行动的成员：草稿、待验证、已退回等发起人；
// 待接单、已接单、修复中、已重开等负责人；已关闭没有处理人。
func Holder(is store.Issue) (int64, bool) {
	switch is.Status {
	case issue.StatusDraft, issue.StatusReadyForVerification, issue.StatusDeclined:
		return is.SourceMemberID, true
	case issue.StatusOpen, issue.StatusAccepted, issue.StatusFixing, issue.StatusReopened:
		if is.OwnerID != nil {
			return *is.OwnerID, true
		}
		return is.SourceMemberID, true
	}
	return 0, false
}

// boardNeeds 是各状态在看板上“现在等谁处理”的文案键。
var boardNeeds = map[issue.Status]string{
	issue.StatusDraft:                "core.board.needs_draft",
	issue.StatusOpen:                 "core.board.needs_open",
	issue.StatusAccepted:             "core.board.needs_accepted",
	issue.StatusFixing:               "core.board.needs_fixing",
	issue.StatusReopened:             "core.board.needs_reopened",
	issue.StatusReadyForVerification: "core.board.needs_ready_for_verification",
	issue.StatusDeclined:             "core.board.needs_declined",
	issue.StatusClosed:               "core.board.needs_closed",
}

// Board 返回工作空间成员与看板上的问题单。
func (s *Service) Board(ctx context.Context, who store.Identity) ([]store.Member, []BoardItem, error) {
	members, err := store.ListMembers(ctx, s.Pool, who.WorkspaceID)
	if err != nil {
		return nil, nil, err
	}
	names := make(map[int64]string, len(members))
	for _, m := range members {
		names[m.ID] = m.Name
	}
	list, err := store.ListBoard(ctx, s.Pool, who.WorkspaceID, who.MemberID)
	if err != nil {
		return nil, nil, err
	}
	l := i18n.FromContext(ctx)
	items := make([]BoardItem, 0, len(list))
	for _, is := range list {
		it := BoardItem{Issue: is}
		if key, ok := boardNeeds[is.Status]; ok {
			it.Needs = l.T(key)
		}
		if id, ok := Holder(is); ok {
			it.HolderID = &id
			it.HolderName = names[id]
		}
		items = append(items, it)
	}
	return members, items, nil
}
