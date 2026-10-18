// Package server 组装协作服务的全部 HTTP 路由：MCP、网页接口、健康检查与看板页面。
package server

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/cohive-ai/cohive/internal/buildinfo"
	"github.com/cohive-ai/cohive/internal/clidist"
	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/httpapi"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/mcpserver"
)

// Options 是协作服务的 HTTP 选项。
type Options struct {
	// AllowedHosts 是除 localhost 外允许访问 MCP 的 Host，例如经 Cloudflare 隧道转发来的公网域名。
	AllowedHosts []string
	// API 是网页与命令行接口的可选依赖（机器人用户名、审批通知）。
	API httpapi.Options
	// CLI 是提供下载的命令行安装包；为空时用随程序嵌入的那一套。
	CLI *clidist.Dist
}

// New 返回协作服务的根处理器。web 是看板的构建产物（index.html 与 assets/）。
func New(svc *core.Service, web fs.FS, opts Options) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpserver.Protected(svc, opts.AllowedHosts...))
	api := httpapi.Handler(svc, opts.API)
	mux.Handle("/api/", api)
	mux.Handle("/auth/", api)
	dist := opts.CLI
	if dist == nil {
		var err error
		if dist, err = clidist.Embedded(); err != nil {
			// 嵌入的清单损坏是构建问题：不提供下载，其余功能照常
			dist, _ = clidist.New(emptyFS{})
		}
	}
	dist.Register(mux, httpapi.Origin, buildinfo.Version, buildinfo.Commit)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	mux.Handle("/", spa(web))
	return mux
}

// notBuiltPage 是看板尚未构建时的提示页；文字按请求的语言渲染，其中的 <code> 标签在语言包里。
func notBuiltPage(l i18n.Lang) string {
	return `<!doctype html><meta charset="utf-8"><title>Cohive</title>
<p>` + l.T("api.server.web_not_built") + `</p>`
}

// spa 托管看板静态文件：存在的文件原样返回，assets/ 下缺失的文件返回 404，
// 其余路径交给前端路由（返回 index.html）。
func spa(web fs.FS) http.Handler {
	files := http.FileServerFS(web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, i18n.FromRequest(r).T("api.server.get_only"), http.StatusMethodNotAllowed)
			return
		}
		index, err := fs.ReadFile(web, "index.html")
		if err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, notBuiltPage(i18n.FromRequest(r)))
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if _, err := fs.Stat(web, name); err == nil {
				if strings.HasPrefix(name, "assets/") {
					// 构建产物文件名带内容哈希，可长期缓存
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			} else if !errors.Is(err, fs.ErrNotExist) {
				http.Error(w, i18n.FromRequest(r).T("api.server.read_failed"), http.StatusInternalServerError)
				return
			}
			if strings.HasPrefix(name, "assets/") {
				http.NotFound(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}

// emptyFS 是没有任何文件的文件系统。
type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
