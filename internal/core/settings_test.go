package core_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
)

func TestNormalizeExternalURL(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"  ", ""},
		{"https://cohive.example.com", "https://cohive.example.com"},
		{"HTTPS://Cohive.Example.COM/", "https://cohive.example.com"},
		{"http://10.0.0.5:8787", "http://10.0.0.5:8787"},
		{"http://[::1]:8787/", "http://[::1]:8787"},
	} {
		if got, err := core.NormalizeExternalURL(c.in); err != nil || got != c.want {
			t.Errorf("%q：得到 %q %v，应为 %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{
		"cohive.example.com", "ftp://x.example.com", "https://x.example.com/cohive", "https://x.example.com?a=1",
		"https://x.example.com#top", "https://user:pw@x.example.com", "https://", "https://x.example.com:", "https://x y.com",
		"mailto:a@b.c", "//x.example.com",
	} {
		if got, err := core.NormalizeExternalURL(bad); err == nil {
			t.Errorf("%q 应报错，得到 %q", bad, got)
		}
	}
}

// 对外地址：只有服务器管理员能改；环境变量锁定时不能改；改了立即生效（包括 MCP 白名单用的主机名）；值没变不写记录。
func TestSetExternalURL(t *testing.T) {
	ctx := context.Background()
	f := newAfix(t)
	svc := f.svc
	if ext, err := svc.ExternalURL(ctx); err != nil || ext.URL != "" || ext.Locked || svc.ExternalHost() != "" {
		t.Fatalf("初始应未设置：%+v %v", ext, err)
	}
	if _, err := svc.SetExternalURL(ctx, core.WebActor(f.p.payAdmin), "https://c.example.com"); !errors.Is(err, core.ErrNotServerAdmin) {
		t.Fatalf("工作空间管理员不能改：%v", err)
	}
	if _, err := svc.SetExternalURL(ctx, core.WebActor(f.p.root), "https://c.example.com/x"); err == nil {
		t.Fatal("带路径应报错")
	}
	if ext, err := svc.SetExternalURL(ctx, core.WebActor(f.p.root), "https://C.example.com:8443/"); err != nil || ext.URL != "https://c.example.com:8443" {
		t.Fatalf("保存：%+v %v", ext, err)
	}
	if h := svc.ExternalHost(); h != "c.example.com" {
		t.Fatalf("MCP 白名单的主机名应立即更新：%q", h)
	}
	// 另一个进程（服务器命令行）看得到
	other := &core.Service{Pool: svc.Pool}
	if ext, _ := other.ExternalURL(ctx); ext.URL != "https://c.example.com:8443" {
		t.Fatalf("应写进数据库：%+v", ext)
	}
	if _, err := svc.SetExternalURL(ctx, core.ServerCLI(), "https://c.example.com:8443"); err != nil {
		t.Fatal(err)
	}
	if n := countAction(allEvents(t, svc), core.ActServerSettings); n != 1 {
		t.Fatalf("值没变不应再写记录：%d", n)
	}
	if ext, err := svc.SetExternalURL(ctx, core.ServerCLI(), ""); err != nil || ext.URL != "" || svc.ExternalHost() != "" {
		t.Fatalf("清除：%+v %v", ext, err)
	}

	locked := &core.Service{Pool: svc.Pool, EnvExternalURL: "https://env.example.com"}
	if ext, _ := locked.ExternalURL(ctx); ext.URL != "https://env.example.com" || !ext.Locked || locked.ExternalHost() != "env.example.com" {
		t.Fatalf("环境变量优先并锁定：%+v", ext)
	}
	if _, err := locked.SetExternalURL(ctx, core.WebActor(f.p.root), "https://x.example.com"); !errors.Is(err, core.ErrExternalURLLocked) {
		t.Fatalf("锁定时不能改：%v", err)
	}
	st, err := store.GetServerSettings(ctx, svc.Pool, false)
	if err != nil || st.ExternalURL != "" {
		t.Fatalf("锁定时不应写库：%+v %v", st, err)
	}
}

func TestIssueLinkUsesPath(t *testing.T) {
	if got := core.IssueLink("https://c.example.com", "pay ops", 12); got != "https://c.example.com/w/pay%20ops/issues/12" {
		t.Fatalf("链接：%q", got)
	}
	if got := core.IssueLink("", "pay", 12); got != "" {
		t.Fatalf("没设对外地址时为空：%q", got)
	}
}
