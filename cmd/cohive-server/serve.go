package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
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
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/server"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/cohive-ai/cohive/web"
)

func serve(ctx context.Context, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8787", "监听地址")
	if err := fs.Parse(args); err != nil {
		return err
	}

	hosts, err := allowedHosts()
	if err != nil {
		return err
	}
	envURL, err := envExternalURL()
	if err != nil {
		return err
	}
	proxies, err := httpapi.ParseTrustedProxies(os.Getenv("COHIVE_TRUSTED_PROXIES"))
	if err != nil {
		return fmt.Errorf("COHIVE_TRUSTED_PROXIES：%w", err)
	}
	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	svc := &core.Service{Pool: pool, DataDir: dataDir(), EnvExternalURL: envURL}
	if err := svc.LoadExternalURL(ctx); err != nil {
		return fmt.Errorf("读取对外地址：%w", err)
	}
	if ext, _ := svc.ExternalURL(ctx); ext.URL == "" {
		fmt.Fprintln(out, "  对外地址：未设置（群消息里不带链接）；请服务器管理员在网页“服务器管理 → 设置”里填写")
	} else if ext.Locked {
		fmt.Fprintf(out, "  对外地址：%s（由环境变量 COHIVE_EXTERNAL_URL 锁定）\n", ext.URL)
	} else {
		fmt.Fprintf(out, "  对外地址：%s\n", ext.URL)
	}
	// 还没有任何管理员时生成初始管理员，密码只写进数据目录下的文件，不进日志
	if path, err := svc.EnsureInitialAdmin(ctx); err != nil {
		return fmt.Errorf("生成初始管理员：%w", err)
	} else if path != "" {
		fmt.Fprintf(out, "  已生成初始管理员，登录名与密码在 %s（仅本人可读）；首次登录网页后须修改密码，改完该文件自动删除\n", path)
	}

	dist, err := clidist.Embedded()
	if err != nil {
		return fmt.Errorf("命令行安装包：%w", err)
	}
	fmt.Fprintf(out, "  %s\n", dist.Summary())
	if m := dist.Manifest; m != nil && m.Version != buildinfo.Version {
		fmt.Fprintf(out, "  警告：命令行安装包版本 %s 与服务端版本 %s 不一致，请用 make build 重新构建\n", m.Version, buildinfo.Version)
	}
	opts := server.Options{AllowedHosts: hosts, CLI: dist}
	opts.API.TrustedProxies = proxies
	// 群通知与机器人：设置了 Telegram 机器人令牌或钉钉应用凭据才启用（可以同时启用）；
	// 都没设置时待发记录会保留，设置后重启即可补发
	dt, err := dingtalkFromEnv()
	if err != nil {
		return err
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
		fmt.Fprintln(out, "  群通知：Telegram 已启用；群里发 /chatid 可获取群编号")
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
		fmt.Fprintf(out, "  群通知：钉钉已启用（Client ID 尾号 …%s），Stream 连接中；同一个钉钉应用只能接一台 Cohive\n", tail(dt.API.ClientID, 4))
	}
	if len(hub.Channels) > 0 {
		d := &notify.Dispatcher{Pool: pool, Senders: senders,
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
		fmt.Fprintln(out, "  群通知：未设置 COHIVE_TELEGRAM_BOT_TOKEN 或钉钉应用凭据，暂不发送（待发记录会保留）")
	}
	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(svc, web.Dist(), opts),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Fprintf(out, "Cohive 协作服务已启动，版本 %s\n", buildinfo.String())
	fmt.Fprintf(out, "  网页：http://%[1]s/\n  MCP： http://%[1]s/mcp\n", displayAddr(*addr))
	if len(hosts) > 0 {
		fmt.Fprintf(out, "  允许经这些域名访问 MCP：%s\n", strings.Join(hosts, "、"))
	}
	if len(proxies) > 0 {
		fmt.Fprintf(out, "  信任这些地址转发来的来源地址头：%s\n", os.Getenv("COHIVE_TRUSTED_PROXIES"))
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		fmt.Fprintln(out, "正在停止……")
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
			return nil, fmt.Errorf("COHIVE_ALLOWED_HOSTS 里的 %q 不对：只写域名，例如 cohive.example.com，不要带 https:// 或路径", h)
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
		return "", fmt.Errorf("COHIVE_EXTERNAL_URL：%w", err)
	}
	return v, nil
}
