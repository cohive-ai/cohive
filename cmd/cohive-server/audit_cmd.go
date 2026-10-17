package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/cohive-ai/cohive/internal/core"
)

// auditCmd 只读查看管理操作记录。
func auditCmd(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprint(out, usage)
		return errors.New("audit 只支持 list")
	}
	var q core.EventQuery
	var before int64
	var limit int
	fs := flag.NewFlagSet("audit list", flag.ContinueOnError)
	fs.StringVar(&q.Workspace, "workspace", "", "只看这个工作空间（含已删除的）")
	fs.StringVar(&q.Category, "type", "", "动作类别："+categoryIDs())
	fs.StringVar(&q.Actor, "actor", "", "操作人登录名")
	fs.StringVar(&q.Via, "via", "", "入口：web、telegram、server_cli、member_cli、agent（编程工具令牌）、system、unknown")
	fs.Int64Var(&before, "before", 0, "只看编号小于它的（翻页）")
	fs.IntVar(&limit, "limit", 50, fmt.Sprintf("条数，1 到 %d", core.MaxEventPage))
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if limit < 1 {
		return fmt.Errorf("--limit 应为 1 到 %d", core.MaxEventPage)
	}
	q.Before, q.Limit = before, limit
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	p, err := (&core.Service{Pool: pool}).ServerEvents(ctx, nil, q)
	if err != nil {
		return err
	}
	if len(p.Items) == 0 {
		fmt.Fprintln(out, "没有记录")
		return nil
	}
	for _, e := range p.Items {
		ws := ""
		if e.WorkspaceName != "" {
			ws = "[" + e.WorkspaceName + "]  "
		}
		fmt.Fprintf(out, "#%d  %s  %s  %s%s\n", e.ID, e.At.Local().Format("2006-01-02 15:04"), core.ViaLabels[e.Via], ws,
			printable(core.EventText(e)))
	}
	if p.NextBefore > 0 {
		fmt.Fprintf(out, "更早的记录：--before %d\n", p.NextBefore)
	}
	return nil
}

func categoryIDs() string {
	ids := make([]string, 0, len(core.Categories))
	for _, c := range core.Categories {
		ids = append(ids, c.ID+"（"+c.Label+"）")
	}
	return strings.Join(ids, "、")
}

// printable 把控制字符换成空格：设备名、显示名是用户填的，不能让它在终端里搞出换行或转义序列。
func printable(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}
