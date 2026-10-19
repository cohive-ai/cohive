package core_test

import (
	"context"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
)

// 一次配对、补签最多 8 种编程工具：opencode、dsh 这类新工具加进来后仍有余量；第 9 种报错。
func TestDeviceToolLimit(t *testing.T) {
	ctx := context.Background()
	p := newPeople(t)
	svc := p.e.svc
	eight := []string{"claude-code", "codex", "opencode", "dsh", "t5", "t6", "t7", "t8"}
	d := pair(t, svc, p.ann, eight...)
	for _, tool := range eight {
		if tokenOf(d, "pay", tool) == "" {
			t.Fatalf("8 种工具都应签发令牌，缺 %s：%+v", tool, d)
		}
	}
	nine := append(append([]string{}, eight...), "t9")
	_, err := svc.StartDeviceAuth(ctx, nine, "mac2")
	if err == nil || i18n.Message(i18n.ZhCN, err) != "工具数量应为 1 到 8 个" {
		t.Fatalf("第 9 种工具应报错：%v", err)
	}
	dev, _, err := svc.AuthenticateDevice(ctx, d.DeviceSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.IssueWorkspaceTokens(ctx, dev, "pay", nine); err == nil {
		t.Fatal("补签 9 种工具也应报错")
	}
	wt, err := svc.IssueWorkspaceTokens(ctx, dev, "pay", []string{"opencode", "dsh"})
	if err != nil || wt.Tokens["opencode"] == "" || wt.Tokens["dsh"] == "" {
		t.Fatalf("补签 opencode、dsh：%+v %v", wt, err)
	}
	who, err := store.LookupToken(ctx, svc.Pool, wt.Tokens["opencode"])
	if err != nil || who.Tool != "opencode" {
		t.Fatalf("令牌应记为 opencode 工具：%+v %v", who, err)
	}
}
