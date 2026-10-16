package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/client"
)

// 等管理员批准时中途退出（Ctrl+C）：进度已保存，批准后 --resume 能领到。
func TestLoginInterruptedThenResume(t *testing.T) {
	configDir(t)
	fastDevice(t)
	deviceAdminWait = time.Hour // 不靠超时保存
	s := newTestServer(t)
	s.login(t, "codex")

	configDir(t) // 另一台电脑
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		s.approve(t, true)
		// 等终端进入“等管理员批准”后再中断
		for i := 0; i < 300; i++ {
			var n int
			_ = s.svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM device_auths WHERE status = 'awaiting_admin'`).Scan(&n)
			if n > 0 {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	var buf bytes.Buffer
	if err := runCLI(ctx, strings.NewReader(""), &buf, []string{"login", "--server", s.url, "--tools", "codex", "--open=false"}); err == nil {
		t.Fatalf("中断后应返回错误：\n%s", buf.String())
	}
	if list, _ := client.LoadPendings(); len(list) != 1 {
		t.Fatalf("中断时应已保存待领取记录：%+v", list)
	}
	s.adminApprove(t)
	out, err := run(t, "login", "--resume")
	if err != nil || !strings.Contains(out, "已登录") {
		t.Fatalf("批准后续领：%v\n%s", err, out)
	}
	if list, _ := client.LoadPendings(); len(list) != 0 {
		t.Fatalf("领到后应删除记录：%+v", list)
	}
}

// 网络连续失败退出：保留待领取记录，提示稍后 --resume。
func TestLoginNetworkFailureKeepsPending(t *testing.T) {
	configDir(t)
	fastDevice(t)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/device/start") {
			_, _ = w.Write([]byte(`{"request_id":7,"poll_secret":"s","expires_in":900,"url":"/device/da_abc"}`))
			return
		}
		http.Error(w, "坏了", http.StatusBadGateway)
	}))
	defer fake.Close()
	_, err := run(t, "login", "--server", fake.URL, "--open=false")
	if err == nil || !strings.Contains(err.Error(), "--resume") {
		t.Fatalf("网络失败应提示稍后续领：%v", err)
	}
	list, _ := client.LoadPendings()
	if len(list) != 1 || list[0].RequestID != 7 || list[0].Server != fake.URL {
		t.Fatalf("应保留这条待领取记录：%+v", list)
	}
}

// 多条待领取记录互不覆盖，删除只删自己那条。
func TestPendingRecordsIndependent(t *testing.T) {
	configDir(t)
	for _, p := range []client.Pending{{Server: "https://a.example.com", RequestID: 1, PollSecret: "x"},
		{Server: "https://b.example.com", RequestID: 1, PollSecret: "y"}, {Server: "https://a.example.com", RequestID: 2, PollSecret: "z"}} {
		if err := client.SavePending(p); err != nil {
			t.Fatal(err)
		}
	}
	if list, _ := client.LoadPendings(); len(list) != 3 {
		t.Fatalf("三条记录应各自保存：%+v", list)
	}
	client.ClearPending("https://a.example.com", 1)
	list, _ := client.LoadPendings()
	if len(list) != 2 {
		t.Fatalf("只删自己那条：%+v", list)
	}
	for _, p := range list {
		if p.Server == "https://a.example.com" && p.RequestID == 1 {
			t.Fatal("删掉的记录还在")
		}
	}
}

// B20：在已配置好 Cohive 的仓库里续领，提示仓库已配置好，不再让人执行 cohive init。
func TestResumeInConfiguredRepo(t *testing.T) {
	configDir(t)
	fastDevice(t)
	s := newTestServer(t)
	s.login(t, "codex")
	configDir(t) // 另一台电脑
	go s.approve(t, true)
	if _, err := run(t, "login", "--server", s.url, "--tools", "codex", "--open=false"); err == nil {
		t.Fatal("等管理员超时应先退出")
	}
	s.adminApprove(t)
	repo := t.TempDir()
	writeRepoConfig(t, repo, s.url)
	out, err := run(t, "login", "--resume", "--dir", repo)
	if err != nil || !strings.Contains(out, "已经配置好") || strings.Contains(out, "执行 cohive init") {
		t.Fatalf("仓库已配置好时不应再提示 cohive init：%v\n%s", err, out)
	}
}
