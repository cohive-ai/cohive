package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
)

// 连接链接失效时，提示回网页个人中心重新连接（没有 cohive link 这个命令）。
func TestLinkGoneHintPointsToWeb(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.CompleteTelegramLink(context.Background(), "ln_missing", 1, "x")
	if !errors.Is(err, core.ErrLinkGone) {
		t.Fatalf("应为链接失效：%v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "个人中心") || !strings.Contains(msg, "连接 Telegram") || strings.Contains(msg, "cohive link") {
		t.Fatalf("提示应指向网页个人中心：%s", msg)
	}
}
