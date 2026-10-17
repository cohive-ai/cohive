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

// dingtalkFromEnv 返回配置好的钉钉通道；没有配置应用凭据时返回 nil。Client ID 与 Secret 只配了一个时报错。
// 凭据只从环境变量读，不打印。
func dingtalkFromEnv() (*notify.DingTalkChannel, error) {
	id, secret := os.Getenv("COHIVE_DINGTALK_CLIENT_ID"), os.Getenv("COHIVE_DINGTALK_CLIENT_SECRET")
	switch {
	case id == "" && secret == "":
		return nil, nil
	case id == "":
		return nil, errors.New("设置了 COHIVE_DINGTALK_CLIENT_SECRET，但缺少 COHIVE_DINGTALK_CLIENT_ID（钉钉应用的 Client ID）")
	case secret == "":
		return nil, errors.New("设置了 COHIVE_DINGTALK_CLIENT_ID，但缺少 COHIVE_DINGTALK_CLIENT_SECRET（钉钉应用的 Client Secret）")
	}
	return &notify.DingTalkChannel{
		API: &notify.DingTalk{ClientID: id, ClientSecret: secret, RobotCode: os.Getenv("COHIVE_DINGTALK_ROBOT_CODE"),
			BaseURL: os.Getenv("COHIVE_DINGTALK_API_BASE")},
		NoticeTemplate:   os.Getenv("COHIVE_DINGTALK_NOTICE_TEMPLATE"),
		ApprovalTemplate: os.Getenv("COHIVE_DINGTALK_APPROVAL_TEMPLATE"),
	}, nil
}

// tail 返回字符串的最后几位，用于在日志里核对用的是哪个应用。
func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func notifyCmd(ctx context.Context, out io.Writer, args []string) error {
	if len(args) > 0 && args[0] == "retry" {
		return notifyRetry(ctx, out, args[1:])
	}
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
	dt, err := dingtalkFromEnv()
	if err != nil {
		return err
	}
	if telegramFromEnv() == nil && dt == nil {
		return errors.New("未设置环境变量 COHIVE_TELEGRAM_BOT_TOKEN（Telegram 机器人令牌）或钉钉应用凭据 COHIVE_DINGTALK_CLIENT_ID、COHIVE_DINGTALK_CLIENT_SECRET")
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
	var ch notify.Channel
	switch ws.Notify.Channel {
	case store.ProviderTelegram:
		tg := telegramFromEnv()
		if tg == nil {
			return errors.New("未设置环境变量 COHIVE_TELEGRAM_BOT_TOKEN（Telegram 机器人令牌）")
		}
		ch = &notify.Bot{TG: tg}
	case store.ProviderDingTalk:
		if dt == nil {
			return errors.New("未设置钉钉应用凭据 COHIVE_DINGTALK_CLIENT_ID、COHIVE_DINGTALK_CLIENT_SECRET")
		}
		ch = dt
	default:
		return fmt.Errorf("工作空间 %s 还没配置群通知：Telegram 群在群里发 /chatid 拿到群编号，再执行 workspace notify --workspace %s --telegram-chat <群编号>；钉钉群在网页“管理 → 工作空间”生成绑定码，在群里 @机器人 发“绑定群 绑定码”", name, name)
	}
	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	text := fmt.Sprintf("Cohive 测试消息：工作空间 %s 的群通知已接通。之后轮到谁处理问题单，会在这里 @ 到人。", ch.Format().Esc(name))
	if err := ch.Send(sendCtx, ws.Notify.Target, notify.Rendered{Title: "Cohive 测试消息", Text: text}); err != nil {
		return err
	}
	fmt.Fprintf(out, "已发送测试消息到工作空间 %s 的群\n", name)
	return nil
}

// notifyRetry 把工作空间里发送失败的通知重新排队（例如修好群配置或令牌之后）。
func notifyRetry(ctx context.Context, out io.Writer, args []string) error {
	var name string
	if err := parse("notify retry", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "workspace", "default", "工作空间名")
	}); err != nil {
		return err
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
	n, err := store.RequeueFailedNotifications(ctx, pool, wsID)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "已把工作空间 %s 的 %d 条失败通知重新排队，服务会在几秒内重发（问题单已有新进展的会被跳过）\n", name, n)
	return nil
}
