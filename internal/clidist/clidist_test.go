package clidist_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/cohive-ai/cohive/internal/clidist"
)

// 假“程序”：一段 shell 脚本，安装后能响应 cohive version。
const fakeBinary = "#!/bin/sh\necho 'cohive 9.9.9-test'\n"

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// fakeDist 造一套五个平台内容相同的安装包；tamper 为真时清单里的校验和是错的。
func fakeDist(t *testing.T, tamper bool) *clidist.Dist {
	t.Helper()
	fsys := fstest.MapFS{}
	m := clidist.Manifest{Version: "9.9.9-test", Commit: "abc1234", Files: map[string]clidist.File{}}
	for _, p := range clidist.Platforms {
		name := "cohive"
		if strings.HasPrefix(p, "windows") {
			name = "cohive.exe"
		}
		file := "cohive-" + p + ".gz"
		fsys[file] = &fstest.MapFile{Data: gz(t, []byte(fakeBinary))}
		s := sum([]byte(fakeBinary))
		if tamper {
			s = sum([]byte("被篡改"))
		}
		m.Files[p] = clidist.File{Name: name, GZ: file, Size: int64(len(fakeBinary)), SHA256: s}
	}
	b, _ := json.Marshal(m)
	fsys[clidist.ManifestName] = &fstest.MapFile{Data: b}
	d, err := clidist.New(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func serve(t *testing.T, d *clidist.Dist) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	d.Register(mux, func(r *http.Request) string { return "http://" + r.Host }, "9.9.9-test", "abc1234")
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, b
}

func TestDownloadAndVersion(t *testing.T) {
	srv := serve(t, fakeDist(t, false))
	resp, b := get(t, srv.URL+"/dl/cohive/linux-arm64")
	if resp.StatusCode != 200 || string(b) != fakeBinary || resp.Header.Get("Content-Length") != fmt.Sprint(len(fakeBinary)) {
		t.Fatalf("应返回解压后的原始文件：%d %q %s", resp.StatusCode, b, resp.Header.Get("Content-Length"))
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, `filename="cohive"`) {
		t.Fatalf("下载文件名：%s", cd)
	}
	if resp, _ := get(t, srv.URL+"/dl/cohive/windows-amd64"); !strings.Contains(resp.Header.Get("Content-Disposition"), "cohive.exe") {
		t.Fatal("Windows 安装包应叫 cohive.exe")
	}
	_, sums := get(t, srv.URL+"/dl/cohive/SHA256SUMS")
	if lines := strings.Split(strings.TrimSpace(string(sums)), "\n"); len(lines) != 5 || !strings.HasPrefix(lines[0], sum([]byte(fakeBinary))+"  cohive-darwin-arm64") {
		t.Fatalf("SHA256SUMS 格式：%s", sums)
	}
	if resp, b := get(t, srv.URL+"/dl/cohive/plan9-386"); resp.StatusCode != 404 || !strings.Contains(string(b), "darwin-arm64") {
		t.Fatalf("未知平台应 404 并列出提供的平台：%d %s", resp.StatusCode, b)
	}
	if resp, _ := get(t, srv.URL+"/dl/other"); resp.StatusCode != 404 {
		t.Fatalf("/dl/ 下的其他路径应 404：%d", resp.StatusCode)
	}
	_, vb := get(t, srv.URL+"/api/v1/version")
	var v struct {
		Version string
		CLI     struct {
			Version   string
			Platforms []string
		}
	}
	if err := json.Unmarshal(vb, &v); err != nil || v.Version != "9.9.9-test" || v.CLI.Version != "9.9.9-test" || len(v.CLI.Platforms) != 5 {
		t.Fatalf("版本接口：%s %v", vb, err)
	}
}

// 计划 6.2 第 7 条：没有附带安装包时，接口与脚本给出明确提示。
func TestNoDist(t *testing.T) {
	d, err := clidist.New(fstest.MapFS{".gitkeep": &fstest.MapFile{}})
	if err != nil || d.Manifest != nil {
		t.Fatalf("只有占位文件时应视为没有安装包：%v", err)
	}
	srv := serve(t, d)
	if resp, b := get(t, srv.URL+"/dl/cohive/linux-amd64"); resp.StatusCode != 404 || !strings.Contains(string(b), "make cli-dist") {
		t.Fatalf("应提示没有附带安装包：%d %s", resp.StatusCode, b)
	}
	_, vb := get(t, srv.URL+"/api/v1/version")
	if !strings.Contains(string(vb), `"cli":null`) {
		t.Fatalf("版本接口的 cli 应为 null：%s", vb)
	}
	_, script := get(t, srv.URL+"/install.sh")
	out, err := exec.Command("sh", "-c", string(script)).CombinedOutput()
	if err == nil || !strings.Contains(string(out), "没有附带命令行安装包") {
		t.Fatalf("没有安装包时脚本应报错退出：%v %s", err, out)
	}
	_, ps := get(t, srv.URL+"/install.ps1")
	if !strings.Contains(string(ps), "throw '这台服务器没有附带命令行安装包") {
		t.Fatalf("PowerShell 脚本也应提示：%s", ps)
	}
}

func TestManifestValidation(t *testing.T) {
	bad := func(m clidist.Manifest) error {
		b, _ := json.Marshal(m)
		_, err := clidist.New(fstest.MapFS{clidist.ManifestName: &fstest.MapFile{Data: b}, "x.gz": &fstest.MapFile{Data: []byte("x")}})
		return err
	}
	f := clidist.File{Name: "cohive", GZ: "x.gz", Size: 1, SHA256: strings.Repeat("a", 64)}
	if err := bad(clidist.Manifest{Version: "1.0'; rm -rf ~; '", Files: map[string]clidist.File{"linux-amd64": f}}); err == nil {
		t.Fatal("版本号带引号等字符应拒绝（会写进安装脚本）")
	}
	if err := bad(clidist.Manifest{Version: "1.0", Files: map[string]clidist.File{"linux-amd64": {Name: "cohive", GZ: "x.gz", Size: 1, SHA256: "zz"}}}); err == nil {
		t.Fatal("校验和格式不对应拒绝")
	}
	if err := bad(clidist.Manifest{Version: "1.0", Files: map[string]clidist.File{"linux-amd64": {Name: "cohive", GZ: "missing.gz", Size: 1, SHA256: strings.Repeat("a", 64)}}}); err == nil {
		t.Fatal("清单里的文件不存在应拒绝")
	}
	if err := bad(clidist.Manifest{Version: "1.0", Files: map[string]clidist.File{"plan9-386": f}}); err != nil {
		if !strings.Contains(err.Error(), "未知平台") {
			t.Fatal(err)
		}
	} else {
		t.Fatal("未知平台应拒绝")
	}
}

// 计划 6.2 第 9 条：服务地址只取自本次请求，Host 含奇怪字符时拒绝生成脚本。
func TestScriptUsesRequestOriginOnly(t *testing.T) {
	srv := serve(t, fakeDist(t, false))
	_, script := get(t, srv.URL+"/install.sh")
	if !strings.Contains(string(script), "server='"+srv.URL+"'") {
		t.Fatalf("脚本应写入本次请求的地址：%s", script)
	}
	for _, line := range strings.Split(string(script), "\n") {
		if strings.Contains(line, "://") && !strings.Contains(line, srv.URL) && !strings.Contains(line, "http://localhost*") &&
			!strings.Contains(line, "http://*") {
			t.Errorf("脚本里不应出现别的地址：%s", line)
		}
	}
	for _, host := range []string{"evil';curl x|sh;'", "a$(id)", "a b", "x`id`"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/install.sh", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			continue // 客户端本身拒绝发送这种 Host 也可以
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("Host %q 应被拒绝，得到 %d", host, resp.StatusCode)
		}
	}
}

func runInstall(t *testing.T, srv *httptest.Server, args ...string) (dir, out string, err error) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("安装脚本只用于 macOS、Linux")
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("需要 curl")
	}
	home := t.TempDir()
	dir = filepath.Join(home, "bin")
	cmd := exec.Command("sh", "-c", "curl -fsSL "+srv.URL+"/install.sh | sh -s -- "+strings.Join(args, " "))
	cmd.Env = []string{"HOME=" + home, "COHIVE_INSTALL_DIR=" + dir, "PATH=/usr/bin:/bin:/usr/sbin:/sbin"}
	b, err := cmd.CombinedOutput()
	return dir, string(b), err
}

// 计划 6.2 第 8 条：端到端跑一次 curl | sh，校验通过才安装；校验和被篡改时不安装。
func TestInstallScriptEndToEnd(t *testing.T) {
	dir, out, err := runInstall(t, serve(t, fakeDist(t, false)))
	if err != nil {
		t.Fatalf("安装应成功：%v\n%s", err, out)
	}
	b, err := os.ReadFile(filepath.Join(dir, "cohive"))
	if err != nil || string(b) != fakeBinary {
		t.Fatalf("应装上下载的文件：%v %q", err, b)
	}
	if fi, _ := os.Stat(filepath.Join(dir, "cohive")); fi.Mode().Perm()&0o111 == 0 {
		t.Fatal("装好的文件应可执行")
	}
	for _, want := range []string{"cohive 9.9.9-test", "不在 PATH 里", "cohive login --server http://127.0.0.1"} {
		if !strings.Contains(out, want) {
			t.Errorf("输出应包含 %q：\n%s", want, out)
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("安装目录里只应有 cohive，得到 %v", entries)
	}

	dir, out, err = runInstall(t, serve(t, fakeDist(t, true)))
	if err == nil || !strings.Contains(out, "校验和不符") {
		t.Fatalf("校验和被篡改时应报错退出：%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "cohive")); !os.IsNotExist(err) {
		t.Fatalf("校验失败时不应安装：%v", err)
	}
}

// 装完接着配对：有终端时用装好的程序执行 cohive login，没有终端时提示手动执行；不认识的参数直接报错。
func TestInstallScriptLogin(t *testing.T) {
	srv := serve(t, fakeDist(t, false))
	dir, out, err := runInstall(t, srv, "--login")
	if err != nil {
		t.Fatalf("安装应成功：%v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "cohive")); err != nil {
		t.Fatalf("应先装好：%v", err)
	}
	// 测试进程不一定有终端：有就接着执行装好的程序（假程序会再打印一次版本），没有就提示手动执行
	tty := strings.Contains(out, "接着配对这台电脑") && strings.Count(out, "cohive 9.9.9-test") == 2
	noTTY := strings.Contains(out, "没有可交互的终端") && strings.Contains(out, "cohive login --server "+srv.URL)
	if !tty && !noTTY {
		t.Fatalf("--login 应接着配对或提示手动配对：\n%s", out)
	}

	_, out, err = runInstall(t, srv, "--bogus")
	if err == nil || !strings.Contains(out, "不认识的参数 --bogus") {
		t.Fatalf("不认识的参数应报错：%v\n%s", err, out)
	}
}

func TestPowerShellScriptContent(t *testing.T) {
	srv := serve(t, fakeDist(t, false))
	_, ps := get(t, srv.URL+"/install.ps1")
	s := string(ps)
	for _, want := range []string{"$server = '" + srv.URL + "'", "$want = '" + sum([]byte(fakeBinary)) + "'",
		"Get-FileHash -Algorithm SHA256", "/dl/cohive/windows-amd64", "SetEnvironmentVariable('Path', $newPath, 'User')",
		"if ($env:COHIVE_LOGIN -eq '1')", "login --server $server"} {
		if !strings.Contains(s, want) {
			t.Errorf("PowerShell 脚本应包含 %q", want)
		}
	}
}
