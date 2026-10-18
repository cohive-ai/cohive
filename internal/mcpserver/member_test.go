package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/store"
)

func TestFindMemberAndNicknameGuardOverMCP(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	for _, m := range [][2]string{{"guanyu", "关羽"}, {"liubei", "刘备"}, {"xuande", "liubei"}} {
		if _, err := store.CreateMember(ctx, f.pool, f.ws, m[0], m[1]); err != nil {
			t.Fatal(err)
		}
	}
	a := mustConnect(t, f.url, f.tokenA)

	out := mustCall(t, a, "cohive_find_member", map[string]any{"query": "关羽"})
	ms := out["members"].([]any)
	if len(ms) != 1 || out["truncated"] != false {
		t.Fatalf("应只查到一位：%v", out)
	}
	m := ms[0].(map[string]any)
	if m["name"] != "guanyu" || m["display_name"] != "关羽" || m["role"] != "member" || m["exact"] != true ||
		len(m["services"].([]any)) != 0 {
		t.Fatalf("查找结果字段不符：%v", m)
	}
	out = mustCall(t, a, "cohive_find_member", map[string]any{"query": "codex"})
	if m := out["members"].([]any)[0].(map[string]any); m["name"] != "lee-codex" || m["exact"] != false ||
		m["services"].([]any)[0] != "order-api" {
		t.Fatalf("包含匹配应带上负责的服务：%v", out)
	}

	// 昵称起草被拦下：报错列出候选并带错误码
	_, errText := call(t, a, "cohive_draft_issue", map[string]any{"title": "t", "owner": "关羽"})
	for _, want := range []string{"guanyu", "关羽", "member_nickname"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("报错应含 %q：%s", want, errText)
		}
	}

	// 英文账号：登录名与别人的昵称相同
	f.setLang("lee-claude", "en")
	a = mustConnect(t, f.url, f.tokenA)
	_, errText = call(t, a, "cohive_draft_issue", map[string]any{"title": "t", "owner": "liubei"})
	for _, want := range []string{"- liubei (display name: 刘备)", "- xuande (display name: liubei)", "(error code: member_ambiguous)"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("英文报错应含 %q：%s", want, errText)
		}
	}

	// 用登录名照旧成功
	d := mustCall(t, a, "cohive_draft_issue", map[string]any{"title": "t", "owner": "xuande"})
	if d["owner"] != "xuande" {
		t.Fatalf("用登录名起草应指派给 xuande：%v", d)
	}
}
