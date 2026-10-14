// cohive-server 是 Cohive 协作服务及其管理命令。
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/cohive-ai/cohive/internal/db"
)

const usage = `用法：cohive-server <命令>

命令：
  migrate    执行数据库迁移
  serve      启动协作服务（--addr 监听地址，默认 :8787）

数据库地址取环境变量 COHIVE_DATABASE_URL。
`

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
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
		fmt.Println("迁移完成")
		return nil
	case "serve":
		return serve(ctx, args[1:])
	default:
		fmt.Print(usage)
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
