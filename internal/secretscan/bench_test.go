package secretscan

import (
	"strings"
	"testing"
)

// 一段约 1 MB 的普通日志（含 token、key 等字样，会进入通用规则的正则），衡量最坏情况的扫描耗时。
func BenchmarkScanLog1MB(b *testing.B) {
	line := "2026-10-17T03:30:00Z level=error msg=\"refresh token failed\" request_id=" + uuidLike() +
		" api=/v1/orders key_count=3 status=500 duration=12ms\n"
	text := strings.Repeat(line, 1<<20/len(line))
	rules()
	b.SetBytes(int64(len(text)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := Scan(Field{Name: "evidence", Text: text}); len(got) != 0 {
			b.Fatalf("普通日志不应命中：%+v", got[0])
		}
	}
}
