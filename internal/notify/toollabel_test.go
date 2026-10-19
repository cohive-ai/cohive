package notify

import "testing"

// 群通知里“经哪个编程工具”的显示名：已知工具用产品名，其他（例如手工接入的 dsh）原样显示。
func TestToolLabel(t *testing.T) {
	for tool, want := range map[string]string{"claude-code": "Claude Code", "codex": "Codex", "opencode": "OpenCode", "dsh": "dsh"} {
		if got := toolLabel(tool); got != want {
			t.Errorf("toolLabel(%q) = %q，应为 %q", tool, got, want)
		}
	}
}
