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

服务器设置：
  server show                                     查看对外地址
  server set --external-url <地址>                设置对外地址（群消息链接与 MCP 主机名用它），例如 https://cohive.example.com；
                                                  传空字符串清除；设了 COHIVE_EXTERNAL_URL 时以环境变量为准，不能改

工作空间（一个项目一个群，对应一个工作空间；路径不可改，显示名可改）：
  workspace add --name <路径> [--display <显示名>] [--admin <登录名>]
                                                  创建工作空间并指定首位管理员
  workspace set --name <路径> --display <显示名>   修改显示名（传空字符串恢复为与路径相同）
  workspace remove --name <路径>                  删除工作空间（只限没有任何问题单的）
  workspace list                                  列出工作空间与群通知配置
  workspace notify --workspace <路径> --telegram-chat <群聊编号（群里发 /chatid 获取）>
      [--show-title=false] [--show-brief=false]   配置群通知；--off 关闭；一个群只能绑一个工作空间
  notify test --workspace <名称>                   往工作空间的群里发一条测试消息
  notify retry --workspace <名称>                  把发送失败的通知重新排队

账号（全服务器一份；日常管理请用网页的“管理”与“服务器管理”页）：
  account list [--prefix <登录名前缀>]             列出账号、所在工作空间与设备数
  account set --name <登录名> [--display <显示名>] [--telegram <用户名>] [--telegram-id <编号>]
                                                  修改显示名，登记 Telegram 用户名（用于 @）或用户编号
  account disable --name <登录名>                  停用：吊销全部设备与令牌、注销网页登录、断开 Telegram
  account enable --name <登录名>                   启用（设备不恢复，需重新配对并由管理员批准）
  account server-admin --name <登录名> --on|--off  授予或撤销服务器管理员
  admin reset-password --name <登录名>             重置网页密码，新密码只在这里显示一次，登录后须修改

管理操作记录（只读；网页的“管理”与“服务器管理”页各有“记录”一栏）：
  audit list [--workspace <名称>] [--type <类别>] [--actor <登录名>] [--via <入口>] [--limit 50] [--before <编号>]
                                                  按时间倒序列出管理操作记录

成员资格、令牌与服务（都可加 --workspace <名称>，缺省为 default）：
  member add --name <登录名> [--display <显示名>] [--role admin]
                                                  把账号加入工作空间；账号不存在时新建（没有密码）
  member set --name <登录名> --role admin|member  设置角色（至少保留一名管理员）
  member remove --name <登录名> --handover-to <登录名> --as <管理员登录名>
                                                  移出：名下待处理问题单与服务全部转给接手人，令牌吊销
  member list [--all]                             列出在册成员（--all 含已移出的）
  token issue --member <登录名> --tool <工具>      为成员的某个工具签发令牌（只显示一次）
  token list                                      列出令牌
  token revoke --id <编号>                         吊销令牌
  service add --name <服务> --owner <登录名>       登记服务负责人（已存在则更新）
  service list                                    列出服务

环境变量：
  COHIVE_DATABASE_URL          数据库地址（必填）
  COHIVE_TELEGRAM_BOT_TOKEN    Telegram 机器人令牌（使用群通知时必填）
  COHIVE_EXTERNAL_URL          对外地址（可选）；设了就锁定，网页与命令行不能改
  COHIVE_ALLOWED_HOSTS         除对外地址外，经隧道等方式访问 MCP 时还允许的域名，逗号分隔
  COHIVE_DATA_DIR              数据目录（缺省为 ./data），首次启动时初始管理员密码写在这里
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
	case "workspace", "member", "token", "service", "account":
		return admin(ctx, out, args)
	case "notify":
		return notifyCmd(ctx, out, args[1:])
	case "admin":
		return adminCmd(ctx, out, args[1:])
	case "audit":
		return auditCmd(ctx, out, args[1:])
	case "server":
		return serverCmd(ctx, out, args[1:])
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
