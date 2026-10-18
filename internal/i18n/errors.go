package i18n

import (
	"errors"
	"maps"
)

// Error 是可按语言渲染的错误：携带文案键与参数，展示时再翻译。
// Err 是被包装的错误（例如哨兵错误），errors.Is / errors.As 照常可用；
// 模板里可以用 {{.Err}} 引用它渲染后的文字。
type Error struct {
	Key  string
	Data D
	Err  error
}

// Error 用本进程的缺省语言渲染。
func (e *Error) Error() string { return e.Localize(Default()) }

func (e *Error) Unwrap() error { return e.Err }

// Localize 按指定语言渲染。
func (e *Error) Localize(l Lang) string {
	d := D{}
	maps.Copy(d, e.Data)
	if e.Err != nil {
		d["Err"] = Message(l, e.Err)
	}
	return l.T(e.Key, d)
}

// Errorf 返回一个可本地化的错误。
func Errorf(key string, data ...D) error {
	return &Error{Key: key, Data: first(data)}
}

// Wrap 包装 err，返回可本地化的错误；err 为 nil 时返回 nil。
func Wrap(err error, key string, data ...D) error {
	if err == nil {
		return nil
	}
	return &Error{Key: key, Data: first(data), Err: err}
}

// Message 按语言渲染错误：错误链上有 *Error 时翻译它，否则用 err.Error()。
func Message(l Lang, err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Localize(l)
	}
	return err.Error()
}

func first(data []D) D {
	if len(data) > 0 {
		return data[0]
	}
	return nil
}
