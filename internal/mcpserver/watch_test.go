package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/store"
)

func names(v any) []string {
	var out []string
	arr, _ := v.([]any)
	for _, x := range arr {
		switch e := x.(type) {
		case string:
			out = append(out, e)
		case map[string]any:
			out = append(out, e["name"].(string))
		}
	}
	return out
}

// 抄送、昵称拦截、自行关注与取消、移除别人被拒、留言里未识别的 @、归档后关注报错。
func TestWatchersOverMCP(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	// lee-claude 是发起人，lee-codex 是 order-api 的负责人；再加两位成员
	for _, m := range [][2]string{{"guanyu", "关羽"}, {"zhangfei", "张飞"}} {
		if _, err := store.CreateMember(ctx, f.pool, f.ws, m[0], m[1]); err != nil {
			t.Fatal(err)
		}
	}
	a := mustConnect(t, f.url, f.tokenA)
	b := mustConnect(t, f.url, f.tokenB)

	// 抄送昵称：被拦下，带候选与错误码
	_, errText := call(t, a, "cohive_draft_issue", map[string]any{"title": "t", "target_service": "order-api", "cc": []string{"关羽"}})
	for _, want := range []string{"guanyu", "member_nickname"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("抄送昵称的报错应含 %q：%s", want, errText)
		}
	}

	// 起草时带 cc，发出时再抄送一位；结果里带 watchers
	d := mustCall(t, a, "cohive_draft_issue", map[string]any{"title": "下单 500", "target_service": "order-api", "cc": []string{"guanyu"}})
	id, v := idv(d)
	o := mustCall(t, a, "cohive_submit_issue", map[string]any{"issue_id": id, "expected_version": v, "cc": []string{"zhangfei"}})
	if w := names(o["watchers"]); len(w) != 2 || w[0] != "guanyu" || w[1] != "zhangfei" {
		t.Fatalf("发出后应有 guanyu、zhangfei 两位关注者：%v", o["watchers"])
	}
	g := mustCall(t, a, "cohive_get_issue", map[string]any{"issue_id": id})
	if w := names(g["issue"].(map[string]any)["watchers"]); len(w) != 2 {
		t.Fatalf("get_issue 应带关注者：%v", g)
	}

	// 负责人自己关注、取消；返回登录名与昵称
	r := mustCall(t, b, "cohive_watch_issue", map[string]any{"issue_id": id, "watch": true})
	if len(names(r["watchers"])) != 3 {
		t.Fatalf("负责人关注后应有 3 位：%v", r)
	}
	r = mustCall(t, b, "cohive_watch_issue", map[string]any{"issue_id": id, "watch": false})
	if w := names(r["watchers"]); len(w) != 2 {
		t.Fatalf("取消后应剩 2 位：%v", r)
	}
	if first := r["watchers"].([]any)[0].(map[string]any); first["display_name"] != "关羽" {
		t.Fatalf("关注者应带昵称：%v", first)
	}

	// 负责人（非发起人）移除别人：被拒；昵称：被拦
	if _, e := call(t, b, "cohive_watch_issue", map[string]any{"issue_id": id, "watch": false, "members": []string{"guanyu"}}); e == "" {
		t.Fatal("非发起人移除别人应被拒")
	}
	if _, e := call(t, a, "cohive_watch_issue", map[string]any{"issue_id": id, "watch": true, "members": []string{"张飞"}}); !strings.Contains(e, "member_nickname") {
		t.Fatalf("昵称应返回 member_nickname：%s", e)
	}
	// 发起人移除别人：成功
	r = mustCall(t, a, "cohive_watch_issue", map[string]any{"issue_id": id, "watch": false, "members": []string{"zhangfei"}})
	if w := names(r["watchers"]); len(w) != 1 || w[0] != "guanyu" {
		t.Fatalf("发起人移除后应只剩 guanyu：%v", r)
	}

	// 留言：@登录名 识别；@不存在的名字 未识别，结果里列出并带提示
	c := mustCall(t, b, "cohive_update_issue", map[string]any{"issue_id": id, "note": "请 @zhangfei 看下，也问问 @nobody"})
	um := names(c["unknown_mentions"])
	if len(um) != 1 || um[0] != "@nobody" || c["hint"] == "" || c["hint"] == nil {
		t.Fatalf("应列出未识别的 @nobody 并给提示：%v", c)
	}
	c = mustCall(t, b, "cohive_update_issue", map[string]any{"issue_id": id, "note": "没有 @ 任何人"})
	if _, ok := c["unknown_mentions"]; ok {
		t.Fatalf("没有未识别时应省略 unknown_mentions：%v", c)
	}
	if _, ok := c["hint"]; ok {
		t.Fatalf("没有未识别时应省略 hint：%v", c)
	}
	if w := names(c["watchers"]); len(w) != 1 {
		t.Fatalf("留言结果应带关注者：%v", c)
	}

	// 归档后关注报错
	by := int64(1)
	if err := store.SetWorkspaceArchived(ctx, f.pool, f.ws, &by); err != nil {
		t.Fatal(err)
	}
	if _, e := call(t, b, "cohive_watch_issue", map[string]any{"issue_id": id, "watch": true}); e == "" {
		t.Fatal("归档后关注应报错")
	}
}
