package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
)

// auditCmd 只读查看管理操作记录。
func auditCmd(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 || args[0] != "list" {
		fmt.Fprint(out, usage())
		return i18n.Errorf("server.audit.only_list")
	}
	var q core.EventQuery
	var before int64
	var limit int
	fs := flag.NewFlagSet("audit list", flag.ContinueOnError)
	fs.StringVar(&q.Workspace, "workspace", "", i18n.T("server.flag.audit_workspace"))
	fs.StringVar(&q.Category, "type", "", i18n.T("server.flag.audit_type", i18n.D{"Types": categoryIDs()}))
	fs.StringVar(&q.Actor, "actor", "", i18n.T("server.flag.audit_actor"))
	fs.StringVar(&q.Via, "via", "", i18n.T("server.flag.audit_via"))
	fs.Int64Var(&before, "before", 0, i18n.T("server.flag.audit_before"))
	fs.IntVar(&limit, "limit", 50, i18n.T("server.flag.audit_limit", i18n.D{"Max": core.MaxEventPage}))
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if limit < 1 {
		return i18n.Errorf("server.audit.limit_range", i18n.D{"Max": core.MaxEventPage})
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
		fmt.Fprintln(out, i18n.T("server.audit.none"))
		return nil
	}
	l := i18n.Default()
	for _, e := range p.Items {
		ws := ""
		if e.WorkspaceName != "" {
			ws = "[" + e.WorkspaceName + "]  "
		}
		fmt.Fprintf(out, "#%d  %s  %s  %s%s\n", e.ID, e.At.Local().Format("2006-01-02 15:04"), core.ViaLabel(l, e.Via), ws,
			printable(core.EventText(l, e)))
	}
	if p.NextBefore > 0 {
		fmt.Fprintln(out, i18n.T("server.audit.older", i18n.D{"Before": p.NextBefore}))
	}
	return nil
}

func categoryIDs() string {
	ids := make([]string, 0, len(core.Categories))
	for _, c := range core.Categories {
		ids = append(ids, i18n.T("server.audit.category_item", i18n.D{"ID": c.ID, "Label": c.Label(i18n.Default())}))
	}
	return strings.Join(ids, i18n.T("server.list_separator"))
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
