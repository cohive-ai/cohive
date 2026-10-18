package notify_test

import (
	"os"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
)

// TestMain：现有测试断言中文文案，固定用中文跑；英文另有用例。
func TestMain(m *testing.M) {
	i18n.SetDefault(i18n.ZhCN)
	os.Exit(m.Run())
}
