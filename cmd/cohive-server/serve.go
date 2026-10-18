package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cohive-ai/cohive/internal/buildinfo"
	"github.com/cohive-ai/cohive/internal/clidist"
	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/httpapi"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/server"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/web"
)

// defaultLanguage 读取 COHIVE_DEFAULT_LANGUAGE：服务器缺省语言，决定网页未指定语言时的报错、MCP 与群通知的语言。
// 不设时为英文；写了不认识的语言时报错并列出可用的语言。
func defaultLanguage() (i18n.Lang, error) {
	v := strings.TrimSpace(os.Getenv("COHIVE_DEFAULT_LANGUAGE"))
	if v == "" {
		return i18n.EN, nil
	}
	l, err := i18n.Parse(v)
	if err != nil {
		return "", fmt.Errorf("COHIVE_DEFAULT_LANGUAGE: %w", err)
	}
	return l, nil
}

// logErr 把可本地化的错误固定成英文：serve 的启动报错属于服务器日志，不随语言变化。
func logErr(err error) error {
	if err == nil {
		return nil
	}
	return errors.New(i18n.Message(i18n.EN, err))
}

// serve 启动协作服务。它打印到标准输出与日志的内容属于服务器日志，一律英文、不进语言包。
func serve(ctx context.Context, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8787", "listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	lang, err := defaultLanguage()
	if err != nil {
		return err
	}
	i18n.SetDefault(lang)

	hosts, err := allowedHosts()
	if err != nil {
		return err
	}
	envURL, err := envExternalURL()
	if err != nil {
		return logErr(err)
	}
	proxies, err := httpapi.ParseTrustedProxies(os.Getenv("COHIVE_TRUSTED_PROXIES"))
	if err != nil {
		return fmt.Errorf("COHIVE_TRUSTED_PROXIES: %w", err)
	}
	pool, err := openDB(ctx)
	if err != nil {
		return logErr(err)
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	svc := &core.Service{Pool: pool, DataDir: dataDir(), EnvExternalURL: envURL}
	if err := svc.LoadExternalURL(ctx); err != nil {
		return fmt.Errorf("reading external URL: %w", err)
	}
	if ext, _ := svc.ExternalURL(ctx); ext.URL == "" {
		fmt.Fprintln(out, "  External URL: not set (chat messages carry no links); a server admin can set it in the web UI under Server admin > Settings")
	} else if ext.Locked {
		fmt.Fprintf(out, "  External URL: %s (locked by the COHIVE_EXTERNAL_URL environment variable)\n", ext.URL)
	} else {
		fmt.Fprintf(out, "  External URL: %s\n", ext.URL)
	}
	// 还没有任何管理员时生成初始管理员，密码只写进数据目录下的文件，不进日志
	if path, err := svc.EnsureInitialAdmin(ctx); err != nil {
		return fmt.Errorf("creating initial admin: %w", err)
	} else if path != "" {
		fmt.Fprintf(out, "  Initial admin created: username and password are in %s (readable only by the owner); change the password on first web login, then the file is removed automatically\n", path)
	}

	dist, err := clidist.Embedded()
	if err != nil {
		return fmt.Errorf("CLI install bundle: %w", err)
	}
	fmt.Fprintf(out, "  %s\n", dist.Summary())
	if m := dist.Manifest; m != nil && m.Version != buildinfo.Version {
		fmt.Fprintf(out, "  Warning: CLI bundle version %s does not match server version %s; rebuild with make build\n", m.Version, buildinfo.Version)
	}
	opts := server.Options{AllowedHosts: hosts, CLI: dist}
	opts.API.TrustedProxies = proxies
	// 群通知与机器人：设置了 Telegram 机器人令牌或钉钉应用凭据才启用（可以同时启用）；
	// 都没设置时待发记录会保留，设置后重启即可补发
	dt, err := dingtalkFromEnv()
	if err != nil {
		return logErr(err)
	}
	hub := &notify.Hub{Core: svc, Channels: map[string]notify.Channel{}}
	senders := map[string]notify.Sender{}
	var infos []func() httpapi.ChannelInfo
	if tg := telegramFromEnv(); tg != nil {
		bot := &notify.Bot{TG: tg, Core: svc, Hub: hub}
		hub.Channels[store.ProviderTelegram], senders[store.ProviderTelegram] = bot, bot
		go bot.Run(ctx)
		opts.API.BotName = bot.Username
		infos = append(infos, func() httpapi.ChannelInfo {
			online, detail := bot.Status()
			return httpapi.ChannelInfo{Provider: store.ProviderTelegram, BotName: bot.Username(), Online: online, Detail: detail}
		})
		fmt.Fprintln(out, "  Group notifications: Telegram enabled; send /chatid in a chat to get its ID")
	}
	if dt != nil {
		dt.Core, dt.Hub = svc, hub
		hub.Channels[store.ProviderDingTalk], senders[store.ProviderDingTalk] = dt, dt
		go dt.Run(ctx)
		infos = append(infos, func() httpapi.ChannelInfo {
			online, detail := dt.Status()
			return httpapi.ChannelInfo{Provider: store.ProviderDingTalk, Online: online, Detail: detail,
				AppID: "…" + tail(dt.API.ClientID, 4)}
		})
		fmt.Fprintf(out, "  Group notifications: DingTalk enabled (client ID ending ...%s), Stream connecting; one DingTalk app can serve only one Cohive server\n", tail(dt.API.ClientID, 4))
	}
	if len(hub.Channels) > 0 {
		d := &notify.Dispatcher{Pool: pool, Senders: senders, Reminders: svc.QueueReminders,
			BaseURL: func(ctx context.Context) string { ext, _ := svc.ExternalURL(ctx); return ext.URL }}
		go d.Run(ctx, 2*time.Second)
		opts.API.Announcer = hub
		opts.API.Channels = func() []httpapi.ChannelInfo {
			out := make([]httpapi.ChannelInfo, 0, len(infos))
			for _, f := range infos {
				out = append(out, f())
			}
			return out
		}
	} else {
		fmt.Fprintln(out, "  Group notifications: COHIVE_TELEGRAM_BOT_TOKEN or DingTalk app credentials not set, not sending (pending records are kept)")
		// 没有群也照样催办：会话内提醒照发，群催办写进待发记录，设置机器人后补发
		go func() {
			t := time.NewTicker(notify.RemindEvery)
			defer t.Stop()
			for {
				if _, err := svc.QueueReminders(ctx); err != nil && ctx.Err() == nil {
					log.Printf("queue overdue reminders: %v", err)
				}
				select {
				case <-ctx.Done():
					return
				case <-t.C:
				}
			}
		}()
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(svc, web.Dist(), opts),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Fprintf(out, "Cohive server started, version %s\n", buildinfo.String())
	fmt.Fprintf(out, "  Default language: %s\n", lang)
	fmt.Fprintf(out, "  Web: http://%[1]s/\n  MCP: http://%[1]s/mcp\n", displayAddr(*addr))
	if len(hosts) > 0 {
		fmt.Fprintf(out, "  MCP allowed through these hostnames: %s\n", strings.Join(hosts, ", "))
	}
	if len(proxies) > 0 {
		fmt.Fprintf(out, "  Trusting forwarded client-address headers from: %s\n", os.Getenv("COHIVE_TRUSTED_PROXIES"))
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		fmt.Fprintln(out, "Shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// dataDir 返回服务器数据目录（COHIVE_DATA_DIR，缺省为当前目录下的 data），保存初始管理员密码等文件。
func dataDir() string {
	if d := os.Getenv("COHIVE_DATA_DIR"); d != "" {
		return d
	}
	return "data"
}

// allowedHosts 读取 COHIVE_ALLOWED_HOSTS（逗号分隔），即经隧道等方式访问时使用的公网域名。
// 只接受域名（可带端口），写成完整地址时报错，避免静默失效。
func allowedHosts() ([]string, error) {
	var out []string
	for _, h := range strings.Split(os.Getenv("COHIVE_ALLOWED_HOSTS"), ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if strings.Contains(h, "://") || strings.Contains(h, "/") {
			return nil, fmt.Errorf("COHIVE_ALLOWED_HOSTS entry %q is invalid: write only the hostname, for example cohive.example.com, without https:// or a path", h)
		}
		out = append(out, h)
	}
	return out, nil
}

func displayAddr(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "localhost" + addr
	}
	return addr
}

// envExternalURL 读取 COHIVE_EXTERNAL_URL：设了就锁定对外地址，网页与命令行不能改；格式不对时不启动。
func envExternalURL() (string, error) {
	raw := strings.TrimSpace(os.Getenv("COHIVE_EXTERNAL_URL"))
	if raw == "" {
		return "", nil
	}
	v, err := core.NormalizeExternalURL(raw)
	if err != nil {
		return "", i18n.Wrap(err, "server.external_url.env_invalid")
	}
	return v, nil
}
