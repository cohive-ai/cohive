package store

import "context"

// ListBoard 返回看板上的问题单：查看者可见的进行中单子（含自己的草稿），
// 以及 7 天内结束的单子（已作废的也在内，网页默认隐藏）。
func ListBoard(ctx context.Context, q DBTX, wsID, viewerID int64) ([]Issue, error) {
	rows, err := q.Query(ctx, `SELECT `+issueColumns+issueFrom+`
		WHERE i.workspace_id = $1
		  AND (i.status <> 'DRAFT' OR i.source_member_id = $2)
		  AND (i.status NOT IN ('CLOSED','DECLINED','CANCELLED') OR i.updated_at > now() - interval '7 days')
		ORDER BY i.updated_at DESC, i.id DESC`, wsID, viewerID)
	if err != nil {
		return nil, err
	}
	return collectIssues(rows)
}
