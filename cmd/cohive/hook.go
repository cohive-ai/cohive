package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cohive-ai/cohive/internal/client"
	"github.com/cohive-ai/cohive/internal/i18n"
)

// 会话钩子的时限：编程工具每次提交提示词都要等它，离线或服务挂起时不能拖慢会话。
var (
	hookDeadline    = 2 * time.Second         // 整个钩子
	hookHTTPTimeout = 1500 * time.Millisecond // 其中的网络请求
	hookThrottle    = 20 * time.Second        // 同一会话两次联网的最短间隔
	hookStateMaxAge = 7 * 24 * time.Hour
	hookNow         = time.Now
)

// hookInput 是两家编程工具传给钩子的标准输入里我们用到的字段（两家字段名相同）。
type hookInput struct {
	SessionID string `json:"session_id"`
	Event     string `json:"hook_event_name"`
	Source    string `json:"source"`
}

type hookOutput struct {
	SystemMessage string `json:"systemMessage"`
	Specific      struct {
		Event   string `json:"hookEventName"`
		Context string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// runHook 是 cohive hook：供编程工具的会话开始、提交提示词钩子调用。
// 永远不返回错误、不打印诊断（Claude Code 的提交提示词钩子退出码 2 会拦下用户的提示词）：
// 有新动态时输出一个 JSON，其余情况（离线、超时、没有登录、没有新动态）什么都不输出。
func runHook(ctx context.Context, in io.Reader, out io.Writer, args []string) {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	server := fs.String("server", "", "")
	workspace := fs.String("workspace", "", "")
	tool := fs.String("tool", "", "")
	check := fs.Bool("check", false, "")
	if fs.Parse(args) != nil || *server == "" || *workspace == "" || !isHookTool(*tool) {
		if *check {
			fmt.Fprintln(out, i18n.T("cli.hook.check_usage"))
		}
		return
	}
	if *check {
		hookCheck(ctx, out, *server, *workspace, *tool)
		return
	}

	ctx, cancel := context.WithTimeout(ctx, hookDeadline)
	defer cancel()
	done := make(chan []byte, 1)
	go func() {
		var b []byte
		defer func() {
			_ = recover() // 任何意外都只是“没有提醒”
			done <- b
		}()
		b = hookOnce(ctx, in, *server, *workspace, *tool)
	}()
	select {
	case b := <-done:
		if len(b) > 0 {
			_, _ = out.Write(b)
		}
	case <-ctx.Done():
	}
}

func hookOnce(ctx context.Context, in io.Reader, server, workspace, tool string) []byte {
	raw, err := io.ReadAll(io.LimitReader(in, 1<<20))
	if err != nil {
		return nil
	}
	var hi hookInput
	if json.Unmarshal(raw, &hi) != nil || (hi.Event != "SessionStart" && hi.Event != "UserPromptSubmit") {
		return nil
	}
	creds, err := client.LoadCredentials()
	if err != nil {
		return nil
	}
	login, ok := creds.Find(server, workspace, tool)
	if !ok {
		return nil
	}
	statePath, err := client.HookStatePath(tool, hi.SessionID)
	if err != nil {
		return nil
	}
	fetch := func(after *int64) (client.Alerts, bool) {
		c, cancel := context.WithTimeout(ctx, hookHTTPTimeout)
		defer cancel()
		res, _, err := client.FetchAlerts(c, server, workspace, login.Token, after)
		return res, err == nil
	}
	now := hookNow()
	state, have := client.LoadHookState(statePath)
	if have && (state.Server != client.NormalizeServer(server) || state.Workspace != workspace) {
		have = false
	}

	throttled := have && now.Sub(state.CheckedAt) < hookThrottle && !now.Before(state.CheckedAt)
	if !have || state.Cursor < 0 {
		// 这个会话还没有游标（新会话，或钩子是中途装上的）：给待办摘要，游标从现在算起。
		// 游标先记为 -1（未取到）：服务不可达时，之后的提示词不会从 0 开始把旧提醒全翻出来
		if hi.Event != "SessionStart" && throttled {
			return nil
		}
		client.PruneHookStates(hookStateMaxAge)
		base := client.HookState{Server: client.NormalizeServer(server), Workspace: workspace, Cursor: -1, CheckedAt: now}
		_ = client.SaveHookState(statePath, base)
		res, ok := fetch(nil)
		if !ok {
			return nil
		}
		base.Cursor = res.Cursor
		for _, it := range res.Items { // 最近写入、已算进摘要的提醒：之后补查时不再重复
			base.Saw(it.ID)
		}
		_ = client.SaveHookState(statePath, base)
		return renderPending(hi.Event, workspace, tool, res)
	}

	// 已经有游标的会话再次“会话开始”（恢复、清空、压缩之后）：摘要已经说过，不再重复，只补查新动态
	if throttled && hi.Event != "SessionStart" {
		return nil
	}
	// 先记下这次查询的时间：服务挂起时，后面的提示词也不会每次都等一遍
	state.CheckedAt = now
	_ = client.SaveHookState(statePath, state)
	after := state.Cursor
	res, ok := fetch(&after)
	if !ok {
		return nil
	}
	var fresh []client.AlertItem
	for _, it := range res.Items {
		if it.ID <= 0 || state.HasSeen(it.ID) {
			continue
		}
		state.Saw(it.ID)
		fresh = append(fresh, it)
	}
	if res.Cursor > state.Cursor {
		state.Cursor = res.Cursor
	}
	_ = client.SaveHookState(statePath, state)
	return renderAlerts(hi.Event, workspace, tool, fresh, res.More)
}

// isHookTool：cohive init 配置的工具之外，还接受 dsh（DeepSeek Harness 用户按手工接入说明自己配钩子）。
func isHookTool(t string) bool { return isKnownTool(t) || t == "dsh" }

var loginName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// safeActor 只放行登录名的字符集；服务端给的不像登录名时不原样输出。
func safeActor(s string) string {
	if loginName.MatchString(s) {
		return s
	}
	return i18n.T("cli.hook.some_member")
}

// safeText 去掉换行与控制字符并限长：待办文案是服务端的固定文字，这里再防一道。
func safeText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max]) + "…"
	}
	return s
}

func alertPhrase(it client.AlertItem) string {
	d := i18n.D{"ID": it.IssueID, "Who": safeActor(it.Actor)}
	switch it.Kind {
	case "assigned":
		return i18n.T("cli.hook.alert_assigned", d)
	case "declined":
		return i18n.T("cli.hook.alert_declined", d)
	case "verify":
		return i18n.T("cli.hook.alert_verify", d)
	case "reopened":
		return i18n.T("cli.hook.alert_reopened", d)
	case "escalated":
		return i18n.T("cli.hook.alert_escalated", d)
	case "accepted":
		return i18n.T("cli.hook.alert_accepted", d)
	case "closed":
		return i18n.T("cli.hook.alert_closed", d)
	case "overdue":
		return i18n.T("cli.hook.alert_overdue", d)
	case "transferred":
		return i18n.T("cli.hook.alert_transferred", d)
	case "started":
		return i18n.T("cli.hook.alert_started", d)
	case "commented":
		return i18n.T("cli.hook.alert_commented", d)
	case "reassigned":
		return i18n.T("cli.hook.alert_reassigned", d)
	case "cancelled":
		return i18n.T("cli.hook.alert_cancelled", d)
	case "watching":
		d["Event"] = hookEventName(it.About)
		if it.Actor == "" { // 超时催办之类系统动作没有操作者
			return i18n.T("cli.hook.alert_watching_system", d)
		}
		return i18n.T("cli.hook.alert_watching", d)
	case "mentioned":
		return i18n.T("cli.hook.alert_mentioned", d)
	}
	return i18n.T("cli.hook.alert_other", d)
}

// hookEventName 是关注提醒里的事件短名；服务端发来不认识的事件名用“有新动态”。
func hookEventName(about string) string {
	switch about {
	case "assigned":
		return i18n.T("cli.hook.event.assigned")
	case "declined":
		return i18n.T("cli.hook.event.declined")
	case "verify":
		return i18n.T("cli.hook.event.verify")
	case "reopened":
		return i18n.T("cli.hook.event.reopened")
	case "escalated":
		return i18n.T("cli.hook.event.escalated")
	case "overdue":
		return i18n.T("cli.hook.event.overdue")
	case "overdue_admin":
		return i18n.T("cli.hook.event.overdue_admin")
	case "accepted":
		return i18n.T("cli.hook.event.accepted")
	case "closed":
		return i18n.T("cli.hook.event.closed")
	case "transferred":
		return i18n.T("cli.hook.event.transferred")
	case "started":
		return i18n.T("cli.hook.event.started")
	case "commented":
		return i18n.T("cli.hook.event.commented")
	case "reassigned":
		return i18n.T("cli.hook.event.reassigned")
	case "cancelled":
		return i18n.T("cli.hook.event.cancelled")
	}
	return i18n.T("cli.hook.event.other")
}

// slashCommand 是提醒里给的调用写法：Codex 的技能用 $cohive，其余（Claude Code、opencode、dsh）用 /cohive。
func slashCommand(tool string, issueID int64) string {
	if tool == "codex" {
		return fmt.Sprintf("$cohive %d", issueID)
	}
	return fmt.Sprintf("/cohive %d", issueID)
}

func hookJSON(event, human, model string) []byte {
	var o hookOutput
	o.SystemMessage = human
	o.Specific.Event = event
	o.Specific.Context = model
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if enc.Encode(o) != nil {
		return nil
	}
	return buf.Bytes()
}

// hookAsk 是给模型的第二行：Cohive 钩子（用户自己装的本机配置）要求模型把 say 原样告诉用户。
// 只给模型看不够：Codex 桌面版不显示 systemMessage，模型若不说，人就完全看不到；
// 要写在本轮最终答复里：Codex 桌面版在本轮结束后会折叠中途的进度说明；
// 而且放在末尾：回复常常很长，放在开头容易被略过（lee 2026-10-17）。
func hookAsk(say string) string {
	return i18n.T("cli.hook.ask", i18n.D{"Say": say})
}

func renderAlerts(event, workspace, tool string, items []client.AlertItem, more bool) []byte {
	if len(items) == 0 {
		return nil
	}
	const show = 5
	var parts []string
	for i, it := range items {
		if i == show {
			break
		}
		parts = append(parts, alertPhrase(it))
	}
	if len(items) > show || more {
		parts = append(parts, i18n.T("cli.hook.and_more"))
	}
	line := strings.Join(parts, i18n.T("cli.hook.sep"))
	cmd := slashCommand(tool, items[0].IssueID)
	// 开头固定是 Cohive（英文 Cohive reminder，中文 Cohive 提醒）：会话开始时还有别的插件注入的大段内容，要让人和模型一眼认出；
	// 技能文件只认“以 Cohive 开头的提醒”，不依赖具体语言
	human := i18n.T("cli.hook.alerts_human", i18n.D{"Line": line, "Workspace": workspace})
	model := i18n.T("cli.hook.alerts_model", i18n.D{"Workspace": workspace, "Line": line, "Trust": i18n.T("cli.hook.trust_note"),
		"Ask": hookAsk(i18n.T("cli.hook.alerts_say", i18n.D{"Line": line, "Cmd": cmd}))})
	return hookJSON(event, human, model)
}

func renderPending(event, workspace, tool string, res client.Alerts) []byte {
	if res.Pending.Count == 0 || len(res.Pending.Top) == 0 {
		return nil
	}
	var long []string
	for _, p := range res.Pending.Top {
		long = append(long, fmt.Sprintf("#%d %s", p.IssueID, safeText(p.Needs, 40)))
	}
	rest := ""
	if res.Pending.Count > len(res.Pending.Top) {
		rest = i18n.T("cli.hook.pending_rest")
	}
	list := strings.Join(long, i18n.T("cli.hook.sep")) + rest
	cmd := slashCommand(tool, res.Pending.Top[0].IssueID)
	human := i18n.T("cli.hook.pending_human", i18n.D{"Count": res.Pending.Count, "List": list, "Workspace": workspace})
	model := i18n.T("cli.hook.pending_model", i18n.D{"Count": res.Pending.Count, "List": list, "Workspace": workspace,
		"Trust": i18n.T("cli.hook.trust_note"),
		"Ask":   hookAsk(i18n.T("cli.hook.pending_say", i18n.D{"Count": res.Pending.Count, "List": list, "Cmd": cmd}))})
	return hookJSON(event, human, model)
}

// hookCheck 手动排查“为什么没有提醒”：打印每一步的结果，不写游标。
func hookCheck(ctx context.Context, out io.Writer, server, workspace, tool string) {
	creds, err := client.LoadCredentials()
	if err != nil {
		fmt.Fprint(out, i18n.T("cli.hook.check_creds_failed", i18n.D{"Err": i18n.Message(i18n.Default(), err)}))
		return
	}
	login, ok := creds.Find(server, workspace, tool)
	if !ok {
		fmt.Fprint(out, i18n.T("cli.hook.check_no_login", i18n.D{"Tool": tool, "Workspace": workspace, "Server": server}))
		return
	}
	fmt.Fprint(out, i18n.T("cli.hook.check_found", i18n.D{"Workspace": workspace, "Member": login.Member, "Tool": tool}))
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	start := time.Now()
	res, code, err := client.FetchAlerts(c, server, workspace, login.Token, nil)
	if err != nil {
		fmt.Fprint(out, i18n.T("cli.hook.check_fetch_failed", i18n.D{"Code": code, "Took": time.Since(start).Round(time.Millisecond).String(),
			"Err": i18n.Message(i18n.Default(), err)}))
		return
	}
	fmt.Fprint(out, i18n.T("cli.hook.check_ok", i18n.D{"Took": time.Since(start).Round(time.Millisecond).String(),
		"Count": res.Pending.Count, "Cursor": res.Cursor}))
	if b := renderPending("SessionStart", workspace, tool, res); b != nil {
		fmt.Fprint(out, i18n.T("cli.hook.check_would_insert"), string(b))
	} else {
		fmt.Fprintln(out, i18n.T("cli.hook.check_nothing"))
	}
	if dir, err := client.HookStateDir(); err == nil {
		fmt.Fprint(out, i18n.T("cli.hook.check_state_dir", i18n.D{"Dir": dir}))
	}
}
