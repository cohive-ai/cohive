package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/store"
)

// telegramFromEnv 返回配置好的 Telegram 客户端；没有设置机器人令牌时返回 nil。
func telegramFromEnv() *notify.Telegram {
	token := os.Getenv("COHIVE_TELEGRAM_BOT_TOKEN")
	if token == "" {
		return nil
	}
	return &notify.Telegram{Token: token, BaseURL: os.Getenv("COHIVE_TELEGRAM_API_BASE")}
}

func notifyCmd(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 || args[0] != "test" {
		fmt.Fprint(out, usage)
		return fmt.Errorf("未知命令 %q", "notify")
	}
	var name string
	if err := parse("notify test", args[1:], func(fs *flag.FlagSet) {
		fs.StringVar(&name, "workspace", "default", "工作空间名")
	}); err != nil {
		return err
	}
	tg := telegramFromEnv()
	if tg == nil {
		return errors.New("未设置环境变量 COHIVE_TELEGRAM_BOT_TOKEN（Telegram 机器人令牌）")
	}
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	wsID, err := resolveWorkspace(ctx, pool, name)
	if err != nil {
		return err
	}
	ws, err := store.GetWorkspace(ctx, pool, wsID)
	if err != nil {
		return err
	}
	if ws.Notify.Channel != "telegram" {
		return fmt.Errorf("工作空间 %s 还没配置群通知：在群里发 /chatid 拿到群编号，再执行 workspace notify --workspace %s --telegram-chat <群编号>", name, name)
	}
	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	text := fmt.Sprintf("Cohive 测试消息：工作空间 %s 的群通知已接通。之后轮到谁处理问题单，会在这里 @ 到人。", name)
	if err := tg.Send(sendCtx, ws.Notify.Target, text); err != nil {
		return err
	}
	fmt.Fprintf(out, "已发送测试消息到工作空间 %s 的群\n", name)
	return nil
}
