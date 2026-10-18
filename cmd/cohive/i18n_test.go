package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
)

// TestMain：现有测试断言中文文案，固定用中文跑；英文另有用例。
func TestMain(m *testing.M) {
	i18n.SetDefault(i18n.ZhCN)
	os.Exit(m.Run())
}

func TestUsageEnglish(t *testing.T) {
	i18n.SetDefault(i18n.EN)
	defer i18n.SetDefault(i18n.ZhCN)
	var out bytes.Buffer
	err := runCLI(context.Background(), strings.NewReader(""), &out, []string{"bogus"})
	if err == nil || err.Error() != `unknown command "bogus"` {
		t.Fatalf("err = %v", err)
	}
	if !strings.HasPrefix(out.String(), "Usage: cohive <command>") {
		t.Fatalf("usage = %q", out.String())
	}
}
