package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

var joinStatusLabel = map[string]string{"pending": "待审", "approved": "已批准", "rejected": "已拒绝"}

// joinCmd 是加入申请的命令行备用入口；平时由管理员在群里点按钮审批。
func joinCmd(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return errors.New("join 缺少子命令")
	}
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		var name string
		var all bool
		if err := parse("join list", rest, func(fs *flag.FlagSet) {
			fs.StringVar(&name, "workspace", "default", "工作空间名")
			fs.BoolVar(&all, "all", false, "也列出已处理的申请")
		}); err != nil {
			return err
		}
		wsID, err := resolveWorkspace(ctx, pool, name)
		if err != nil {
			return err
		}
		status := "pending"
		if all {
			status = ""
		}
		list, err := store.ListJoinRequests(ctx, pool, wsID, status)
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "编号\tTelegram 用户名\t显示名\tTelegram 编号\t状态\t申请时间")
		for _, j := range list {
			fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%s\t%s\n", j.ID, j.TelegramUsername, j.DisplayName, j.TelegramUserID,
				joinStatusLabel[j.Status], j.CreatedAt.Local().Format("2006-01-02 15:04"))
		}
		return tw.Flush()
	case "approve", "reject":
		var id int64
		if err := parse("join "+sub, rest, func(fs *flag.FlagSet) {
			fs.Int64Var(&id, "id", 0, "申请编号（join list 查看）")
		}); err != nil {
			return err
		}
		if id <= 0 {
			return errors.New("缺少参数 --id")
		}
		d, err := (&core.Service{Pool: pool}).DecideJoinByOperator(ctx, id, sub == "approve")
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("申请 %d 不存在", id)
		}
		if err != nil {
			return err
		}
		if d.Member != nil {
			fmt.Fprintf(out, "已批准，%s 以成员名 %s 加入工作空间 %s\n", d.Request.DisplayName, d.Member.Name, d.Workspace.Name)
		} else {
			fmt.Fprintf(out, "已拒绝申请 %d\n", id)
		}
		return nil
	}
	fmt.Fprint(out, usage)
	return fmt.Errorf("未知命令 %q", "join "+sub)
}
