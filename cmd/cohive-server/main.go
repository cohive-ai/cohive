// cohive-server 是 Cohive 协作服务及其管理命令。
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cohive-ai/cohive/internal/db"
)

const usage = `用法：cohive-server <命令>

命令：
  migrate                                         执行数据库迁移
  serve [--addr :8787]                            启动协作服务
  member add --name <名称> [--display <显示名>]    添加成员
  member list                                     列出成员
  token issue --member <名称> --tool <工具>        为成员的某个工具签发令牌（只显示一次）
  token list                                      列出令牌
  token revoke --id <编号>                         吊销令牌
  service add --name <服务> --owner <成员>         登记服务负责人（已存在则更新）
  service list                                    列出服务

数据库地址取环境变量 COHIVE_DATABASE_URL。
`

func main() {
	if err := run(context.Background(), os.Stdout, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage)
		return nil
	}
	switch args[0] {
	case "migrate":
		pool, err := openDB(ctx)
		if err != nil {
			return err
		}
		defer pool.Close()
		if err := db.Migrate(ctx, pool); err != nil {
			return err
		}
		fmt.Fprintln(out, "迁移完成")
		return nil
	case "serve":
		return serve(ctx, out, args[1:])
	case "member", "token", "service":
		return admin(ctx, out, args)
	case "help", "-h", "--help":
		fmt.Fprint(out, usage)
		return nil
	default:
		fmt.Fprint(out, usage)
		return fmt.Errorf("未知命令 %q", args[0])
	}
}

func databaseURL() (string, error) {
	u := os.Getenv("COHIVE_DATABASE_URL")
	if u == "" {
		return "", fmt.Errorf("未设置环境变量 COHIVE_DATABASE_URL")
	}
	return u, nil
}
