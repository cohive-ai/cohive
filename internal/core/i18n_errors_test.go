package core_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
	"github.com/cohive-ai/cohive/internal/secretscan"
	"github.com/cohive-ai/cohive/internal/store"
)

// 业务层的报错按渲染时的语言出文字，不随进程缺省语言（测试固定中文）走。
func TestErrorsRenderInRequestedLanguage(t *testing.T) {
	tok := fakeGitHubToken(11)
	cases := []struct {
		name   string
		err    error
		zh, en string
	}{
		{"哨兵错误", core.ErrNotAdmin, "只有工作空间管理员", "Only a workspace admin can do this"},
		{"版本冲突", &core.ConflictError{IssueID: 1, Expected: 2, Current: 3}, "版本已变化", "you provided version 2"},
		{"并发中止", &core.RetryError{Code: "40P01"}, "有其他操作同时在改", "Another operation was changing"},
		{"对外地址被锁定", core.ErrExternalURLLocked, "由环境变量 COHIVE_EXTERNAL_URL 锁定", "locked by the COHIVE_EXTERNAL_URL"},
		{"疑似密钥", &core.SecretError{Findings: secretscan.Scan(secretscan.Field{Name: "note", Text: "token " + tok})},
			"note 第 1 行：GitHub 令牌", "note line 1: GitHub token"},
	}
	for _, c := range cases {
		if got := i18n.Message(i18n.ZhCN, c.err); !strings.Contains(got, c.zh) {
			t.Errorf("%s：中文应含 %q，得到 %q", c.name, c.zh, got)
		}
		got := i18n.Message(i18n.EN, c.err)
		if !strings.Contains(got, c.en) {
			t.Errorf("%s：英文应含 %q，得到 %q", c.name, c.en, got)
		}
		if strings.ContainsAny(got, "的了请") {
			t.Errorf("%s：英文文案里混进了中文：%q", c.name, got)
		}
		if strings.Contains(got, tok[:8]) {
			t.Errorf("%s：英文文案里出现了密钥片段：%q", c.name, got)
		}
		// Error() 用进程缺省语言（这里是中文），errors.Is 照常可用
		if c.err.Error() != i18n.Message(i18n.ZhCN, c.err) {
			t.Errorf("%s：Error() 应等于缺省语言（中文）的渲染", c.name)
		}
	}
	if !errors.Is(&core.ConflictError{}, store.ErrVersionConflict) || !errors.Is(&core.RetryError{}, store.ErrVersionConflict) {
		t.Error("版本冲突与并发中止应仍能 errors.Is store.ErrVersionConflict")
	}
}

func TestInputAndStateErrorsEnglish(t *testing.T) {
	_, err := core.NormalizeExternalURL("ftp://x")
	var ie *core.InputError
	if !errors.As(err, &ie) {
		t.Fatalf("应为参数错误：%v", err)
	}
	if got := ie.Localize(i18n.EN); !strings.Contains(got, "must look like https://hostname") {
		t.Errorf("英文：%q", got)
	}
	if got := ie.Localize(i18n.ZhCN); !strings.Contains(got, "对外地址应为") {
		t.Errorf("中文：%q", got)
	}
}

// 带请求语言的上下文：错误、看板与收件箱的文字按调用方的语言出。
func TestServiceTextFollowsContextLanguage(t *testing.T) {
	e := newEnv(t)
	en := i18n.WithLang(context.Background(), i18n.EN)

	// 参数错误与版本冲突
	_, err := e.svc.Draft(en, e.a, core.DraftInput{Title: "x", Severity: "bad"})
	if got := i18n.Message(i18n.EN, err); !strings.Contains(got, "severity must be one of") {
		t.Errorf("起草的参数错误应有英文：%q", got)
	}
	if got := i18n.Message(i18n.ZhCN, err); !strings.Contains(got, "severity 只能是") {
		t.Errorf("同一个错误按中文渲染：%q", got)
	}
	is := e.draft(t)
	_, err = e.svc.Act(en, e.a, core.ActInput{IssueID: is.ID, Action: issue.ActSubmit})
	if got := i18n.Message(i18n.EN, err); !strings.Contains(got, "expected_version") || strings.ContainsAny(got, "必须提供") {
		t.Errorf("缺少版本号应有英文：%q", got)
	}
	_, err = e.svc.Act(en, e.a, core.ActInput{IssueID: 9999, Action: issue.ActSubmit, ExpectedVersion: 1})
	if got := i18n.Message(i18n.EN, err); !strings.Contains(got, "Issue #9999 does not exist or you do not have access to it") || !errors.Is(err, store.ErrNotFound) {
		t.Errorf("找不到问题单应有英文且仍是 ErrNotFound：%q %v", got, err)
	}

	// 收件箱与看板
	items, err := e.svc.Inbox(en, e.a)
	if err != nil || len(items) != 1 || items[0].NeedsMe != "Draft waiting for you to submit" {
		t.Fatalf("英文收件箱：%+v %v", items, err)
	}
	sent := e.act(t, e.a, is, issue.ActSubmit)
	_, board, err := e.svc.Board(en, e.b)
	if err != nil || len(board) != 1 || board[0].Needs != "Waiting for the owner to accept" {
		t.Fatalf("英文看板：%+v %v", board, err)
	}
	_, board, err = e.svc.Board(context.Background(), e.b)
	if err != nil || len(board) != 1 || board[0].Needs != "等负责人接单" {
		t.Fatalf("没有请求语言时用缺省语言（中文）：%+v %v", board, err)
	}

	// 会话钩子的待办摘要
	res, err := e.svc.Alerts(i18n.WithLang(context.Background(), i18n.EN), e.b, nil)
	if err != nil || len(res.PendingTop) != 1 || res.PendingTop[0].IssueID != sent.ID ||
		res.PendingTop[0].Needs != "Waiting for you to accept, decline, or transfer" {
		t.Fatalf("英文待办摘要：%+v %v", res, err)
	}
}
