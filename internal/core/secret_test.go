package core_test

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/issue"
)

// fakeGitHubToken 按 GitHub 个人令牌的格式随机生成一个假令牌，源码里不出现形似真令牌的字面量。
func fakeGitHubToken(seed int64) string {
	const alnum = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	r := rand.New(rand.NewSource(seed))
	b := make([]byte, 36)
	for i := range b {
		b[i] = alnum[r.Intn(len(alnum))]
	}
	return "ghp_" + string(b)
}

// assertNoLeak 断言文本里没有密钥本身，也没有它的任意 8 个字符的片段。
func assertNoLeak(t *testing.T, what, text, secret string) {
	t.Helper()
	for i := 0; i+8 <= len(secret); i++ {
		if strings.Contains(text, secret[i:i+8]) {
			t.Fatalf("%s 里出现了密钥片段 %q：%s", what, secret[i:i+8], text)
		}
	}
}

func (e env) counts(t *testing.T) [4]int {
	t.Helper()
	var c [4]int
	for i, tbl := range []string{"issues", "issue_events", "idempotency", "notifications"} {
		if err := e.svc.Pool.QueryRow(context.Background(), "SELECT count(*) FROM "+tbl).Scan(&c[i]); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

func secretErr(t *testing.T, err error, field string, line int, secret string) {
	t.Helper()
	var se *core.SecretError
	if !errors.As(err, &se) {
		t.Fatalf("应因疑似密钥被拒绝，得到 %v", err)
	}
	found := false
	for _, f := range se.Findings {
		if f.Field == field && f.Line == line && f.RuleID == "github-pat" {
			found = true
		}
	}
	if !found {
		t.Fatalf("应指出 %s 第 %d 行命中 github-pat，得到 %+v", field, line, se.Findings)
	}
	msg := err.Error()
	if !strings.Contains(msg, field) || !strings.Contains(msg, "GitHub 令牌") || !strings.Contains(msg, "已拒绝写入") {
		t.Fatalf("错误应写明字段与规则类别：%s", msg)
	}
	assertNoLeak(t, "错误信息", msg, secret)
}

// 计划 6.2 第 3、4 条：起草的每个字段命中即拒绝，什么都不写；错误不回显密钥。
func TestDraftRejectsSecretsInEveryField(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	enableNotify(t, e)
	tok := fakeGitHubToken(1)
	set := map[string]func(*core.DraftInput, string){
		"title":          func(in *core.DraftInput, v string) { in.Title = "调用失败 " + v },
		"brief":          func(in *core.DraftInput, v string) { in.Brief = "令牌 " + v },
		"summary":        func(in *core.DraftInput, v string) { in.Summary = v },
		"expected":       func(in *core.DraftInput, v string) { in.Expected = v },
		"actual":         func(in *core.DraftInput, v string) { in.Actual = v },
		"repro_script":   func(in *core.DraftInput, v string) { in.ReproScript = v },
		"repro_env":      func(in *core.DraftInput, v string) { in.ReproEnv = v },
		"evidence":       func(in *core.DraftInput, v string) { in.Evidence = v },
		"repo":           func(in *core.DraftInput, v string) { in.Repo = v },
		"branch":         func(in *core.DraftInput, v string) { in.Branch = v },
		"commit":         func(in *core.DraftInput, v string) { in.Commit = v },
		"target_service": func(in *core.DraftInput, v string) { in.TargetService = v },
		"owner":          func(in *core.DraftInput, v string) { in.Owner = v },
	}
	before := e.counts(t)
	for field, fn := range set {
		in := core.DraftInput{Title: "下单接口返回 500", IdempotencyKey: "draft-" + field}
		// 放在第 3 行，验证行号
		fn(&in, "第一行\n第二行\nexport GITHUB_TOKEN="+tok)
		_, err := e.svc.Draft(ctx, e.a, in)
		secretErr(t, err, field, 3, tok)
	}
	if after := e.counts(t); after != before {
		t.Fatalf("被拒绝的起草不应写任何记录：前 %v 后 %v", before, after)
	}

	// 脱敏后用同一个幂等键重试成功
	in := core.DraftInput{Title: "下单接口返回 500", Evidence: "export GITHUB_TOKEN=[已脱敏]", IdempotencyKey: "draft-evidence"}
	if _, err := e.svc.Draft(ctx, e.a, in); err != nil {
		t.Fatalf("脱敏后重试应成功：%v", err)
	}
}

// 计划 6.2 第 3 条：所有经 Act 的写入（发出、接单、退回、转交、评论、开始修复、请求验证、关闭、重开）都扫描 note。
func TestActRejectsSecretsInNote(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	enableNotify(t, e)
	tok := fakeGitHubToken(2)
	note := "日志如下\nAuthorization: token " + tok

	reject := func(who string, is int64, a issue.Action, version int, extra func(*core.ActInput)) {
		t.Helper()
		id := e.a
		if who == "b" {
			id = e.b
		}
		in := core.ActInput{IssueID: is, Action: a, ExpectedVersion: version, Note: note, IdempotencyKey: "k-" + string(a)}
		if extra != nil {
			extra(&in)
		}
		before := e.counts(t)
		_, err := e.svc.Act(ctx, id, in)
		secretErr(t, err, "note", 2, tok)
		if after := e.counts(t); after != before {
			t.Fatalf("%s 被拒绝后不应写任何记录：前 %v 后 %v", a, before, after)
		}
	}

	d := e.draft(t)
	reject("a", d.ID, issue.ActSubmit, d.Version, nil)
	reject("a", d.ID, issue.ActComment, 0, nil)
	is := e.act(t, e.a, d, issue.ActSubmit)
	reject("b", is.ID, issue.ActAccept, is.Version, nil)
	reject("b", is.ID, issue.ActDecline, is.Version, nil)
	reject("b", is.ID, issue.ActTransfer, is.Version, func(in *core.ActInput) { in.Assignee = "lee-claude" })
	is = e.act(t, e.b, is, issue.ActAccept)
	reject("b", is.ID, issue.ActStart, is.Version, nil)
	reject("b", is.ID, issue.ActRequestVerification, is.Version, nil)
	is = e.act(t, e.b, is, issue.ActRequestVerification)
	reject("a", is.ID, issue.ActClose, is.Version, nil)
	reject("a", is.ID, issue.ActReopen, is.Version, nil)

	// 接收人里贴了密钥：不入库，但查无此人时错误会回显名字，所以也扫
	_, err := e.svc.Act(ctx, e.b, core.ActInput{IssueID: is.ID, Action: issue.ActTransfer, ExpectedVersion: is.Version,
		Note: "转给前端", Assignee: tok})
	var se *core.SecretError
	if !errors.As(err, &se) {
		t.Fatalf("接收人里的密钥应被拦下，得到 %v", err)
	}
	assertNoLeak(t, "错误信息", err.Error(), tok)

	// 脱敏后用同一个幂等键重试成功
	if _, err := e.svc.Act(ctx, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActClose, ExpectedVersion: is.Version,
		Note: "Authorization: token [已脱敏]", IdempotencyKey: "k-" + string(issue.ActClose)}); err != nil {
		t.Fatalf("脱敏后重试应成功：%v", err)
	}
}

func TestSecretErrorListsAtMostTen(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t)
	var lines []string
	for i := 0; i < 12; i++ {
		lines = append(lines, "token="+fakeGitHubToken(int64(100+i)))
	}
	_, err := e.svc.Draft(ctx, e.a, core.DraftInput{Title: "很多令牌", Evidence: strings.Join(lines, "\n")})
	if err == nil || !strings.Contains(err.Error(), "另有 2 处") || !strings.Contains(err.Error(), "evidence 第 10 行") {
		t.Fatalf("最多列 10 处，其余汇总：%v", err)
	}
}
