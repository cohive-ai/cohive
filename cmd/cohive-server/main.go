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

服务：
  migrate                                         执行数据库迁移
  serve [--addr :8787]                            启动协作服务（含看板与群通知投递）

工作空间（一个业务组一个工作空间、一个群）：
  workspace add --name <名称>                      创建工作空间
  workspace list                                  列出工作空间与群通知配置
  workspace notify --workspace <名称> --telegram-chat <群聊编号（群里发 /chatid 获取）>
      [--show-title=false] [--show-brief=false] [--public-url <看板地址>]
                                                  配置群通知；--off 关闭
  notify test --workspace <名称>                   往工作空间的群里发一条测试消息

成员、令牌与服务（都可加 --workspace <名称>，缺省为 default）：
  member add --name <名称> [--display <显示名>]    添加成员
  member set --name <名称> --telegram <用户名>      登记 Telegram 用户名，用于群里 @ 提醒
  member list                                     列出成员
  token issue --member <名称> --tool <工具>        为成员的某个工具签发令牌（只显示一次）
  token list                                      列出令牌
  token revoke --id <编号>                         吊销令牌
  service add --name <服务> --owner <成员>         登记服务负责人（已存在则更新）
  service list                                    列出服务

环境变量：
  COHIVE_DATABASE_URL          数据库地址（必填）
  COHIVE_TELEGRAM_BOT_TOKEN    Telegram 机器人令牌（使用群通知时必填）
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
	case "workspace", "member", "token", "service":
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
