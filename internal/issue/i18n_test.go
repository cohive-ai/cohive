package issue_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/issue"
)

// TestMain：现有测试断言中文文案，固定用中文跑；英文另有用例。
func TestMain(m *testing.M) {
	i18n.SetDefault(i18n.ZhCN)
	os.Exit(m.Run())
}

var allStatuses = []issue.Status{
	issue.StatusDraft, issue.StatusOpen, issue.StatusAccepted, issue.StatusFixing,
	issue.StatusReadyForVerification, issue.StatusReopened, issue.StatusClosed, issue.StatusDeclined,
}

// 状态名用动态键取，遍历所有状态与所有语言，确认键齐全。
func TestStatusLabelsComplete(t *testing.T) {
	for _, l := range i18n.Supported() {
		for _, s := range allStatuses {
			got := issue.StatusLabelIn(l, s)
			if got == "" || strings.HasPrefix(got, "issue.") {
				t.Errorf("%s 缺状态 %s 的名称：%q", l, s, got)
			}
		}
	}
	// 旧函数固定中文，不受缺省语言影响
	i18n.SetDefault(i18n.EN)
	defer i18n.SetDefault(i18n.ZhCN)
	if got := issue.StatusOpen.Label(); got != "待接单" {
		t.Errorf("Label() 应固定为中文，得到 %q", got)
	}
	if got := issue.StatusLabelIn(i18n.EN, issue.StatusReadyForVerification); got != "Ready for verification" {
		t.Errorf("英文状态名不符：%q", got)
	}
	if got := issue.StatusLabelIn(i18n.EN, "BOGUS"); got != "BOGUS" {
		t.Errorf("未知状态应原样返回：%q", got)
	}
}

// 不能执行的原因也走语言包：遍历每种原因，两种语言都要渲染出文字，不能留下键名。
func TestTransitionErrorLocalized(t *testing.T) {
	source, owner := issue.Roles(true, false), issue.Roles(false, true)
	cases := []struct {
		name  string
		from  issue.Status
		act   issue.Action
		roles []issue.Role
		en    string
	}{
		{"状态不对", issue.StatusDraft, issue.ActClose, source, "only available when the issue is Ready for verification"},
		{"角色不对", issue.StatusOpen, issue.ActAccept, source, "only the owner can do this"},
		{"草稿评论", issue.StatusDraft, issue.ActComment, owner, "only the reporter can edit a draft"},
		{"评论角色", issue.StatusFixing, issue.ActComment, nil, "only the reporter or the owner can comment"},
		{"未知动作", issue.StatusOpen, "bogus", owner, "unknown action"},
	}
	for _, c := range cases {
		_, err := issue.Apply(c.from, c.act, c.roles)
		var te *issue.TransitionError
		if !errors.As(err, &te) {
			t.Fatalf("%s：应为 *TransitionError，得到 %v", c.name, err)
		}
		zh, en := te.Localize(i18n.ZhCN), te.Localize(i18n.EN)
		if strings.Contains(zh, "issue.") || strings.Contains(en, "issue.") {
			t.Errorf("%s：渲染结果里留有键名：%q / %q", c.name, zh, en)
		}
		if !strings.Contains(en, c.en) || strings.ContainsAny(en, "的了") {
			t.Errorf("%s：英文应含 %q 且不含中文：%q", c.name, c.en, en)
		}
		if te.Error() != zh {
			t.Errorf("%s：Error() 应固定为中文：%q", c.name, te.Error())
		}
		if te.Reason == "" {
			t.Errorf("%s：Reason 应保留中文原因", c.name)
		}
	}
}

func TestTransitionErrorEnglishFull(t *testing.T) {
	_, err := issue.Apply(issue.StatusDraft, issue.ActClose, issue.Roles(true, false))
	var te *issue.TransitionError
	if !errors.As(err, &te) {
		t.Fatal(err)
	}
	want := "This issue is Draft (DRAFT), so close is not allowed: this action is only available when the issue is Ready for verification. Actions you can take now: submit, comment"
	if got := te.Localize(i18n.EN); got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
}
