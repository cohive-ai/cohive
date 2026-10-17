package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/cohive-ai/cohive/internal/core"
)

// serverCmd 管理服务器级设置：对外地址。
func serverCmd(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 || (args[0] != "set" && args[0] != "show") {
		fmt.Fprint(out, usage)
		return fmt.Errorf("server 只支持 set 与 show")
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
			fmt.Fprintln(out, "对外地址：未设置（群消息里不带链接）")
		case ext.Locked:
			fmt.Fprintf(out, "对外地址：%s（由环境变量 COHIVE_EXTERNAL_URL 锁定）\n", ext.URL)
		default:
			fmt.Fprintf(out, "对外地址：%s\n", ext.URL)
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
	fs.StringVar(&raw, "external-url", "", "对外地址，例如 https://cohive.example.com（不带路径）；传空字符串清除")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	fs.Visit(func(f *flag.Flag) { set = set || f.Name == "external-url" })
	if !set {
		return fmt.Errorf("缺少参数 --external-url")
	}
	ext, err := svc.SetExternalURL(ctx, core.ServerCLI(), raw)
	if err != nil {
		return err
	}
	show(ext)
	fmt.Fprintln(out, "正在运行的协作服务 30 秒内生效")
	return nil
}
