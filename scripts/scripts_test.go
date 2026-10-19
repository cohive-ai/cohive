// 测试服脚本的测试：真实 Postgres 上备份与轮换，启动脚本只动自己记下的进程、不泄露机器人令牌。
package scripts_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/cohive-ai/cohive/internal/testutil"
	"github.com/jackc/pgx/v5"
)

func needBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("脚本只用于 macOS、Linux")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("需要 bash")
	}
}

// script 在干净的环境里运行脚本：只给定的环境变量，HOME 指向临时目录，避免读到本机真实配置。
// 现有断言都是中文提示，缺省固定 COHIVE_LANG=zh-CN；env 里给出 COHIVE_LANG（含空值）则以它为准。
func script(t *testing.T, name string, env map[string]string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{name}, args...)...)
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "COHIVE_LANG=zh-CN"}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	b, err := cmd.CombinedOutput()
	return string(b), err
}

// tempDatabase 建一个随机命名的库并写入一点数据，测试结束删除。
func tempDatabase(t *testing.T) (url, name string) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, testutil.DatabaseURL())
	if err != nil {
		t.Fatalf("连接测试数据库失败（先执行 make db-up）：%v", err)
	}
	defer admin.Close(ctx)
	buf := make([]byte, 5)
	_, _ = rand.Read(buf)
	name = "cohive_bk_" + hex.EncodeToString(buf)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, err := pgx.Connect(context.Background(), testutil.DatabaseURL())
		if err == nil {
			_, _ = c.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
			c.Close(context.Background())
		}
	})
	base := testutil.DatabaseURL()
	i := strings.LastIndex(base, "/")
	q := ""
	if j := strings.Index(base[i:], "?"); j >= 0 {
		q = base[i+j:]
	}
	url = base[:i+1] + name + q
	c, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, "CREATE TABLE t (v text); INSERT INTO t VALUES ('备份测试')"); err != nil {
		t.Fatal(err)
	}
	return url, name
}

// backupEnv 选择备份方式：开发库容器在运行就用容器里的 pg_dump（本机客户端常比服务器旧），否则用本机的。
func backupEnv(t *testing.T, url, dir string) map[string]string {
	t.Helper()
	env := map[string]string{"HOME": t.TempDir(), "COHIVE_DATABASE_URL": url, "COHIVE_BACKUP_DIR": dir, "COHIVE_BACKUP_KEEP": "2"}
	if out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", "cohive-dev-postgres").Output(); err == nil && strings.TrimSpace(string(out)) == "true" {
		env["COHIVE_PG_CONTAINER"] = "cohive-dev-postgres"
	} else if _, err := exec.LookPath("pg_dump"); err != nil {
		t.Skip("既没有开发库容器也没有 pg_dump")
	}
	return env
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// 计划 6.2 第 10 条：备份成功后只删同库、同命名规则的旧文件；备份失败时一个都不删、不留半截文件。
func TestBackupRotation(t *testing.T) {
	needBash(t)
	url, db := tempDatabase(t)
	dir := t.TempDir()
	touch := func(n string) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("旧备份"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := []string{"cohive-" + db + "-20260101-000000.dump", "cohive-" + db + "-20260102-000000.dump", "cohive-" + db + "-20260103-000000.dump"}
	for _, n := range old {
		touch(n)
	}
	others := []string{"cohive-otherdb-20250101-000000.dump", "notes.txt", "cohive-" + db + "-手工.dump"}
	for _, n := range others {
		touch(n)
	}
	env := backupEnv(t, url, dir)

	out, err := script(t, "backup-db.sh", env)
	if err != nil {
		t.Fatalf("备份应成功：%v\n%s", err, out)
	}
	if strings.Contains(out, "cohive:cohive") || strings.Contains(out, url) {
		t.Fatalf("输出不应包含数据库密码：%s", out)
	}
	got := names(t, dir)
	var fresh string
	for _, n := range got {
		if strings.HasPrefix(n, "cohive-"+db+"-2026") && !contains(old, n) {
			fresh = n
		}
	}
	if fresh == "" {
		t.Fatalf("应写出新备份：%v\n%s", got, out)
	}
	want := append([]string{fresh, old[2]}, others...)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("保留 2 份：应剩新备份与最近一份旧备份，其他文件不动\n得到 %v\n期望 %v", got, want)
	}
	if !strings.Contains(out, "删除旧备份 2 份") {
		t.Fatalf("输出应说明删了几份：%s", out)
	}
	// 备份能恢复出数据（用与备份相同的方式读目录）
	list := exec.Command("pg_restore", "--list", filepath.Join(dir, fresh))
	if c := env["COHIVE_PG_CONTAINER"]; c != "" {
		list = exec.Command("docker", "exec", "-i", c, "pg_restore", "--list")
		f, _ := os.Open(filepath.Join(dir, fresh))
		defer f.Close()
		list.Stdin = f
	}
	if b, err := list.CombinedOutput(); err != nil || !strings.Contains(string(b), "TABLE public t") {
		t.Fatalf("备份里应有表 t：%v\n%s", err, b)
	}

	// 失败：库不存在。旧备份一个都不删，不留 .partial
	before := names(t, dir)
	bad := map[string]string{}
	for k, v := range env {
		bad[k] = v
	}
	bad["COHIVE_DATABASE_URL"] = strings.Replace(url, db, db+"_missing", 1)
	bad["COHIVE_BACKUP_KEEP"] = "1"
	if out, err := script(t, "backup-db.sh", bad); err == nil {
		t.Fatalf("库不存在时应失败：%s", out)
	}
	if after := names(t, dir); strings.Join(after, ",") != strings.Join(before, ",") {
		t.Fatalf("备份失败时不应删除或新增任何文件：前 %v 后 %v", before, after)
	}
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// fakeServer 编译一个假的 cohive-server：serve 时提供 /healthz，并在输出里记下“是否拿到机器人令牌”（不记值）。
func fakeServer(t *testing.T, dir string) string {
	t.Helper()
	src := filepath.Join(dir, "fake", "main.go")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	code := `package main

import (
	"fmt"
	"net/http"
	"os"
)

func main() {
	if len(os.Args) != 4 || os.Args[1] != "serve" || os.Args[2] != "--addr" {
		os.Exit(2)
	}
	set := ""
	if os.Getenv("COHIVE_TELEGRAM_BOT_TOKEN") != "" {
		set = "yes"
	}
	fmt.Println("TOKEN_SET=" + set, "DATA_DIR_SET=" + fmt.Sprint(os.Getenv("COHIVE_DATA_DIR") != ""),
		"DT_SET=" + fmt.Sprint(os.Getenv("COHIVE_DINGTALK_CLIENT_SECRET") != ""),
		"SL_SET=" + fmt.Sprint(os.Getenv("COHIVE_SLACK_BOT_TOKEN") != "" && os.Getenv("COHIVE_SLACK_APP_TOKEN") != ""))
	http.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprintln(w, "ok") })
	fmt.Println(http.ListenAndServe(os.Args[3], nil))
}
`
	if err := os.WriteFile(src, []byte(code), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "cohive-server")
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Dir = filepath.Dir(src)
	cmd.Env = append(os.Environ(), "GO111MODULE=off")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("编译假服务：%v\n%s", err, b)
	}
	return bin
}

// 计划 6.2 第 11 条与派活要求：启动、状态、停止只针对自己记下的进程；端口被占时不动别人；令牌只进服务进程、不进日志与输出。
func TestServerScriptLifecycle(t *testing.T) {
	needBash(t)
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("需要 lsof")
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "cohive-home")
	port := freePort(t)
	token := "123456789:AA" + strings.Repeat("x", 33)
	tg := filepath.Join(tmp, "telegram.env")
	if err := os.WriteFile(tg, []byte("export COHIVE_TELEGRAM_BOT_TOKEN="+token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 配置走 server.env：数据库地址里的密码不应出现在任何输出里
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	conf := fmt.Sprintf("COHIVE_DATABASE_URL=postgres://u:pw-in-url@localhost:1/demo\nCOHIVE_ADDR=\"127.0.0.1:%d\"\n", port)
	if err := os.WriteFile(filepath.Join(home, "server.env"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	dtSecret := "dt-secret-" + strings.Repeat("y", 24)
	dt := filepath.Join(tmp, "dingtalk.env")
	if err := os.WriteFile(dt, []byte("export COHIVE_DINGTALK_CLIENT_ID=dingabc123\nexport COHIVE_DINGTALK_CLIENT_SECRET="+dtSecret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	slBot, slApp := "xoxb-"+strings.Repeat("b", 30), "xapp-"+strings.Repeat("a", 30)
	sl := filepath.Join(tmp, "slack.env")
	if err := os.WriteFile(sl, []byte("export COHIVE_SLACK_BOT_TOKEN="+slBot+"\nexport COHIVE_SLACK_APP_TOKEN="+slApp+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": tmp, "COHIVE_HOME": home, "COHIVE_BIN": fakeServer(t, tmp), "COHIVE_TELEGRAM_ENV": tg,
		"COHIVE_DINGTALK_ENV": dt, "COHIVE_SLACK_ENV": sl}

	out, err := script(t, "cohive-server.sh", env, "start")
	if err != nil {
		t.Fatalf("启动应成功：%v\n%s", err, out)
	}
	t.Cleanup(func() { _, _ = script(t, "cohive-server.sh", env, "stop") })
	pidB, err := os.ReadFile(filepath.Join(home, "run", "server.pid"))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := script(t, "cohive-server.sh", env, "status"); err != nil || !strings.Contains(out, "运行中") || !strings.Contains(out, "demo") {
		t.Fatalf("状态应为运行中并显示库名：%v\n%s", err, out)
	}
	if out, _ := script(t, "cohive-server.sh", env, "start"); !strings.Contains(out, "已在运行") {
		t.Fatalf("重复启动应提示已在运行：%s", out)
	}
	logB, _ := os.ReadFile(filepath.Join(home, "logs", "server.log"))
	all := out + string(logB)
	if !strings.Contains(string(logB), "TOKEN_SET=yes DATA_DIR_SET=true DT_SET=true SL_SET=true") {
		t.Fatalf("机器人令牌、钉钉凭据与 Slack 令牌应传给服务进程：%s", logB)
	}
	if strings.Contains(all, token[:20]) || strings.Contains(all, "pw-in-url") || strings.Contains(all, dtSecret) ||
		strings.Contains(all, slBot) || strings.Contains(all, slApp) {
		t.Fatalf("输出与日志里不应有令牌或数据库密码：%s", all)
	}
	if fi, _ := os.Stat(filepath.Join(home, "data")); fi == nil || !fi.IsDir() {
		t.Fatal("应建好数据目录")
	}

	out, err = script(t, "cohive-server.sh", env, "stop")
	if err != nil || !strings.Contains(out, "已停止") {
		t.Fatalf("停止应成功：%v\n%s", err, out)
	}
	if exec.Command("kill", "-0", strings.TrimSpace(string(pidB))).Run() == nil {
		t.Fatal("服务进程应已退出")
	}

	// 进程号文件指向别的程序：stop 拒绝动手
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Process.Kill(); _ = other.Wait() }()
	if err := os.WriteFile(filepath.Join(home, "run", "server.pid"), []byte(fmt.Sprint(other.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := script(t, "cohive-server.sh", env, "stop"); err == nil || !strings.Contains(out, "不会动它") {
		t.Fatalf("进程号文件指向别的程序时 stop 应拒绝：%v\n%s", err, out)
	}
	if other.Process.Signal(syscallZero()) != nil {
		t.Fatal("别的程序不应被结束")
	}
	_ = os.Remove(filepath.Join(home, "run", "server.pid"))

	// 端口被别的程序占着：start 报出占用者并退出，不结束它
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if out, err := script(t, "cohive-server.sh", env, "start"); err == nil || !strings.Contains(out, "已被进程") {
		t.Fatalf("端口被占时应报错退出：%v\n%s", err, out)
	}
	l.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}
}

// 装了 launchd 常驻服务后，start/stop/restart 改走 launchctl：先停掉之前后台起的那份，任何时候只跑一个服务。
// 用假的 launchctl 模拟装载、拉起、kickstart -k 与卸下，不碰本机真实的 launchd。
func TestServerScriptHandsOverToLaunchd(t *testing.T) {
	needBash(t)
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("需要 lsof")
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "cohive-home")
	ld := filepath.Join(tmp, "launchd")
	fakeBin := filepath.Join(tmp, "fakebin")
	for _, d := range []string{home, ld, fakeBin} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	port := freePort(t)
	conf := fmt.Sprintf("COHIVE_DATABASE_URL=postgres://u:p@localhost:1/demo\nCOHIVE_ADDR=127.0.0.1:%d\n", port)
	if err := os.WriteFile(filepath.Join(home, "server.env"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	self, err := filepath.Abs("cohive-server.sh")
	if err != nil {
		t.Fatal(err)
	}
	fake := `#!/bin/bash
d="$FAKE_LD_DIR"; echo "$*" >> "$d/calls"
alive() { [ -f "$d/pid" ] && kill -0 "$(cat "$d/pid")" 2>/dev/null; }
spawn() { bash "$FAKE_LD_SCRIPT" run >> "$d/server.log" 2>&1 < /dev/null & echo $! > "$d/pid"; }
halt() {
  if alive; then p="$(cat "$d/pid")"; kill "$p"; while kill -0 "$p" 2>/dev/null; do sleep 0.1; done; fi
  rm -f "$d/pid"
}
case "$1" in
  print) [ -f "$d/loaded" ] || exit 113; echo "state = running"; if alive; then printf '\tpid = %s\n' "$(cat "$d/pid")"; fi ;;
  bootstrap) touch "$d/loaded"; spawn ;;
  kickstart) [ -f "$d/loaded" ] || exit 113; if [ "$2" = -k ]; then halt; spawn; elif ! alive; then spawn; fi ;;
  bootout) halt; rm -f "$d/loaded" ;;
esac
`
	if err := os.WriteFile(filepath.Join(fakeBin, "launchctl"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(tmp, "server.plist")
	env := map[string]string{
		"HOME": tmp, "COHIVE_HOME": home, "COHIVE_BIN": fakeServer(t, tmp),
		"COHIVE_TELEGRAM_ENV": filepath.Join(tmp, "none"), "COHIVE_LAUNCHD_PLIST": plist,
		"FAKE_LD_DIR": ld, "FAKE_LD_SCRIPT": self, "PATH": fakeBin + ":" + os.Getenv("PATH"),
	}
	pidOf := func() string {
		b, _ := os.ReadFile(filepath.Join(ld, "pid"))
		return strings.TrimSpace(string(b))
	}
	alive := func(pid string) bool { return pid != "" && exec.Command("kill", "-0", pid).Run() == nil }
	t.Cleanup(func() { _, _ = script(t, "cohive-server.sh", env, "stop") })

	// 还没装 plist：照旧后台启动
	if out, err := script(t, "cohive-server.sh", env, "start"); err != nil {
		t.Fatalf("后台启动应成功：%v\n%s", err, out)
	}
	b, _ := os.ReadFile(filepath.Join(home, "run", "server.pid"))
	oldPid := strings.TrimSpace(string(b))

	// 装上 plist 后 start：先停掉后台那份，再交给 launchd
	if err := os.WriteFile(plist, []byte("<plist/>"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := script(t, "cohive-server.sh", env, "start")
	if err != nil || !strings.Contains(out, "不是 launchd 拉起的") || !strings.Contains(out, "已由 launchd 启动") {
		t.Fatalf("应先停后台进程再由 launchd 拉起：%v\n%s", err, out)
	}
	if alive(oldPid) {
		t.Fatal("后台起的旧进程应已停止")
	}
	first := pidOf()
	if !alive(first) {
		t.Fatalf("launchd 应已拉起服务：%s", out)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "run", "server.pid")); strings.TrimSpace(string(b)) != first {
		t.Fatalf("run 应把自己的进程号写进进程号文件：%q，launchd 记录 %q", b, first)
	}

	// 再 start：不会多起一份
	if out, err := script(t, "cohive-server.sh", env, "start"); err != nil || pidOf() != first {
		t.Fatalf("重复 start 不应换进程：%v\n%s", err, out)
	}
	if out, err := script(t, "cohive-server.sh", env, "status"); err != nil || !strings.Contains(out, "由 launchd 托管") {
		t.Fatalf("状态应说明由 launchd 托管：%v\n%s", err, out)
	}

	// restart 即 kickstart -k：换成新进程，旧的退出
	if out, err := script(t, "cohive-server.sh", env, "restart"); err != nil {
		t.Fatalf("重启应成功：%v\n%s", err, out)
	}
	second := pidOf()
	if second == first || !alive(second) || alive(first) {
		t.Fatalf("重启后应换成新进程：旧 %s，新 %s", first, second)
	}

	// stop 即从 launchd 卸下
	if out, err := script(t, "cohive-server.sh", env, "stop"); err != nil || !strings.Contains(out, "从 launchd 卸下") {
		t.Fatalf("停止应卸下 launchd 服务：%v\n%s", err, out)
	}
	if alive(second) {
		t.Fatal("停止后服务进程应已退出")
	}
	calls, _ := os.ReadFile(filepath.Join(ld, "calls"))
	for _, want := range []string{"bootstrap", "kickstart -k", "bootout"} {
		if !strings.Contains(string(calls), want) {
			t.Fatalf("应调用 launchctl %s：\n%s", want, calls)
		}
	}
}

func syscallZero() os.Signal { return syscall.Signal(0) }

// 2026-10-17 事故：launchd 下 LANG=en_US.UTF-8，macOS 自带的 bash 3.2 把“$addr）”里的多字节字符当成变量名的一部分，
// set -u 下报 unbound variable，服务起不来。变量名后紧跟非 ASCII 字符时必须写成 ${名字}。
var bareVarBeforeNonASCII = regexp.MustCompile(`\$[A-Za-z_][A-Za-z0-9_]*[^\x00-\x7F]`)

func TestShellVarsBracedBeforeNonASCII(t *testing.T) {
	files := []string{filepath.Join("..", "internal", "clidist", "install.sh.tmpl")}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".sh") {
			files = append(files, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 5 {
		t.Fatalf("应扫描到 scripts 下的全部 .sh 与安装脚本模板，只找到 %v", files)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(b), "\n") {
			if m := bareVarBeforeNonASCII.FindString(line); m != "" {
				t.Errorf("%s:%d：%q 后面紧跟非 ASCII 字符，改成 ${名字}：%s", f, i+1, m, strings.TrimSpace(line))
			}
		}
	}
}

// 实际用 macOS 自带的 bash（没有就用 PATH 里的 bash）在 UTF-8 语言环境下跑一次 run 子命令，它会打印“监听 ${addr}）”。
func TestServerScriptRunsUnderUTF8Locale(t *testing.T) {
	needBash(t)
	bash := "/bin/bash"
	if _, err := os.Stat(bash); err != nil {
		bash = "bash"
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "cohive-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "server.env"),
		[]byte("COHIVE_DATABASE_URL=postgres://u:p@localhost:1/demo\nCOHIVE_ADDR=127.0.0.1:18799\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(tmp, "cohive-server")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"fake $*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, "cohive-server.sh", "run")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tmp, "COHIVE_HOME=" + home, "COHIVE_BIN=" + fake,
		"COHIVE_TELEGRAM_ENV=" + filepath.Join(tmp, "none.env"), "COHIVE_LAUNCHD_PLIST=" + filepath.Join(tmp, "none.plist"),
		"COHIVE_LANG=zh-CN", "LANG=en_US.UTF-8", "LC_ALL=en_US.UTF-8"}
	b, err := cmd.CombinedOutput()
	out := string(b)
	if err != nil || !strings.Contains(out, "监听 127.0.0.1:18799）") || !strings.Contains(out, "fake serve --addr 127.0.0.1:18799") {
		t.Fatalf("UTF-8 语言环境下 run 应正常打印监听地址并启动服务：%v\n%s", err, out)
	}
}

// 保存钉钉凭据：文件仅本人可读，内容能被启动脚本加载；Client Secret 不出现在输出里；格式不对时拒绝。
func TestSetDingTalkCredentials(t *testing.T) {
	tmp := t.TempDir()
	secret := "Sec" + strings.Repeat("z", 30)
	run := func(input string) (string, error) {
		cmd := exec.Command("bash", "set-dingtalk-credentials.sh")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tmp, "COHIVE_LANG=zh-CN"}
		cmd.Stdin = strings.NewReader(input)
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	out, err := run("dingabc123\n" + secret + "\ntpl-notice\n\n")
	if err != nil || strings.Contains(out, secret) {
		t.Fatalf("保存应成功、输出不含密钥：%v\n%s", err, out)
	}
	file := filepath.Join(tmp, ".config", "cohive", "dingtalk.env")
	fi, err := os.Stat(file)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("文件应仅本人可读：%v %v", fi, err)
	}
	b, _ := os.ReadFile(file)
	for _, want := range []string{"COHIVE_DINGTALK_CLIENT_ID=dingabc123", "COHIVE_DINGTALK_CLIENT_SECRET=" + secret,
		"COHIVE_DINGTALK_NOTICE_TEMPLATE=tpl-notice", "COHIVE_DINGTALK_APPROVAL_TEMPLATE=\n"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("文件里应有 %q：%s", want, b)
		}
	}
	if out, err := run("dingabc123\nshort\n\n\n"); err == nil || !strings.Contains(out, "Client Secret 格式不对") {
		t.Fatalf("密钥格式不对应拒绝：%v %s", err, out)
	}
}

// 保存 Slack 令牌：文件仅本人可读，内容能被启动脚本加载；令牌不出现在输出里；填反、格式不对时拒绝。
func TestSetSlackTokens(t *testing.T) {
	tmp := t.TempDir()
	bot, app := "xoxb-1234-5678-"+strings.Repeat("q", 20), "xapp-1-A123-"+strings.Repeat("r", 20)
	run := func(input string) (string, error) {
		cmd := exec.Command("bash", "set-slack-tokens.sh")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + tmp, "COHIVE_LANG=zh-CN"}
		cmd.Stdin = strings.NewReader(input)
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	out, err := run(" " + bot + " \n" + app + "\n")
	if err != nil || strings.Contains(out, bot) || strings.Contains(out, app) {
		t.Fatalf("保存应成功、输出不含令牌：%v\n%s", err, out)
	}
	file := filepath.Join(tmp, ".config", "cohive", "slack.env")
	fi, err := os.Stat(file)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("文件应仅本人可读：%v %v", fi, err)
	}
	b, _ := os.ReadFile(file)
	if string(b) != "export COHIVE_SLACK_BOT_TOKEN="+bot+"\nexport COHIVE_SLACK_APP_TOKEN="+app+"\n" {
		t.Fatalf("文件内容：%s", b)
	}
	if out, err := run(app + "\n" + bot + "\n"); err == nil || !strings.Contains(out, "填反") || strings.Contains(out, bot) {
		t.Fatalf("填反应拒绝：%v %s", err, out)
	}
	if out, err := run("xoxb-short\n" + app + "\n"); err == nil || !strings.Contains(out, "机器人令牌格式不对") {
		t.Fatalf("格式不对应拒绝：%v %s", err, out)
	}
}

// shellLang 在给定环境下 source 语言库，返回脚本判断出的语言（zh 或 en）。
// 用假的 uname（报告 Darwin）与读不到界面语言的假 defaults 排除本机真实系统语言的影响。
func shellLang(t *testing.T, env ...string) string {
	t.Helper()
	return shellLangWithDefaults(t, "", false, env...)
}

// shellLangWithDefaults 同 shellLang，但假 defaults 输出 out；fail 为真时假 defaults 直接失败。
func shellLangWithDefaults(t *testing.T, out string, fail bool, env ...string) string {
	t.Helper()
	bin := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write("uname", "echo Darwin\n")
	if fail {
		write("defaults", "exit 1\n")
	} else {
		if err := os.WriteFile(filepath.Join(bin, "defaults.out"), []byte(out), 0o644); err != nil {
			t.Fatal(err)
		}
		write("defaults", `[ "$*" = "read -g AppleLanguages" ] || exit 2
cat "$(dirname "$0")/defaults.out"
`)
	}
	cmd := exec.Command("bash", "-c", `. lib/cohive-i18n.sh; printf '%s' "$COHIVE_SCRIPT_LANG"; cohive_t 中 en`)
	cmd.Env = append([]string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}, env...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, b)
	}
	return string(b)
}

// 提示语言：COHIVE_LANG > LC_ALL > LC_MESSAGES > LANG，取第一个非空的；zh 开头为中文，其余（含 C）为英文，都没有时英文。
func TestScriptLanguageDetection(t *testing.T) {
	needBash(t)
	for _, c := range []struct {
		env  []string
		want string
	}{
		{nil, "en"},
		{[]string{"LANG=zh_CN.UTF-8"}, "zh"},
		{[]string{"LANG=zh_TW.UTF-8"}, "zh"},
		{[]string{"LANG=en_US.UTF-8"}, "en"},
		{[]string{"LANG=ja_JP.UTF-8"}, "en"},
		{[]string{"LANG=zh_CN.UTF-8", "LC_ALL=en_US.UTF-8"}, "en"},
		{[]string{"LANG=en_US.UTF-8", "LC_MESSAGES=zh_CN.UTF-8"}, "zh"},
		{[]string{"LANG=en_US.UTF-8", "COHIVE_LANG=zh-CN"}, "zh"},
		{[]string{"LANG=zh_CN.UTF-8", "COHIVE_LANG=en"}, "en"},
		{[]string{"LANG=zh_CN.UTF-8", "COHIVE_LANG="}, "zh"},
		{[]string{"LC_ALL=C", "LANG=zh_CN.UTF-8"}, "zh"},
		{[]string{"LC_ALL=POSIX"}, "en"},
	} {
		got := shellLang(t, c.env...)
		want := c.want + map[string]string{"zh": "中", "en": "en"}[c.want]
		if got != want {
			t.Errorf("%v：得到 %q，期望 %q", c.env, got, want)
		}
	}
}

// 环境变量都没给出语言时，macOS 上看系统界面语言（defaults read -g AppleLanguages 的第一项）；
// 环境变量给出了非中文的语言时不看界面语言，与 Go 端一致。
func TestScriptLanguageFromMacOS(t *testing.T) {
	needBash(t)
	zhOut := "(\n    \"zh-Hans-CN\",\n    \"en-CN\"\n)\n"
	enOut := "(\n    \"en-CN\",\n    \"zh-Hans-CN\"\n)\n"
	for _, c := range []struct {
		name string
		out  string
		fail bool
		env  []string
		want string
	}{
		{"界面语言中文", zhOut, false, nil, "zh"},
		{"界面语言繁体", "(\n    \"zh-Hant-TW\"\n)\n", false, nil, "zh"},
		{"界面语言英文（中文排第二）", enOut, false, nil, "en"},
		{"界面语言不带引号", "(\n    en,\n    fr\n)\n", false, nil, "en"},
		{"defaults 失败", "", true, nil, "en"},
		{"defaults 输出为空", "", false, nil, "en"},
		{"LANG 为 C 时仍看界面语言", zhOut, false, []string{"LANG=C", "LC_ALL=POSIX"}, "zh"},
		{"COHIVE_LANG 为空时看界面语言", zhOut, false, []string{"COHIVE_LANG="}, "zh"},
		{"LANG 为英文时不看界面语言", zhOut, false, []string{"LANG=en_US.UTF-8"}, "en"},
		{"LANG 为日文时不看界面语言", zhOut, false, []string{"LANG=ja_JP.UTF-8"}, "en"},
		{"COHIVE_LANG=en 优先于界面语言", zhOut, false, []string{"COHIVE_LANG=en"}, "en"},
		{"LANG 为中文时界面语言为英文也用中文", enOut, false, []string{"LANG=zh_CN.UTF-8"}, "zh"},
	} {
		got := shellLangWithDefaults(t, c.out, c.fail, c.env...)
		want := c.want + map[string]string{"zh": "中", "en": "en"}[c.want]
		if got != want {
			t.Errorf("%s：得到 %q，期望 %q", c.name, got, want)
		}
	}
	// 不是 macOS 时不调用 defaults：假 uname 报 Linux，哪怕假 defaults 说中文也用英文
	bin := t.TempDir()
	for name, body := range map[string]string{
		"uname":    "echo Linux\n",
		"defaults": "echo '(\"zh-Hans-CN\")'\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("bash", "-c", `. lib/cohive-i18n.sh; printf '%s' "$COHIVE_SCRIPT_LANG"`)
	cmd.Env = []string{"PATH=" + bin + string(os.PathListSeparator) + os.Getenv("PATH")}
	if b, err := cmd.CombinedOutput(); err != nil || string(b) != "en" {
		t.Fatalf("非 macOS 应为英文：%v %q", err, b)
	}
}

// 英文环境：备份脚本的提示与报错都是英文，库名等信息照常。
func TestBackupMessagesEnglish(t *testing.T) {
	needBash(t)
	url, db := tempDatabase(t)
	dir := t.TempDir()
	env := backupEnv(t, url, dir)
	env["COHIVE_LANG"] = "en"
	out, err := script(t, "backup-db.sh", env)
	if err != nil || !strings.Contains(out, "Backed up database "+db) || !strings.Contains(out, "removed 0 old backup(s)") {
		t.Fatalf("英文提示：%v\n%s", err, out)
	}
	if strings.ContainsAny(out, "已备份删除") {
		t.Fatalf("英文输出里不应有中文：%s", out)
	}
	bad := map[string]string{"HOME": t.TempDir(), "COHIVE_LANG": "en"}
	if out, err := script(t, "backup-db.sh", bad); err == nil || !strings.Contains(out, "Error: COHIVE_DATABASE_URL is not set") {
		t.Fatalf("缺少数据库地址时应报英文错误：%v\n%s", err, out)
	}
	// 只靠 LANG：中文系统用中文，其他用英文
	bad["COHIVE_LANG"] = ""
	bad["LANG"] = "zh_CN.UTF-8"
	if out, _ := script(t, "backup-db.sh", bad); !strings.Contains(out, "错误：没有设置 COHIVE_DATABASE_URL") {
		t.Fatalf("LANG=zh_CN 时应报中文错误：%s", out)
	}
	bad["LANG"] = "en_US.UTF-8"
	if out, _ := script(t, "backup-db.sh", bad); !strings.Contains(out, "Error: COHIVE_DATABASE_URL is not set") {
		t.Fatalf("LANG=en_US 时应报英文错误：%s", out)
	}
}

// 英文环境：启动脚本的状态、重复启动、停止、端口占用提示与用法都是英文。
func TestServerScriptMessagesEnglish(t *testing.T) {
	needBash(t)
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("需要 lsof")
	}
	tmp := t.TempDir()
	home := filepath.Join(tmp, "cohive-home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	port := freePort(t)
	conf := fmt.Sprintf("COHIVE_DATABASE_URL=postgres://u:pw@localhost:1/demo\nCOHIVE_ADDR=127.0.0.1:%d\n", port)
	if err := os.WriteFile(filepath.Join(home, "server.env"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": tmp, "COHIVE_HOME": home, "COHIVE_BIN": fakeServer(t, tmp),
		"COHIVE_TELEGRAM_ENV": filepath.Join(tmp, "none"), "COHIVE_DINGTALK_ENV": filepath.Join(tmp, "none"),
		"COHIVE_LAUNCHD_PLIST": filepath.Join(tmp, "none.plist"),
		"COHIVE_LANG":          "", "LANG": "en_US.UTF-8"} // 不设 COHIVE_LANG，只靠系统语言
	t.Cleanup(func() { _, _ = script(t, "cohive-server.sh", env, "stop") })

	if out, err := script(t, "cohive-server.sh", env, "status"); err == nil || !strings.Contains(out, "Not running") || !strings.Contains(out, "Database: demo") {
		t.Fatalf("未运行时的状态：%v\n%s", err, out)
	}
	if out, err := script(t, "cohive-server.sh", env, "start"); err != nil || !strings.Contains(out, "Started, pid ") {
		t.Fatalf("启动：%v\n%s", err, out)
	}
	if out, _ := script(t, "cohive-server.sh", env, "start"); !strings.Contains(out, "Already running, pid ") {
		t.Fatalf("重复启动：%s", out)
	}
	if out, err := script(t, "cohive-server.sh", env, "status"); err != nil || !strings.Contains(out, "Running, pid ") {
		t.Fatalf("运行中的状态：%v\n%s", err, out)
	}
	if out, err := script(t, "cohive-server.sh", env, "stop"); err != nil || !strings.Contains(out, "Stopped (pid ") {
		t.Fatalf("停止：%v\n%s", err, out)
	}
	logB, _ := os.ReadFile(filepath.Join(home, "logs", "server.log"))
	if !strings.Contains(string(logB), "starting (database demo, listening on 127.0.0.1:") {
		t.Fatalf("日志里的启动行应为英文：%s", logB)
	}
	// 没有子命令：打印英文用法
	if out, err := script(t, "cohive-server.sh", env); err == nil || !strings.Contains(out, "Usage: scripts/cohive-server.sh start|stop|restart|status|run") {
		t.Fatalf("用法应为英文：%v\n%s", err, out)
	}
	// 端口被占：英文报错
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	if out, err := script(t, "cohive-server.sh", env, "start"); err == nil || !strings.Contains(out, "is already in use by process") {
		t.Fatalf("端口被占：%v\n%s", err, out)
	}
	// 同样的环境换成中文系统语言：用法是中文
	env["LANG"] = "zh_CN.UTF-8"
	if out, err := script(t, "cohive-server.sh", env); err == nil || !strings.Contains(out, "用法：scripts/cohive-server.sh start|stop|restart|status|run") {
		t.Fatalf("中文系统的用法应为中文：%v\n%s", err, out)
	}
}

// 英文环境：保存令牌的脚本提示与报错为英文。
func TestSetCredentialsScriptsEnglish(t *testing.T) {
	needBash(t)
	run := func(name, input string) (string, error) {
		cmd := exec.Command("bash", name)
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "LANG=en_US.UTF-8"}
		cmd.Stdin = strings.NewReader(input)
		b, err := cmd.CombinedOutput()
		return string(b), err
	}
	if out, err := run("set-dingtalk-credentials.sh", "dingabc123\nshort\n\n\n"); err == nil || !strings.Contains(out, "Invalid Client Secret format") || !strings.Contains(out, "Client ID (DingTalk developer console") {
		t.Fatalf("英文提示：%v\n%s", err, out)
	}
	if out, err := run("set-telegram-token.sh", "nonsense\n"); err == nil || !strings.Contains(out, "Invalid format: a bot token looks like") {
		t.Fatalf("英文提示：%v\n%s", err, out)
	}
	tok := "123456789:AA" + strings.Repeat("x", 30)
	if out, err := run("set-telegram-token.sh", tok+"\n"); err != nil || !strings.Contains(out, "Saved to ") || strings.Contains(out, tok) {
		t.Fatalf("保存成功应为英文且不回显令牌：%v\n%s", err, out)
	}
}
