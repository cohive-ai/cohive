package main

import (
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/theme"
)

// exitError 让命令以指定的退出码结束；err 为空时不再打印错误（报告已经写到标准输出）。
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return "exit " + strconv.Itoa(e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

// themeCmd：cohive-server theme check [文件]。管理员上线自定义主题前自查，规则与网页的对比度测试是同一份。
// 退出码：0 全部通过；1 有被拒绝的写法、未知变量或对比度不达标；2 用法不对或文件读不到。
func themeCmd(out io.Writer, args []string) error {
	if len(args) == 0 || args[0] != "check" || len(args) > 2 {
		fmt.Fprint(out, i18n.T("server.theme.usage"))
		return &exitError{code: 2}
	}
	spec, err := theme.LoadSpec()
	if err != nil {
		return &exitError{code: 2, err: err}
	}
	def, err := theme.Defaults()
	if err != nil {
		return &exitError{code: 2, err: err}
	}
	var l *theme.Loaded
	if len(args) == 1 {
		fmt.Fprintln(out, i18n.T("server.theme.default"))
		l = &theme.Loaded{Results: spec.Check(def, theme.Theme{})}
	} else {
		if l, err = theme.Load(spec, def, args[1]); err != nil {
			return &exitError{code: 2, err: err}
		}
		fmt.Fprintln(out, i18n.T("server.theme.file", i18n.D{"Path": args[1]}))
	}
	if n := len(l.Parsed.Rejected); n > 0 {
		fmt.Fprintln(out, "\n"+i18n.T("server.theme.rejected_title"))
		for _, p := range l.Parsed.Rejected {
			fmt.Fprintln(out, "  "+problemLine(p, spec))
		}
	}
	if n := len(l.Parsed.Unknown); n > 0 {
		fmt.Fprintln(out, "\n"+i18n.T("server.theme.unknown_title"))
		for _, p := range l.Parsed.Unknown {
			fmt.Fprintln(out, "  "+problemLine(p, spec))
		}
	}
	zh := i18n.Default() == i18n.ZhCN
	mode := ""
	for _, r := range l.Results {
		if r.Mode != mode {
			mode = r.Mode
			fmt.Fprintln(out, "\n"+i18n.T("server.theme.contrast_title", i18n.D{"Mode": i18n.T("server.theme.mode." + mode)}))
		}
		mark := "ok  "
		if !r.Pass() {
			mark = "FAIL"
		}
		use := r.Pair.En
		if zh {
			use = r.Pair.Zh
		}
		if use != "" {
			use = " (" + use + ")"
		}
		fmt.Fprintf(out, "  %s %5.2f  %s\n", mark, r.Ratio, i18n.T("server.theme.pair."+r.List, i18n.D{
			"FG": "--" + r.Pair.FG, "BG": "--" + r.Pair.BG, "Min": strconv.FormatFloat(r.Min, 'f', -1, 64),
		})+use)
	}
	failed := len(l.Failed())
	rejected, unknown := len(l.Parsed.Rejected), len(l.Parsed.Unknown)
	fmt.Fprintln(out)
	if rejected+unknown+failed == 0 {
		fmt.Fprintln(out, i18n.T("server.theme.result.ok"))
		return nil
	}
	fmt.Fprintln(out, i18n.T("server.theme.result.problems", i18n.D{"Rejected": rejected, "Unknown": unknown, "Failed": failed}))
	return &exitError{code: 1}
}

// problemLine 渲染一处问题：第几行、哪个变量、为什么。
func problemLine(p theme.Problem, spec *theme.Spec) string {
	d := i18n.D{"Line": p.Line, "Name": p.Name, "Detail": p.Detail, "Max": spec.MaxFileBytes}
	msg := i18n.T("server.theme.problem."+p.Code, d)
	if p.Line > 0 {
		return i18n.T("server.theme.line", d) + "  " + msg
	}
	return msg
}

// exitCode 取出命令要求的退出码（普通错误是 1），以及是否还需要打印错误。
func exitCode(err error) (code int, print bool) {
	var e *exitError
	if errors.As(err, &e) {
		return e.code, e.err != nil
	}
	return 1, true
}
