package theme

import (
	"io"
	"os"

	"github.com/cohive-ai/cohive/internal/i18n"
)

// Loaded 是读取一份自定义主题文件的结果。CSS 为空表示这份文件不能加载（有被拒绝的写法）。
type Loaded struct {
	Path    string
	CSS     []byte
	Parsed  Parsed
	Results []Result
}

// Failed 返回没达标的对比度检查。
func (l *Loaded) Failed() []Result {
	var out []Result
	for _, r := range l.Results {
		if !r.Pass() {
			out = append(out, r)
		}
	}
	return out
}

// Load 读取服务器管理员指定的主题文件：只接受普通文件，超过大小上限的不读，
// 有被拒绝的写法时整份不加载，未知变量丢掉，对比度不达标只报告不拦。
func Load(s *Spec, def Theme, path string) (*Loaded, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, i18n.Wrap(err, "server.theme.read_failed", i18n.D{"Path": path})
	}
	if !fi.Mode().IsRegular() {
		return nil, i18n.Errorf("server.theme.not_regular", i18n.D{"Path": path})
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, i18n.Wrap(err, "server.theme.read_failed", i18n.D{"Path": path})
	}
	defer f.Close()
	// 多读一个字节，超出上限时 Parse 会报“文件过大”
	b, err := io.ReadAll(io.LimitReader(f, int64(s.MaxFileBytes)+1))
	if err != nil {
		return nil, i18n.Wrap(err, "server.theme.read_failed", i18n.D{"Path": path})
	}
	l := &Loaded{Path: path, Parsed: s.Parse(b)}
	l.Results = s.Check(def, l.Parsed.Theme)
	if len(l.Parsed.Rejected) == 0 {
		l.CSS = s.CSS(l.Parsed.Theme)
	}
	return l, nil
}
