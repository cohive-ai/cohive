package main

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/mcpserver"
)

// cohive init 给三家编程工具配的确认清单，必须与协作服务实际注册的工具一致：
// 会改变别人工作的（策略表标了 Confirm）都在 confirmTools，其余都在 allowTools，只读的一定在 allowTools；
// 两张清单不重叠、不重复，也没有服务端不存在的工具名。漏了任何一个，三家都会直接放行，这里会失败。
func TestConfirmListsMatchServer(t *testing.T) {
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := mcpserver.NewServerFor(&core.Service{}, i18n.EN).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	listed := map[string]string{}
	for _, l := range []struct {
		name  string
		tools []string
	}{{"confirmTools", confirmTools}, {"allowTools", allowTools}} {
		for _, n := range l.tools {
			if prev, dup := listed[n]; dup {
				t.Fatalf("%s 同时出现在 %s 和 %s", n, prev, l.name)
			}
			listed[n] = l.name
		}
	}
	confirm := map[string]bool{}
	for _, p := range mcpserver.ToolPolicies {
		confirm[p.Name] = p.Confirm
	}
	registered := map[string]bool{}
	for _, tool := range res.Tools {
		registered[tool.Name] = true
		want := "allowTools"
		if confirm[tool.Name] {
			want = "confirmTools"
		}
		if listed[tool.Name] != want {
			t.Errorf("%s 应在 %s，实际在 %q", tool.Name, want, listed[tool.Name])
		}
		if tool.Annotations != nil && tool.Annotations.ReadOnlyHint && listed[tool.Name] != "allowTools" {
			t.Errorf("只读工具 %s 应直接放行", tool.Name)
		}
	}
	for n, where := range listed {
		if !registered[n] {
			t.Errorf("%s 里的 %s 不是协作服务注册的工具", where, n)
		}
	}
}
