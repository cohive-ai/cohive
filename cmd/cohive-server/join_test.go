package main

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/db"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestJoinCommands(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	mustRun(t, "workspace", "notify", "--workspace", "default", "--telegram-chat", "-1007")

	pool, err := pgxpool.New(context.Background(), databaseURLForTest(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	svc := &core.Service{Pool: pool}
	if _, err := svc.RequestJoin(context.Background(), "-1007", 601, "erin", "Erin"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RequestJoin(context.Background(), "-1007", 602, "frank", "Frank"); err != nil {
		t.Fatal(err)
	}

	out := mustRun(t, "join", "list")
	if !strings.Contains(out, "erin") || !strings.Contains(out, "frank") || !strings.Contains(out, "待审") {
		t.Fatalf("应列出待审申请：%s", out)
	}
	mustRun(t, "join", "approve", "--id", "1")
	mustRun(t, "join", "reject", "--id", "2")
	if out := mustRun(t, "member", "list"); !strings.Contains(out, "erin") || strings.Contains(out, "frank") {
		t.Fatalf("批准的应成为成员，拒绝的不应：%s", out)
	}
	if _, err := runCmd(t, "join", "approve", "--id", "1"); err == nil {
		t.Fatal("已处理的申请不能再处理")
	}
	if _, err := store.MemberByName(context.Background(), pool, 1, "erin"); err != nil {
		t.Fatalf("erin 应在默认工作空间：%v", err)
	}
}

func databaseURLForTest(t *testing.T) string {
	t.Helper()
	u, err := databaseURL()
	if err != nil {
		t.Fatal(err)
	}
	return u
}
