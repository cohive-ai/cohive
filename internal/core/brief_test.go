package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
)

func TestDraftBrief(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	is, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "x", TargetService: "order-api",
		Brief: "  下单接口对空购物车返回 500，\n前端联调卡住  "})
	if err != nil {
		t.Fatal(err)
	}
	if is.Brief != "下单接口对空购物车返回 500， 前端联调卡住" {
		t.Fatalf("简述应去掉首尾空白并把换行合成空格，得到 %q", is.Brief)
	}

	_, err = e.svc.Draft(ctx, e.a, core.DraftInput{Title: "x", Brief: strings.Repeat("长", 81)})
	if err == nil || !strings.Contains(err.Error(), "80") {
		t.Fatalf("超过 80 字的简述应报错，得到 %v", err)
	}
	if _, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "x", Brief: strings.Repeat("长", 80)}); err != nil {
		t.Fatalf("正好 80 字应允许：%v", err)
	}
}
