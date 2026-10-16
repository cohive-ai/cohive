package main

import (
	"context"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/client"
)

// B16：设备被吊销后，cohive web 与补签工具都提示 cohive login --force 重新配对。
func TestRevokedDeviceHintsForce(t *testing.T) {
	configDir(t)
	fastDevice(t)
	s := newTestServer(t)
	s.login(t, "codex")
	creds, _ := client.LoadCredentials()
	l, _ := creds.Find(s.url, "default", "codex")
	if _, err := s.svc.Pool.Exec(context.Background(), `UPDATE devices SET revoked_at = now() WHERE id = $1`, l.DeviceID); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "web", "--open=false"); err == nil || !strings.Contains(err.Error(), "cohive login --force") {
		t.Fatalf("cohive web 应提示重新配对：%v", err)
	}
	if _, err := run(t, "login", "--server", s.url, "--tools", "codex,claude-code"); err == nil || !strings.Contains(err.Error(), "cohive login --force") {
		t.Fatalf("补签工具应提示重新配对：%v", err)
	}
}
