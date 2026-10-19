package mcpserver_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/mcpserver"
)

// 确认策略表与实际注册的工具一一对应：没有漏登记的（注册时会 panic），也没有登记了却不存在的；
// 要确认的工具都不是只读的。
func TestToolPoliciesMatchRegistered(t *testing.T) {
	res, err := connectInMemory(t, i18n.EN).ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	policy := map[string]mcpserver.ToolPolicy{}
	for _, p := range mcpserver.ToolPolicies {
		if _, dup := policy[p.Name]; dup {
			t.Fatalf("ToolPolicies 里 %s 重复", p.Name)
		}
		policy[p.Name] = p
	}
	if len(res.Tools) != len(policy) {
		t.Fatalf("注册了 %d 个工具，策略表有 %d 条", len(res.Tools), len(policy))
	}
	for _, tool := range res.Tools {
		p, ok := policy[tool.Name]
		if !ok {
			t.Fatalf("%s 没有确认策略", tool.Name)
		}
		if p.Confirm && (tool.Annotations == nil || tool.Annotations.ReadOnlyHint) {
			t.Fatalf("%s 是只读工具，不应要求确认", tool.Name)
		}
	}
}
