package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/notify"
	"github.com/cohive-ai/cohive/internal/server"
	"github.com/cohive-ai/cohive/web"
)

func serve(ctx context.Context, out io.Writer, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", ":8787", "监听地址")
	if err := fs.Parse(args); err != nil {
		return err
	}

	pool, err := openDB(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           server.New(&core.Service{Pool: pool}, web.Dist()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 群通知：设置了机器人令牌才投递；没设置时待发记录会保留，设置后重启即可补发
	if tg := telegramFromEnv(); tg != nil {
		d := &notify.Dispatcher{Pool: pool, Senders: map[string]notify.Sender{"telegram": tg}}
		go d.Run(ctx, 2*time.Second)
		go (&notify.Bot{TG: tg}).Run(ctx)
		fmt.Fprintln(out, "  群通知：Telegram 已启用；群里发 /chatid 可获取群编号")
	} else {
		fmt.Fprintln(out, "  群通知：未设置 COHIVE_TELEGRAM_BOT_TOKEN，暂不发送（待发记录会保留）")
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	fmt.Fprintf(out, "Cohive 协作服务已启动\n  看板：http://%[1]s/\n  MCP： http://%[1]s/mcp\n", displayAddr(*addr))

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

func displayAddr(addr string) string {
	if len(addr) > 0 && addr[0] == ':' {
		return "localhost" + addr
	}
	return addr
}
