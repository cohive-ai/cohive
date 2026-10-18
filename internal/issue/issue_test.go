package issue_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/issue"
)

var (
	source = issue.Roles(true, false)
	owner  = issue.Roles(false, true)
)

func TestApplyAllowedTransitions(t *testing.T) {
	cases := []struct {
		from  issue.Status
		act   issue.Action
		roles []issue.Role
		want  issue.Status
	}{
		{issue.StatusDraft, issue.ActSubmit, source, issue.StatusOpen},
		{issue.StatusOpen, issue.ActAccept, owner, issue.StatusAccepted},
		{issue.StatusOpen, issue.ActDecline, owner, issue.StatusDeclined},
		{issue.StatusOpen, issue.ActTransfer, owner, issue.StatusOpen},
		{issue.StatusAccepted, issue.ActStart, owner, issue.StatusFixing},
		{issue.StatusReopened, issue.ActStart, owner, issue.StatusFixing},
		{issue.StatusAccepted, issue.ActRequestVerification, owner, issue.StatusReadyForVerification},
		{issue.StatusFixing, issue.ActRequestVerification, owner, issue.StatusReadyForVerification},
		{issue.StatusReopened, issue.ActRequestVerification, owner, issue.StatusReadyForVerification},
		{issue.StatusReadyForVerification, issue.ActClose, source, issue.StatusClosed},
		{issue.StatusReadyForVerification, issue.ActReopen, source, issue.StatusReopened},
		{issue.StatusDraft, issue.ActComment, source, issue.StatusDraft},
		{issue.StatusFixing, issue.ActComment, owner, issue.StatusFixing},
		{issue.StatusFixing, issue.ActComment, source, issue.StatusFixing},
	}
	for _, c := range cases {
		got, err := issue.Apply(c.from, c.act, c.roles)
		if err != nil {
			t.Errorf("%s --%s--> 应成功，得到错误：%v", c.from, c.act, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s --%s--> 应得 %s，得到 %s", c.from, c.act, c.want, got)
		}
	}
}

func TestInvalidTransitionMessage(t *testing.T) {
	_, err := issue.Apply(issue.StatusDraft, issue.ActClose, source)
	if err == nil {
		t.Fatal("关闭草稿应失败")
	}
	var te *issue.TransitionError
	if !errors.As(err, &te) {
		t.Fatalf("错误类型应为 *TransitionError，得到 %T", err)
	}
	msg := err.Error()
	for _, want := range []string{"草稿", "允许的动作", "submit"} {
		if !strings.Contains(msg, want) {
			t.Errorf("错误信息应包含 %q，实际：%s", want, msg)
		}
	}
}

func TestWrongRole(t *testing.T) {
	_, err := issue.Apply(issue.StatusOpen, issue.ActAccept, source)
	if err == nil {
		t.Fatal("发起人不能接单")
	}
	if !strings.Contains(err.Error(), "负责人") {
		t.Errorf("错误信息应说明只有负责人可以接单，实际：%s", err)
	}
	if _, err := issue.Apply(issue.StatusReadyForVerification, issue.ActClose, owner); err == nil {
		t.Fatal("负责人不能关闭问题单")
	}
	if _, err := issue.Apply(issue.StatusDraft, issue.ActComment, owner); err == nil {
		t.Fatal("草稿只有发起人可以评论")
	}
}

func TestNoTransitionFromTerminal(t *testing.T) {
	for _, s := range []issue.Status{issue.StatusClosed, issue.StatusDeclined} {
		if got := issue.Allowed(s, issue.Roles(true, true)); len(got) != 1 || got[0] != issue.ActComment {
			t.Errorf("%s 只允许评论，实际允许 %v", s, got)
		}
	}
}

func TestCategory(t *testing.T) {
	cases := map[issue.Status]issue.Category{
		issue.StatusDraft:                issue.CategoryDraft,
		issue.StatusOpen:                 issue.CategoryInProgress,
		issue.StatusReadyForVerification: issue.CategoryInProgress,
		issue.StatusClosed:               issue.CategoryDone,
		issue.StatusDeclined:             issue.CategoryDone,
	}
	for s, want := range cases {
		if got := s.Category(); got != want {
			t.Errorf("%s 的类别应为 %s，得到 %s", s, want, got)
		}
	}
}

func TestParseStatusRejectsUnknown(t *testing.T) {
	if _, err := issue.ParseStatus("BOGUS"); err == nil {
		t.Fatal("未知状态应报错")
	}
	if s, err := issue.ParseStatus("OPEN"); err != nil || s != issue.StatusOpen {
		t.Fatalf("OPEN 应解析成功，得到 %v %v", s, err)
	}
}

// 作废（#12）：发起人只能在待接单时作废；作废之后只能留言；管理员能作废的只有进行中的状态。
func TestCancel(t *testing.T) {
	if got, err := issue.Apply(issue.StatusOpen, issue.ActCancel, source); err != nil || got != issue.StatusCancelled {
		t.Fatalf("发起人作废待接单的单：%v %v", got, err)
	}
	for _, from := range []issue.Status{issue.StatusDraft, issue.StatusAccepted, issue.StatusFixing,
		issue.StatusReadyForVerification, issue.StatusReopened, issue.StatusClosed, issue.StatusDeclined, issue.StatusCancelled} {
		if _, err := issue.Apply(from, issue.ActCancel, source); err == nil {
			t.Errorf("发起人不能在 %s 作废", from)
		}
	}
	if _, err := issue.Apply(issue.StatusOpen, issue.ActCancel, owner); err == nil {
		t.Error("负责人不能作废")
	}
	both := issue.Roles(true, true)
	if got := issue.Allowed(issue.StatusCancelled, both); len(got) != 1 || got[0] != issue.ActComment {
		t.Errorf("已作废只能留言：%v", got)
	}
	if issue.StatusCancelled.Category() != issue.CategoryDone {
		t.Error("已作废算已结束")
	}
	want := map[issue.Status]bool{issue.StatusOpen: true, issue.StatusAccepted: true, issue.StatusFixing: true,
		issue.StatusReadyForVerification: true, issue.StatusReopened: true}
	for _, s := range []issue.Status{issue.StatusDraft, issue.StatusOpen, issue.StatusAccepted, issue.StatusFixing,
		issue.StatusReadyForVerification, issue.StatusReopened, issue.StatusClosed, issue.StatusDeclined, issue.StatusCancelled} {
		if issue.AdminCancellable(s) != want[s] {
			t.Errorf("管理员能否作废 %s：应为 %v", s, want[s])
		}
	}
}
