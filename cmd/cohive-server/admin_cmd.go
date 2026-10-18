package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// adminCmd 是服务器上的账号兜底命令：日常管理在网页上做，这里只处理忘记密码等无法在网页完成的事。
func adminCmd(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 || args[0] != "reset-password" {
		fmt.Fprint(out, usage())
		return i18n.Errorf("server.admin.only_reset_password")
	}
	var name string
	fs := flag.NewFlagSet("admin reset-password", flag.ContinueOnError)
	fs.StringVar(&name, "name", "", i18n.T("server.flag.username"))
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if name == "" {
		return i18n.Errorf("server.arg_missing", i18n.D{"Name": "name"})
	}
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	pw, err := (&core.Service{Pool: pool}).ResetPassword(ctx, core.ServerCLI(), name)
	if errors.Is(err, store.ErrNotFound) {
		return i18n.Errorf("server.account.not_found", i18n.D{"Name": name})
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(out, i18n.T("server.admin.password_reset", i18n.D{"Name": name, "Password": pw}))
	return nil
}
