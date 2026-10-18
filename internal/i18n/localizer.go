package i18n

import (
	"context"
	"errors"

	goi18n "github.com/nicksnyder/go-i18n/v2/i18n"
)

// D 是文案模板的参数，键名对应模板里的 {{.Name}}。键 Count 同时用于选择复数形式。
type D = map[string]any

// T 按语言渲染一条文案；当前语言缺词时回退英文，英文也没有时返回键名（完整性测试会拦住）。
func (l Lang) T(key string, data ...D) string {
	loc, ok := localizers[l]
	if !ok {
		loc = localizers[EN]
	}
	cfg := &goi18n.LocalizeConfig{MessageID: key}
	if len(data) > 0 && data[0] != nil {
		cfg.TemplateData = data[0]
		if n, ok := data[0]["Count"]; ok {
			cfg.PluralCount = n
		}
	}
	s, err := loc.Localize(cfg)
	if err != nil && s == "" {
		return key
	}
	return s
}

// Has 报告该语言自己的语言包里有没有这个键（不计回退）。
func (l Lang) Has(key string) bool {
	loc, ok := localizers[l]
	if !ok {
		return false
	}
	_, tag, err := loc.LocalizeWithTag(&goi18n.LocalizeConfig{MessageID: key})
	var nf *goi18n.MessageNotFoundErr
	if errors.As(err, &nf) {
		return false
	}
	return tag.String() == string(l)
}

// T 用本进程的缺省语言渲染文案。
func T(key string, data ...D) string { return Default().T(key, data...) }

type ctxKey struct{}

// WithLang 把请求的语言放进上下文。
func WithLang(ctx context.Context, l Lang) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext 取上下文里的语言，没有时为本进程的缺省语言。
func FromContext(ctx context.Context) Lang {
	if l, ok := ctx.Value(ctxKey{}).(Lang); ok {
		return l
	}
	return Default()
}
