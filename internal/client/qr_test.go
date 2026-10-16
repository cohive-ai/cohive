package client

import (
	"strings"
	"testing"
)

// B21：终端二维码固定为白色前景、黑色背景，浅色背景的终端里也不会黑白颠倒。
func TestColorizeQR(t *testing.T) {
	qr, err := QRText("https://cohive.example.com/device/da_abc")
	if err != nil {
		t.Fatal(err)
	}
	out := ColorizeQR(qr)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != len(strings.Split(strings.TrimRight(qr, "\n"), "\n")) {
		t.Fatal("行数应不变")
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "\x1b[97;40m") || !strings.HasSuffix(l, "\x1b[0m") {
			t.Fatalf("每行应以白字黑底开始、以复原结束：%q", l)
		}
	}
	if strings.Contains(qr, "\x1b[") {
		t.Fatal("纯文本二维码不应带颜色")
	}
}
