package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

// adminCmd 是服务器上的账号兜底命令：日常管理在网页上做，这里只处理忘记密码等无法在网页完成的事。
func adminCmd(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 || args[0] != "reset-password" {
		fmt.Fprint(out, usage)
		return errors.New("admin 只支持 reset-password")
	}
	var name string
	fs := flag.NewFlagSet("admin reset-password", flag.ContinueOnError)
	fs.StringVar(&name, "name", "", "登录名")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if name == "" {
		return errors.New("缺少参数 --name")
	}
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	pw, err := (&core.Service{Pool: pool}).ResetPassword(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("没有登录名为 %s 的账号", name)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "已重置 %s 的密码，新密码（只显示这一次）：\n\n  %s\n\n登录网页后须立即修改；该账号原有的网页登录已全部失效。\n", name, pw)
	return nil
}
