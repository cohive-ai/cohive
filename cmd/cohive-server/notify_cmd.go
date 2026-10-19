package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/i18n"
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
		return nil, i18n.Errorf("server.notify.dingtalk_id_missing")
	case secret == "":
		return nil, i18n.Errorf("server.notify.dingtalk_secret_missing")
	}
	return &notify.DingTalkChannel{
		API: &notify.DingTalk{ClientID: id, ClientSecret: secret, RobotCode: os.Getenv("COHIVE_DINGTALK_ROBOT_CODE"),
			BaseURL: os.Getenv("COHIVE_DINGTALK_API_BASE")},
		NoticeTemplate:   os.Getenv("COHIVE_DINGTALK_NOTICE_TEMPLATE"),
		ApprovalTemplate: os.Getenv("COHIVE_DINGTALK_APPROVAL_TEMPLATE"),
	}, nil
}

// slackFromEnv 返回配置好的 Slack 通道；两个令牌都没配时返回 nil，只配了一个或明显填反时报错。令牌只从环境变量读，不打印。
func slackFromEnv() (*notify.SlackChannel, error) {
	bot, app := os.Getenv("COHIVE_SLACK_BOT_TOKEN"), os.Getenv("COHIVE_SLACK_APP_TOKEN")
	switch {
	case bot == "" && app == "":
		return nil, nil
	case bot == "":
		return nil, i18n.Errorf("server.notify.slack_bot_token_missing")
	case app == "":
		return nil, i18n.Errorf("server.notify.slack_app_token_missing")
	case strings.HasPrefix(bot, "xapp-") || strings.HasPrefix(app, "xoxb-"):
		return nil, i18n.Errorf("server.notify.slack_tokens_swapped")
	}
	return &notify.SlackChannel{API: &notify.Slack{BotToken: bot, AppToken: app, BaseURL: os.Getenv("COHIVE_SLACK_API_BASE")}}, nil
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
		fmt.Fprint(out, usage())
		return i18n.Errorf("server.unknown_command", i18n.D{"Command": strconv.Quote("notify")})
	}
	var name string
	if err := parse("notify test", args[1:], func(fs *flag.FlagSet) {
		fs.StringVar(&name, "workspace", "default", i18n.T("server.flag.ws_name"))
	}); err != nil {
		return err
	}
	dt, err := dingtalkFromEnv()
	if err != nil {
		return err
	}
	sl, err := slackFromEnv()
	if err != nil {
		return err
	}
	if telegramFromEnv() == nil && dt == nil && sl == nil {
		return i18n.Errorf("server.notify.no_credentials")
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
			return i18n.Errorf("server.notify.telegram_token_missing")
		}
		ch = &notify.Bot{TG: tg}
	case store.ProviderDingTalk:
		if dt == nil {
			return i18n.Errorf("server.notify.dingtalk_credentials_missing")
		}
		ch = dt
	case store.ProviderSlack:
		if sl == nil {
			return i18n.Errorf("server.notify.slack_credentials_missing")
		}
		ch = sl
	default:
		return i18n.Errorf("server.notify.not_configured", i18n.D{"Name": name})
	}
	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// 测试消息发到群里，用该工作空间的语言（没设时用服务器缺省语言），不跟随运维人员终端的语言
	lang, err := defaultLanguage()
	if err != nil {
		return err
	}
	if l, ok := i18n.Match(ws.Language); ws.Language != "" && ok {
		lang = l
	}
	text := lang.T("server.notify.test_text", i18n.D{"Name": ch.Format().Esc(name)})
	if err := ch.Send(sendCtx, ws.Notify.Target, notify.Rendered{Title: lang.T("server.notify.test_title"), Text: text}); err != nil {
		return err
	}
	fmt.Fprintln(out, i18n.T("server.notify.test_sent", i18n.D{"Name": name}))
	return nil
}

// notifyRetry 把工作空间里发送失败的通知重新排队（例如修好群配置或令牌之后）。
func notifyRetry(ctx context.Context, out io.Writer, args []string) error {
	var name string
	if err := parse("notify retry", args, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "workspace", "default", i18n.T("server.flag.ws_name"))
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
	fmt.Fprintln(out, i18n.T("server.notify.retried", i18n.D{"Name": name, "Count": n}))
	return nil
}
