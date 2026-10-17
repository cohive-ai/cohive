package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/core"
)

// 版本冲突与“事务被数据库并发中止”都回 409 并附可读的提示，不当成内部错误。
func TestWriteErrorConflicts(t *testing.T) {
	for _, err := range []error{&core.ConflictError{IssueID: 1, Expected: 2, Current: 3}, &core.RetryError{Code: "40P01"}} {
		w := httptest.NewRecorder()
		writeError(w, err)
		if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "最新") {
			t.Fatalf("%T 应回 409 与重读提示：%d %s", err, w.Code, w.Body.String())
		}
	}
}
