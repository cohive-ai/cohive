package clidist_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"testing/fstest"
	"unicode"

	"github.com/cohive-ai/cohive/internal/clidist"
	"github.com/cohive-ai/cohive/internal/i18n"
)

func hasHan(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func getHdr(t *testing.T, url string, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

// 安装脚本的语言：?lang= > Accept-Language > 服务器缺省语言。
func TestScriptLanguage(t *testing.T) {
	srv := serve(t, fakeDist(t, false))

	// 服务器缺省语言是中文（TestMain）：不带任何语言信息时是中文
	if _, s := getHdr(t, srv.URL+"/install.sh", nil); !strings.Contains(s, "正在从") || strings.Contains(s, "Downloading") {
		t.Fatalf("缺省应是服务器缺省语言（中文）：\n%s", s)
	}
	resp, s := getHdr(t, srv.URL+"/install.sh?lang=en", nil)
	if !strings.Contains(s, "Downloading cohive ${version} (") || hasHan(s) {
		t.Fatalf("?lang=en 应是纯英文脚本：\n%s", s)
	}
	if resp.Header.Get("Content-Language") != "en" {
		t.Fatalf("Content-Language = %q", resp.Header.Get("Content-Language"))
	}
	// 请求头里的语言，服务器缺省语言改成英文
	i18n.SetDefault(i18n.EN)
	defer i18n.SetDefault(i18n.ZhCN)
	if _, s := getHdr(t, srv.URL+"/install.sh", nil); !strings.Contains(s, "Downloading") || hasHan(s) {
		t.Fatalf("服务器缺省语言是英文时，不带参数应是英文：\n%s", s)
	}
	if _, s := getHdr(t, srv.URL+"/install.sh", map[string]string{"Accept-Language": "zh-CN,zh;q=0.9"}); !strings.Contains(s, "正在从") {
		t.Fatalf("Accept-Language 应能选中文：\n%s", s)
	}
	if _, s := getHdr(t, srv.URL+"/install.sh?lang=zh-CN", map[string]string{"Accept-Language": "en"}); !strings.Contains(s, "正在从") {
		t.Fatalf("?lang 应优先于 Accept-Language：\n%s", s)
	}
	if _, s := getHdr(t, srv.URL+"/install.sh?lang=xx-nonsense", nil); !strings.Contains(s, "Downloading") {
		t.Fatalf("认不出的 lang 应退回缺省语言：\n%s", s)
	}
	for _, p := range []string{"/install.ps1?lang=zh-CN"} {
		if _, s := getHdr(t, srv.URL+p, nil); !strings.Contains(s, "正在从") {
			t.Fatalf("%s 应是中文：\n%s", p, s)
		}
	}
	if _, s := getHdr(t, srv.URL+"/install.ps1", nil); !strings.Contains(s, "Downloading cohive") || hasHan(s) {
		t.Fatalf("/install.ps1 缺省（英文）应是纯英文：\n%s", s)
	}
}

// 各种语言、各种状态（有安装包、没有安装包、没有 Windows 包）渲染出的脚本：没有残留的占位、哨兵，sh 语法合法。
func TestScriptsWellFormedInEveryLanguage(t *testing.T) {
	noWindows := func() *clidist.Dist {
		m := clidist.Manifest{Version: "1.2.3", Files: map[string]clidist.File{
			"linux-amd64": {Name: "cohive", GZ: "x.gz", Size: 1, SHA256: strings.Repeat("a", 64)}}}
		b, _ := json.Marshal(m)
		d, err := clidist.New(fstest.MapFS{clidist.ManifestName: &fstest.MapFile{Data: b}, "x.gz": &fstest.MapFile{Data: []byte("x")}})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}()
	none, _ := clidist.New(fstest.MapFS{})
	dists := map[string]*clidist.Dist{"full": fakeDist(t, false), "none": none, "no-windows": noWindows}
	for name, d := range dists {
		srv := serve(t, d)
		for _, l := range i18n.Supported() {
			for _, p := range []string{"/install.sh", "/install.ps1"} {
				_, s := getHdr(t, srv.URL+p+"?lang="+string(l), nil)
				if strings.Contains(s, "<no value>") || strings.Contains(s, "\x00") || strings.Contains(s, "{{") {
					t.Errorf("%s %s %s：脚本里有没填上的占位：\n%s", name, l, p, s)
				}
				if (l == i18n.EN) && hasHan(s) {
					t.Errorf("%s %s %s：英文脚本里不应有汉字", name, l, p)
				}
				if p == "/install.sh" {
					cmd := exec.Command("sh", "-n")
					cmd.Stdin = strings.NewReader(s)
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Errorf("%s %s：sh 语法检查不通过：%v\n%s\n%s", name, l, err, out, s)
					}
				}
			}
		}
	}
}

// 英文脚本端到端：成功安装时的输出是英文，校验失败、不认识的参数的报错也是英文。
func TestInstallScriptEnglishEndToEnd(t *testing.T) {
	i18n.SetDefault(i18n.EN)
	defer i18n.SetDefault(i18n.ZhCN)
	_, out, err := runInstall(t, serve(t, fakeDist(t, false)))
	if err != nil {
		t.Fatalf("安装应成功：%v\n%s", err, out)
	}
	for _, want := range []string{"Downloading cohive 9.9.9-test", "Installed: ", "Version: cohive 9.9.9-test", "is not in your PATH", "Next: pair this computer", "cohive login --server http://127.0.0.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应包含 %q：\n%s", want, out)
		}
	}
	if hasHan(out) {
		t.Errorf("英文输出里不应有汉字：\n%s", out)
	}
	_, out, err = runInstall(t, serve(t, fakeDist(t, true)))
	if err == nil || !strings.Contains(out, "Checksum mismatch (expected ") || !strings.Contains(out, "nothing was installed") {
		t.Fatalf("校验和被篡改应报英文错误：%v\n%s", err, out)
	}
	_, out, err = runInstall(t, serve(t, fakeDist(t, false)), "--bogus")
	if err == nil || !strings.Contains(out, "Error: Unknown argument --bogus (only --login is supported)") {
		t.Fatalf("不认识的参数应报英文错误：%v\n%s", err, out)
	}
}

// 没有安装包的提示与下载接口的报错按请求的语言。
func TestMessagesFollowRequestLanguage(t *testing.T) {
	none, _ := clidist.New(fstest.MapFS{})
	srv := serve(t, none)
	en := map[string]string{"Accept-Language": "en"}

	_, s := getHdr(t, srv.URL+"/install.sh?lang=en", nil)
	out, err := exec.Command("sh", "-c", s).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "no CLI install bundle") {
		t.Fatalf("没有安装包时英文脚本应报英文错误：%v %s", err, out)
	}
	_, ps := getHdr(t, srv.URL+"/install.ps1?lang=en", nil)
	if !strings.Contains(ps, `throw "This server has no CLI install bundle`) {
		t.Fatalf("PowerShell 脚本应提示：%s", ps)
	}
	resp, body := getHdr(t, srv.URL+"/dl/cohive/linux-amd64", en)
	if resp.StatusCode != 404 || !strings.Contains(body, "no CLI install bundle") || hasHan(body) {
		t.Fatalf("下载接口应用英文报错：%d %s", resp.StatusCode, body)
	}
	if resp, body := getHdr(t, srv.URL+"/dl/other", en); resp.StatusCode != 404 || !strings.Contains(body, "No such file") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	full := serve(t, fakeDist(t, false))
	if resp, body := getHdr(t, full.URL+"/dl/cohive/plan9-386", en); resp.StatusCode != 404 ||
		!strings.Contains(body, "No install package for plan9-386; available platforms: darwin-arm64, darwin-amd64") {
		t.Fatalf("%d %s", resp.StatusCode, body)
	}
	req, _ := http.NewRequest(http.MethodGet, full.URL+"/install.sh", nil)
	req.Host = "a$(id)"
	req.Header.Set("Accept-Language", "en")
	r, err := http.DefaultClient.Do(req)
	if err == nil {
		defer r.Body.Close()
		b, _ := io.ReadAll(r.Body)
		if r.StatusCode != http.StatusBadRequest || !strings.Contains(string(b), "The Host of the request is not valid") {
			t.Fatalf("%d %s", r.StatusCode, b)
		}
	}
}

// 启动时打印的一行说明是服务器日志，一律英文，不随语言变化。
func TestSummaryIsEnglish(t *testing.T) {
	none, _ := clidist.New(fstest.MapFS{})
	if s := none.Summary(); hasHan(s) || !strings.Contains(s, "make cli-dist") {
		t.Fatal(s)
	}
	if s := fakeDist(t, false).Summary(); hasHan(s) || !strings.Contains(s, "version 9.9.9-test, 5 platforms") {
		t.Fatal(s)
	}
}

var _ = httptest.NewServer
