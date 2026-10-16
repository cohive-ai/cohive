package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/client"
)

// B17：--force 重新配对（需管理员批准）成功后，自动吊销本机旧设备，并清掉旧设备上没重登的工具条目。
func TestForceRepairRevokesOldDevice(t *testing.T) {
	configDir(t)
	fastDevice(t)
	deviceAdminWait = time.Minute
	s := newTestServer(t)
	s.login(t, "codex", "claude-code")
	creds, _ := client.LoadCredentials()
	old, _ := creds.Find(s.url, "default", "codex")

	ctx := context.Background()
	go func() {
		s.approve(t, true) // 浏览器里授权：非首台，进入等管理员批准
		for i := 0; i < 300; i++ {
			var id int64
			if err := s.svc.Pool.QueryRow(ctx, `SELECT id FROM device_auths WHERE status = 'awaiting_admin'`).Scan(&id); err == nil {
				if _, err := s.svc.DecideDeviceAuth(ctx, nil, 0, id, true); err != nil {
					t.Error(err)
				}
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Error("没有等到待批准的设备")
	}()
	out, err := run(t, "login", "--server", s.url, "--tools", "codex", "--force", "--open=false")
	if err != nil {
		t.Fatalf("重新配对：%v\n%s", err, out)
	}
	var revoked bool
	if err := s.svc.Pool.QueryRow(ctx, `SELECT revoked_at IS NOT NULL FROM devices WHERE id = $1`, old.DeviceID).Scan(&revoked); err != nil || !revoked {
		t.Fatalf("旧设备应被吊销：%v %v\n%s", revoked, err, out)
	}
	creds, _ = client.LoadCredentials()
	if _, ok := creds.Find(s.url, "default", "claude-code"); ok {
		t.Fatalf("旧设备上的 claude-code 条目应清掉：%+v", creds)
	}
	if l, ok := creds.Find(s.url, "default", "codex"); !ok || l.DeviceID == old.DeviceID {
		t.Fatalf("codex 应换到新设备：%+v", l)
	}
	if !strings.Contains(out, "cohive login --tools claude-code") {
		t.Fatalf("应提示补登 claude-code：%s", out)
	}
}
