package core

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

// 服务器级的对外地址（参照 GitLab 的 external_url）：群消息等服务端主动生成的链接一律用它拼，
// 它的主机名也自动放进 MCP 的主机名白名单。环境变量 COHIVE_EXTERNAL_URL 设了就以它为准（锁定）。

// ExternalURL 是生效的对外地址。
type ExternalURL struct {
	URL    string // 空表示没设
	Locked bool   // 由环境变量锁定，网页与命令行不能改
}

// ErrExternalURLLocked 表示对外地址由环境变量锁定。
var ErrExternalURLLocked = &StateError{Msg: "对外地址由环境变量 COHIVE_EXTERNAL_URL 锁定：请改环境变量后重启协作服务"}

// NormalizeExternalURL 校验并规范化对外地址：只允许 http/https 加主机名与可选端口，不带路径、查询参数、片段与用户信息；
// 协议与主机名转小写，去掉末尾的 /。空串合法，表示清除。
func NormalizeExternalURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	bad := func() (string, error) {
		return "", inputErr("对外地址应为 https://主机名 或 http://主机名:端口，不带路径，例如 https://cohive.example.com")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, "#") ||
		(u.Path != "" && u.Path != "/") {
		return bad()
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" || u.Host == "" || strings.ContainsAny(u.Host, " \t") {
		return bad()
	}
	host := strings.ToLower(u.Host)
	if h, port, err := net.SplitHostPort(host); err == nil {
		if h == "" || port == "" {
			return bad()
		}
	} else if strings.HasSuffix(host, ":") {
		return bad()
	}
	return scheme + "://" + host, nil
}

// externalCache 是进程内缓存的对外地址。网页上改时直接更新；服务器命令行在别的进程里改，
// 所以缓存最多用 externalTTL，过期后重读数据库。
type externalCache struct {
	url string
	at  time.Time
}

const externalTTL = 30 * time.Second

// ExternalURL 返回生效的对外地址：环境变量优先，其次数据库。结果缓存在进程里。
func (s *Service) ExternalURL(ctx context.Context) (ExternalURL, error) {
	if s.EnvExternalURL != "" {
		return ExternalURL{URL: s.EnvExternalURL, Locked: true}, nil
	}
	if c := s.external.Load(); c != nil && time.Since(c.at) < externalTTL {
		return ExternalURL{URL: c.url}, nil
	}
	st, err := store.GetServerSettings(ctx, s.Pool, false)
	if errors.Is(err, store.ErrNotFound) {
		return ExternalURL{}, nil
	}
	if err != nil {
		return ExternalURL{}, err
	}
	s.external.Store(&externalCache{url: st.ExternalURL, at: time.Now()})
	return ExternalURL{URL: st.ExternalURL}, nil
}

// ExternalHost 返回对外地址的主机名（不含端口），给 MCP 主机名白名单用。读缓存；缓存过期时重读一次数据库
// （最多每 30 秒一次），读失败就沿用旧值。
func (s *Service) ExternalHost() string {
	raw := s.EnvExternalURL
	if raw == "" {
		c := s.external.Load()
		if c == nil || time.Since(c.at) >= externalTTL {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			ext, err := s.ExternalURL(ctx)
			cancel()
			if err == nil {
				raw = ext.URL
			} else if c != nil {
				raw = c.url
			}
		} else {
			raw = c.url
		}
	}
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// LoadExternalURL 在启动时加载对外地址缓存。
func (s *Service) LoadExternalURL(ctx context.Context) error {
	s.external.Store(nil)
	_, err := s.ExternalURL(ctx)
	return err
}

// SetExternalURL 由服务器管理员修改对外地址（空串表示清除）。锁定时报错；值没变不写记录。
func (s *Service) SetExternalURL(ctx context.Context, act Actor, raw string) (ExternalURL, error) {
	if s.EnvExternalURL != "" {
		return ExternalURL{URL: s.EnvExternalURL, Locked: true}, ErrExternalURLLocked
	}
	v, err := NormalizeExternalURL(raw)
	if err != nil {
		return ExternalURL{}, err
	}
	err = s.inTx(ctx, func(tx pgx.Tx) error {
		if err := requireServerAdmin(ctx, tx, act.Account); err != nil {
			return err
		}
		old, err := store.GetServerSettings(ctx, tx, true)
		if err != nil {
			return err
		}
		if old.ExternalURL == v {
			return nil
		}
		if err := store.SetExternalURL(ctx, tx, v, actorID(act.Account)); err != nil {
			return err
		}
		return record(ctx, tx, act, store.AdminEvent{Action: ActServerSettings, TargetKind: "server", TargetName: "对外地址",
			Detail: map[string]any{"external_url": map[string]any{"from": old.ExternalURL, "to": v}}})
	})
	if err != nil {
		return ExternalURL{}, err
	}
	// 提交成功后才更新缓存
	s.external.Store(&externalCache{url: v, at: time.Now()})
	return ExternalURL{URL: v}, nil
}

// WorkspaceLink 返回工作空间网页的地址：对外地址 + /w/路径；没设对外地址时为空。
func WorkspaceLink(base, wsPath string) string {
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/w/" + url.PathEscape(wsPath)
}

// IssueLink 返回群消息里问题单链接的写法：对外地址 + /w/路径/issues/编号；没设对外地址时为空。
func IssueLink(base, wsPath string, issueID int64) string {
	if base == "" {
		return ""
	}
	return WorkspaceLink(base, wsPath) + "/issues/" + strconv.FormatInt(issueID, 10)
}
