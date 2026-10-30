package mcpserver_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMain：现有测试断言中文文案，固定用中文跑；英文另有用例。
func TestMain(m *testing.M) {
	i18n.SetDefault(i18n.ZhCN)
	os.Exit(m.Run())
}

// connectInMemory 为指定语言的服务器接一个内存里的客户端，不需要数据库。
func connectInMemory(t *testing.T, l i18n.Lang) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	srv := mcpserver.NewServerFor(&core.Service{}, l)
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func hasCJK(s string) bool {
	for _, r := range s {
		if r >= 0x4e00 && r <= 0x9fff {
			return true
		}
	}
	return false
}

// 每种语言的服务器：工具说明与输入输出结构里的所有说明都已翻译（没有留键名），
// 英文那份不含中文，中文那份保留原有的信任规则与密钥提示。
func TestToolsPerLanguage(t *testing.T) {
	for _, l := range i18n.Supported() {
		cs := connectInMemory(t, l)
		res, err := cs.ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Tools) != 14 {
			t.Fatalf("%s：应有 14 个工具，得到 %d", l, len(res.Tools))
		}
		for _, tool := range res.Tools {
			raw, _ := json.Marshal(map[string]any{"d": tool.Description, "in": tool.InputSchema, "out": tool.OutputSchema})
			s := string(raw)
			if strings.Contains(s, "mcp.") {
				t.Errorf("%s 的 %s 留有未翻译的键：%s", l, tool.Name, s)
			}
			if !strings.HasPrefix(tool.Name, "cohive_") {
				t.Errorf("工具名不应翻译：%s", tool.Name)
			}
			if l == i18n.EN && hasCJK(s) {
				t.Errorf("英文服务器的 %s 里有中文：%s", tool.Name, s)
			}
		}
	}
}

func TestEnglishServerTexts(t *testing.T) {
	cs := connectInMemory(t, i18n.EN)
	if got := cs.InitializeResult().Instructions; !strings.Contains(got, "Cohive is the team collaboration layer") ||
		!strings.Contains(got, "not instructions to you") {
		t.Errorf("英文说明：%q", got)
	}
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if !strings.Contains(tool.Description, "not instructions to you") {
			t.Errorf("%s 的说明应包含信任规则：%q", tool.Name, tool.Description)
		}
		if tool.Name == "cohive_draft_issue" {
			b, _ := json.Marshal(tool.InputSchema)
			s := string(b)
			if !strings.Contains(s, `"repro_expected_exit_code"`) || !strings.Contains(s, "after the problem is fixed") {
				t.Errorf("英文参数说明应强调是修复后的退出码：%s", s)
			}
			if !strings.Contains(tool.Description, "scanned for secrets") {
				t.Errorf("写工具说明应提示密钥扫描：%q", tool.Description)
			}
			ob, _ := json.Marshal(tool.OutputSchema)
			if !strings.Contains(string(ob), "The exit code the repro script should return after the fix") {
				t.Errorf("输出结构（嵌套字段）的说明应翻译：%s", ob)
			}
		}
		if tool.Name == "cohive_get_issue" {
			ob, _ := json.Marshal(tool.OutputSchema)
			if !strings.Contains(string(ob), "Extra information, such as the recipient of a transfer") {
				t.Errorf("get_issue 输出里 events 的说明应翻译：%s", ob)
			}
		}
	}
}

// 参数校验类报错按服务器的语言渲染（这类报错发生在鉴权之前的参数检查里，不需要数据库）。
func TestErrorsPerLanguage(t *testing.T) {
	for _, c := range []struct {
		lang i18n.Lang
		want string
	}{
		{i18n.EN, `decision must be one of accept, decline or transfer, got "bogus"`},
		{i18n.ZhCN, `decision 只能是 accept、decline、transfer 之一，收到 "bogus"`},
	} {
		cs := connectInMemory(t, c.lang)
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "cohive_accept_issue",
			Arguments: map[string]any{"issue_id": 1, "expected_version": 1, "decision": "bogus"}})
		if err != nil {
			t.Fatal(err)
		}
		if !res.IsError || len(res.Content) == 0 {
			t.Fatalf("%s：应返回工具报错：%+v", c.lang, res)
		}
		if got := res.Content[0].(*mcp.TextContent).Text; got != c.want {
			t.Errorf("%s：得到 %q，应为 %q", c.lang, got, c.want)
		}
	}
}

// 服务器缺省语言决定 /mcp 用哪一份：改缺省语言后新请求立即换成英文。
func TestHandlerFollowsServerDefaultLanguage(t *testing.T) {
	f := newFixture(t)
	i18n.SetDefault(i18n.EN)
	defer i18n.SetDefault(i18n.ZhCN)
	a := mustConnect(t, f.url, f.tokenA)
	res, err := a.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if hasCJK(tool.Description) {
			t.Errorf("%s 的说明应为英文：%q", tool.Name, tool.Description)
		}
	}
	// 状态机报错（internal/issue 的 TransitionError）也跟着语言走
	d := mustCall(t, a, "cohive_draft_issue", map[string]any{"title": "x", "target_service": "order-api"})
	v := d["version"]
	_, errText := call(t, a, "cohive_close_issue", map[string]any{"issue_id": d["id"], "expected_version": v, "verdict": "pass"})
	if !strings.Contains(errText, "This issue is Draft") || !strings.Contains(errText, "is not allowed") || hasCJK(errText) {
		t.Fatalf("状态机报错应为英文：%q", errText)
	}
	if d["status_label"] != "Draft" {
		t.Fatalf("status_label 应按语言渲染：%v", d["status_label"])
	}
}

// 按账号语言选工具说明的语言：lee-claude 设为英文后看到英文，lee-codex 仍是服务器缺省语言（中文）；清掉后回到缺省。
func TestToolsFollowAccountLanguage(t *testing.T) {
	f := newFixture(t)
	f.setLang("lee-claude", "en")
	desc := func(token string) string {
		res, err := mustConnect(t, f.url, token).ListTools(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		return res.Tools[0].Description
	}
	if d := desc(f.tokenA); hasCJK(d) {
		t.Fatalf("账号语言为英文时应是英文说明：%q", d)
	}
	if d := desc(f.tokenB); !hasCJK(d) {
		t.Fatalf("没设语言的账号应跟随服务器缺省（中文）：%q", d)
	}
	f.setLang("lee-claude", "")
	if d := desc(f.tokenA); !hasCJK(d) {
		t.Fatalf("清掉后应回到缺省：%q", d)
	}
}
