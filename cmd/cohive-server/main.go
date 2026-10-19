// cohive-server 是 Cohive 协作服务及其管理命令。
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/i18n"
)

// usage 是命令行帮助，文案在语言包里。
func usage() string { return i18n.T("server.usage") }

func main() {
	// serve 的语言由 COHIVE_DEFAULT_LANGUAGE 决定（见 serve）；其他运维命令跟随本机语言
	serving := len(os.Args) > 1 && os.Args[1] == "serve"
	if !serving {
		i18n.SetDefault(i18n.Local())
	}
	if err := run(context.Background(), os.Stdout, os.Args[1:]); err != nil {
		code, print := exitCode(err)
		if print && serving {
			// 服务器日志固定英文
			fmt.Fprintln(os.Stderr, "error:", err)
		} else if print {
			fmt.Fprintln(os.Stderr, i18n.T("server.error", i18n.D{"Err": i18n.Message(i18n.Default(), err)}))
		}
		os.Exit(code)
	}
}

func run(ctx context.Context, out io.Writer, args []string) error {
	if len(args) == 0 {
		fmt.Fprint(out, usage())
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
		fmt.Fprintln(out, i18n.T("server.migrate.done"))
		return nil
	case "serve":
		return serve(ctx, out, args[1:])
	case "workspace", "member", "token", "service", "account", "issue":
		return admin(ctx, out, args)
	case "notify":
		return notifyCmd(ctx, out, args[1:])
	case "admin":
		return adminCmd(ctx, out, args[1:])
	case "audit":
		return auditCmd(ctx, out, args[1:])
	case "server":
		return serverCmd(ctx, out, args[1:])
	case "theme":
		return themeCmd(out, args[1:])
	case "help", "-h", "--help":
		fmt.Fprint(out, usage())
		return nil
	default:
		fmt.Fprint(out, usage())
		return i18n.Errorf("server.unknown_command", i18n.D{"Command": strconv.Quote(args[0])})
	}
}

func databaseURL() (string, error) {
	u := os.Getenv("COHIVE_DATABASE_URL")
	if u == "" {
		return "", i18n.Errorf("server.database_url_missing")
	}
	return u, nil
}
