package core

import (
	"context"

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

var boardNeeds = map[issue.Status]string{
	issue.StatusDraft:                "草稿，等发起人确认发出",
	issue.StatusOpen:                 "等负责人接单",
	issue.StatusAccepted:             "负责人已接单，尚未开始修复",
	issue.StatusFixing:               "负责人修复中",
	issue.StatusReopened:             "验证未通过，等负责人继续修复",
	issue.StatusReadyForVerification: "已修复，等发起人验证",
	issue.StatusDeclined:             "已被退回，等发起人决定下一步",
	issue.StatusClosed:               "已关闭",
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
	items := make([]BoardItem, 0, len(list))
	for _, is := range list {
		it := BoardItem{Issue: is, Needs: boardNeeds[is.Status]}
		if id, ok := Holder(is); ok {
			it.HolderID = &id
			it.HolderName = names[id]
		}
		items = append(items, it)
	}
	return members, items, nil
}
