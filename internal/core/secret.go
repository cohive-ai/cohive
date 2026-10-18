package core

import (
	"strings"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/secretscan"
)

// maxListedFindings 是错误信息里最多列出的命中处数。
const maxListedFindings = 10

// SecretError 表示要写入的正文疑似包含密钥，已拒绝写入、什么都没有保存（安全红线第 5 条）。
// 只含字段、行号与规则类别，不含密钥本身、它的任何片段或所在行的内容，可以原样返回给调用方、写进日志。
type SecretError struct {
	Findings []secretscan.Finding
}

func (e *SecretError) Error() string { return e.Localize(i18n.Default()) }

// Localize 按语言渲染；规则类别也按这个语言写。
func (e *SecretError) Localize(l i18n.Lang) string {
	var b strings.Builder
	b.WriteString(l.T("core.err.secret_header") + "\n")
	for i, f := range e.Findings {
		if i == maxListedFindings {
			b.WriteString("- " + l.T("core.err.secret_more", i18n.D{"Count": len(e.Findings) - maxListedFindings}) + "\n")
			break
		}
		b.WriteString("- " + l.T("core.err.secret_line", i18n.D{"Field": f.Field, "Line": f.Line,
			"Category": f.CategoryIn(l), "Rule": f.RuleID}) + "\n")
	}
	b.WriteString(l.T("core.err.secret_footer"))
	return b.String()
}

// checkSecrets 扫描要写入的字段，命中时返回 *SecretError。所有写入问题单正文的入口都必须先调用它。
func checkSecrets(fields ...secretscan.Field) error {
	if f := secretscan.Scan(fields...); len(f) > 0 {
		return &SecretError{Findings: f}
	}
	return nil
}

func draftFields(in DraftInput) []secretscan.Field {
	return []secretscan.Field{
		{Name: "title", Text: in.Title}, {Name: "brief", Text: in.Brief}, {Name: "summary", Text: in.Summary},
		{Name: "expected", Text: in.Expected}, {Name: "actual", Text: in.Actual},
		{Name: "repro_script", Text: in.ReproScript}, {Name: "repro_env", Text: in.ReproEnv},
		{Name: "evidence", Text: in.Evidence}, {Name: "repo", Text: in.Repo}, {Name: "branch", Text: in.Branch},
		{Name: "commit", Text: in.Commit}, {Name: "target_service", Text: in.TargetService}, {Name: "owner", Text: in.Owner},
	}
}

func actFields(in ActInput) []secretscan.Field {
	// 接收人名字不入库，但查无此人时错误信息会带上它，一并扫描
	return []secretscan.Field{{Name: "note", Text: in.Note}, {Name: "assignee", Text: in.Assignee}}
}
