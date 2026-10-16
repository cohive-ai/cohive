package core

import (
	"fmt"
	"strings"

	"github.com/cohive-ai/cohive/internal/secretscan"
)

// maxListedFindings 是错误信息里最多列出的命中处数。
const maxListedFindings = 10

// SecretError 表示要写入的正文疑似包含密钥，已拒绝写入、什么都没有保存（安全红线第 5 条）。
// 只含字段、行号与规则类别，不含密钥本身、它的任何片段或所在行的内容，可以原样返回给调用方、写进日志。
type SecretError struct {
	Findings []secretscan.Finding
}

func (e *SecretError) Error() string {
	var b strings.Builder
	b.WriteString("内容里疑似有密钥，已拒绝写入，什么都没有保存：\n")
	for i, f := range e.Findings {
		if i == maxListedFindings {
			fmt.Fprintf(&b, "- 另有 %d 处\n", len(e.Findings)-maxListedFindings)
			break
		}
		fmt.Fprintf(&b, "- %s 第 %d 行：%s（规则 %s）\n", f.Field, f.Line, f.Category, f.RuleID)
	}
	b.WriteString("请把这些位置的密钥换成 [已脱敏] 之类的占位后重试。不要把原值展示给用户，也不要写进别的字段。")
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
