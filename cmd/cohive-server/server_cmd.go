package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
)

// serverCmd 管理服务器级设置：对外地址。
func serverCmd(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 || (args[0] != "set" && args[0] != "show") {
		fmt.Fprint(out, usage())
		return i18n.Errorf("server.server.only_set_show")
	}
	envURL, err := envExternalURL()
	if err != nil {
		return err
	}
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	svc := &core.Service{Pool: pool, EnvExternalURL: envURL}
	show := func(ext core.ExternalURL) {
		switch {
		case ext.URL == "":
			fmt.Fprintln(out, i18n.T("server.external_url.unset"))
		case ext.Locked:
			fmt.Fprintln(out, i18n.T("server.external_url.locked", i18n.D{"URL": ext.URL}))
		default:
			fmt.Fprintln(out, i18n.T("server.external_url.set", i18n.D{"URL": ext.URL}))
		}
	}
	if args[0] == "show" {
		ext, err := svc.ExternalURL(ctx)
		if err != nil {
			return err
		}
		show(ext)
		return nil
	}
	var raw string
	set := false
	fs := flag.NewFlagSet("server set", flag.ContinueOnError)
	fs.StringVar(&raw, "external-url", "", i18n.T("server.flag.external_url"))
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == "external-url" })
	if !set {
		return i18n.Errorf("server.arg_missing", i18n.D{"Name": "external-url"})
	}
	ext, err := svc.SetExternalURL(ctx, core.ServerCLI(), raw)
	if err != nil {
		return err
	}
	show(ext)
	fmt.Fprintln(out, i18n.T("server.external_url.applied"))
	return nil
}
