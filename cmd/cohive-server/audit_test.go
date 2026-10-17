package main

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/store"
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
			mustRun(t, c.args...)
			e, _ := lastAdminEvent(t)
			if e.Action != c.action || e.Via != core.ViaServerCLI || e.WorkspaceName != c.ws || e.ActorAccountID != nil {
				t.Fatalf("最后一条记录：%s 入口 %s 工作空间 %q 操作人 %v（%s）", e.Action, e.Via, e.WorkspaceName, e.ActorAccountID, core.EventText(e))
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
	for _, m := range re.FindAllStringSubmatch(usage, -1) {
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
