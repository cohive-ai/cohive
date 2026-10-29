package main

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/jackc/pgx/v5"
)

func lastAdminEvent(t *testing.T) (store.AdminEvent, int) {
	t.Helper()
	pool, err := openDB(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	es, err := store.ListAdminEvents(context.Background(), pool, store.AdminEventFilter{Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if len(es) == 0 {
		return store.AdminEvent{}, 0
	}
	return es[0], len(es)
}

type cliCase struct {
	cmd    string     // 用法里的命令名，用于检查每条命令都归了类
	setup  [][]string // 先执行的命令
	args   []string
	action string
	ws     string
}

// cliCases 覆盖服务器命令行里每条写命令。setup 之后统一已有：default 的管理员 lee、成员 ann、bob。
var cliCases = []cliCase{
	{"workspace add", nil, []string{"workspace", "add", "--name", "ops", "--admin", "lee"}, core.ActWorkspaceCreate, "ops"},
	{"workspace remove", [][]string{{"workspace", "add", "--name", "ops"}}, []string{"workspace", "remove", "--name", "ops"}, core.ActWorkspaceDelete, "ops"},
	{"workspace purge", [][]string{{"workspace", "add", "--name", "ops"}}, []string{"workspace", "purge", "--name", "ops", "--confirm", "ops"}, core.ActWorkspacePurge, "ops"},
	{"workspace set", nil, []string{"workspace", "set", "--name", "default", "--display", "默认组"}, core.ActWorkspaceDisplayName, "default"},
	{"server set", nil, []string{"server", "set", "--external-url", "https://c.example.com"}, core.ActServerSettings, ""},
	{"workspace notify", nil, []string{"workspace", "notify", "--workspace", "default", "--telegram-chat", "-1009"}, core.ActWorkspaceSettings, "default"},
	{"account set", nil, []string{"account", "set", "--name", "ann", "--display", "Ann"}, core.ActAccountUpdate, ""},
	{"account disable", nil, []string{"account", "disable", "--name", "ann"}, core.ActAccountDisable, ""},
	{"account enable", [][]string{{"account", "disable", "--name", "ann"}}, []string{"account", "enable", "--name", "ann"}, core.ActAccountEnable, ""},
	{"account server-admin", nil, []string{"account", "server-admin", "--name", "ann", "--on"}, core.ActServerAdminGrant, ""},
	{"admin reset-password", nil, []string{"admin", "reset-password", "--name", "ann"}, core.ActResetPassword, ""},
	{"member add", nil, []string{"member", "add", "--name", "carl"}, core.ActMemberAdd, "default"},
	{"member set", nil, []string{"member", "set", "--name", "ann", "--role", "admin"}, core.ActMemberRole, "default"},
	{"member remove", nil, []string{"member", "remove", "--name", "ann", "--handover-to", "bob", "--as", "lee"}, core.ActMemberRemove, "default"},
	{"token issue", nil, []string{"token", "issue", "--member", "ann", "--tool", "codex"}, core.ActTokenIssue, "default"},
	{"token revoke", [][]string{{"token", "issue", "--member", "ann", "--tool", "codex"}}, []string{"token", "revoke", "--id", "1"}, core.ActTokenRevoke, "default"},
	{"service add", nil, []string{"service", "add", "--name", "api", "--owner", "bob"}, core.ActServiceOwner, "default"},
	{"issue cancel", nil, []string{"issue", "cancel", "--id", "1", "--reason", "重复", "--as", "lee"}, core.ActIssueCancel, "default"},
}

// cliPrep 是命令行造不出来、要直接写库准备的数据（按命令名）。
var cliPrep = map[string]func(t *testing.T){
	"issue cancel": func(t *testing.T) { submitIssue(t, "ann", "bob") }, // ann 发给 bob 的 #1，bob 已接单
}

// submitIssue 让 source 起草并发出一张交给 owner 的单，owner 接单，返回单号。
func submitIssue(t *testing.T, source, owner string) int64 {
	t.Helper()
	ctx := context.Background()
	pool, err := openDB(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ws, err := store.DefaultWorkspace(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	id := func(name string) store.Identity {
		m, err := store.MemberByName(ctx, pool, ws, name)
		if err != nil {
			t.Fatal(err)
		}
		return store.Identity{AccountID: m.AccountID, MemberID: m.ID, WorkspaceID: ws, MemberName: m.Name, Tool: "codex"}
	}
	svc := &core.Service{Pool: pool}
	is, err := svc.Draft(ctx, id(source), core.DraftInput{Title: "下单接口 500", Owner: owner, Actual: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		who string
		act issue.Action
	}{{source, issue.ActSubmit}, {owner, issue.ActAccept}} {
		if is, err = svc.Act(ctx, id(step.who), core.ActInput{IssueID: is.ID, Action: step.act, ExpectedVersion: is.Version}); err != nil {
			t.Fatal(err)
		}
	}
	return is.ID
}

// notAdminCommands 是不属于管理操作、不留记录的命令，各附理由。
var notAdminCommands = map[string]string{
	"migrate":        "执行数据库迁移",
	"serve":          "启动服务",
	"workspace list": "只读",
	"account list":   "只读",
	"member list":    "只读",
	"token list":     "只读",
	"service list":   "只读",
	"notify test":    "往群里发一条测试消息，不改变任何人的权限",
	"notify retry":   "把发送失败的通知重新排队，不改变任何人的权限",
	"audit list":     "只读：查看管理操作记录",
	"server show":    "只读：查看对外地址",
	"theme check":    "只读：检查主题文件，不改任何设置（主题要改文件、重启才生效）",
}

// 服务器命令行里每条写命令都留记录，入口为服务器命令行、操作人为空。
func TestEveryServerCommandRecords(t *testing.T) {
	for _, c := range cliCases {
		t.Run(c.cmd, func(t *testing.T) {
			adminEnv(t)
			mustRun(t, "migrate")
			mustRun(t, "member", "add", "--name", "lee", "--role", "admin")
			mustRun(t, "member", "add", "--name", "ann")
			mustRun(t, "member", "add", "--name", "bob")
			for _, s := range c.setup {
				mustRun(t, s...)
			}
			if prep := cliPrep[c.cmd]; prep != nil {
				prep(t)
			}
			mustRun(t, c.args...)
			e, _ := lastAdminEvent(t)
			if e.Action != c.action || e.Via != core.ViaServerCLI || e.WorkspaceName != c.ws || e.ActorAccountID != nil {
				t.Fatalf("最后一条记录：%s 入口 %s 工作空间 %q 操作人 %v（%s）", e.Action, e.Via, e.WorkspaceName, e.ActorAccountID, core.EventText(i18n.Default(), e))
			}
		})
	}
}

// 用法里列出的每条命令要么在 cliCases（留记录），要么在 notAdminCommands（附理由）。新增命令不归类就红。
func TestEveryCommandClassified(t *testing.T) {
	listed := map[string]bool{}
	for _, c := range cliCases {
		listed[c.cmd] = true
	}
	re := regexp.MustCompile(`(?m)^  ([a-z][a-z-]*(?: [a-z][a-z-]*)?)(?:\s|$)`)
	n := 0
	for _, m := range re.FindAllStringSubmatch(usage(), -1) {
		cmd := strings.TrimSpace(m[1])
		n++
		if !listed[cmd] && notAdminCommands[cmd] == "" {
			t.Errorf("命令 %q 没有归类：管理操作要写记录并加进 cliCases，否则加进 notAdminCommands 并写明理由", cmd)
		}
	}
	if n < 20 {
		t.Fatalf("只从用法里解析出 %d 条命令，解析可能失效", n)
	}
}

// audit list：人话、入口、工作空间、翻页提示；用户填的名字里的控制字符不会原样打到终端。
func TestAuditList(t *testing.T) {
	adminEnv(t)
	mustRun(t, "migrate")
	if out := mustRun(t, "audit", "list"); !strings.Contains(out, "没有记录") {
		t.Fatalf("空库：%s", out)
	}
	mustRun(t, "member", "add", "--name", "lee", "--role", "admin")
	mustRun(t, "member", "add", "--name", "ann")
	mustRun(t, "member", "set", "--name", "ann", "--role", "admin")
	pool, err := openDB(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	err = pgx.BeginFunc(context.Background(), pool, func(tx pgx.Tx) error {
		return store.InsertAdminEvent(context.Background(), tx, store.AdminEvent{Via: core.ViaWeb, ActorName: "lee",
			Action: core.ActDeviceRevoke, TargetKind: "device", TargetName: "evil\x1b[31m\nname",
			Detail: map[string]any{"owner": "ann", "device_name": "evil\x1b[31m\nname"}})
	})
	if err != nil {
		t.Fatal(err)
	}

	out := mustRun(t, "audit", "list", "--limit", "2")
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "lee 在网页上吊销了 ann 的设备 evil [31m name") ||
		!strings.Contains(lines[1], "服务器命令行把 ann 的角色从成员改为管理员") || !strings.Contains(lines[1], "[default]") ||
		!strings.Contains(lines[1], "服务器命令行") || !strings.HasPrefix(lines[2], "更早的记录：--before ") {
		t.Fatalf("输出：%q", out)
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Fatalf("控制字符应被替换：%q", out)
	}
	if out := mustRun(t, "audit", "list", "--type", "member", "--workspace", "default"); strings.Count(out, "\n") != 2 {
		t.Fatalf("按类别与工作空间筛选：%s", out)
	}
	if _, err := runCmd(t, "audit", "list", "--type", "nope"); err == nil {
		t.Fatal("不认识的类别应报错")
	}
}
