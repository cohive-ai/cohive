package core

import (
	"context"
	"errors"

	"github.com/cohive-ai/cohive/internal/store"
)

// 权限判定（设计稿第 1.3 节）。所有管理入口共用这几个函数，且在事务里按库里的最新状态判定，
// 不信请求开始时读到的角色。actor 为空表示服务器命令行，视为最高权限。

var (
	// ErrNotAdmin 表示操作者不是该工作空间的管理员。
	ErrNotAdmin = errors.New("只有工作空间管理员能执行这个操作")
	// ErrNotServerAdmin 表示操作者不是服务器管理员。
	ErrNotServerAdmin = errors.New("只有服务器管理员能执行这个操作")
	// ErrNotManager 表示操作者管不到这个人（他不在操作者管理的任何工作空间里在册）。
	ErrNotManager = errors.New("只有此人所在工作空间的管理员能执行这个操作")
)

// freshActor 重新读取操作者账号；已停用的视为无权。
func freshActor(ctx context.Context, q store.DBTX, actor *store.Account) (*store.Account, error) {
	if actor == nil {
		return nil, nil
	}
	a, err := store.AccountByID(ctx, q, actor.ID)
	if err != nil {
		return nil, err
	}
	if a.Disabled() {
		return nil, store.ErrAccountDisabled
	}
	return &a, nil
}

// canAdmin：操作者未停用，且是服务器管理员，或是 ws 在册的管理员。
func canAdmin(ctx context.Context, q store.DBTX, actor *store.Account, wsID int64) error {
	a, err := freshActor(ctx, q, actor)
	if err != nil || a == nil {
		return err
	}
	if a.ServerAdmin {
		return nil
	}
	ok, err := store.IsWorkspaceAdmin(ctx, q, a.ID, wsID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotAdmin
	}
	return nil
}

// requireServerAdmin：操作者未停用且是服务器管理员。
func requireServerAdmin(ctx context.Context, q store.DBTX, actor *store.Account) error {
	a, err := freshActor(ctx, q, actor)
	if err != nil || a == nil {
		return err
	}
	if !a.ServerAdmin {
		return ErrNotServerAdmin
	}
	return nil
}

// canManagePerson（批设备、吊销设备、看设备）：操作者是服务器管理员，
// 或存在某个工作空间，target 在册且操作者是它在册的管理员。
func canManagePerson(ctx context.Context, q store.DBTX, actor *store.Account, targetID int64) error {
	a, err := freshActor(ctx, q, actor)
	if err != nil || a == nil {
		return err
	}
	if a.ServerAdmin {
		return nil
	}
	ok, err := store.AdministersMemberOf(ctx, q, a.ID, targetID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotManager
	}
	return nil
}

// canManagePersonIn：在 ws 的语境下（工作空间管理页、本群审批按钮）管这个人：
// 操作者能管理 ws，且 target 是 ws 的在册成员。
func canManagePersonIn(ctx context.Context, q store.DBTX, actor *store.Account, wsID, targetID int64) error {
	if err := canAdmin(ctx, q, actor, wsID); err != nil {
		return err
	}
	ok, err := store.IsActiveMemberOf(ctx, q, targetID, wsID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotManager
	}
	return nil
}
