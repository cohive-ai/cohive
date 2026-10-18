package authn_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cohive-ai/cohive/internal/authn"
	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// TestMain：固定服务器缺省语言为中文跑；英文另有用例。
func TestMain(m *testing.M) {
	i18n.SetDefault(i18n.ZhCN)
	os.Exit(m.Run())
}

// stub 是只返回固定错误的数据库：无此令牌（ErrNoRows）或数据库故障。
type stub struct{ err error }

type row struct{ err error }

func (r row) Scan(...any) error { return r.err }

func (s stub) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, s.err
}
func (s stub) Query(context.Context, string, ...any) (pgx.Rows, error) { return nil, s.err }
func (s stub) QueryRow(context.Context, string, ...any) pgx.Row        { return row{s.err} }

func verify(t *testing.T, dbErr error) (int, string) {
	t.Helper()
	h := authn.Middleware(stub{dbErr})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("鉴权失败时不应进入下游")
	}))
	r := httptest.NewRequest("POST", "/mcp", nil)
	r.Header.Set("Authorization", "Bearer anything")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}

// 鉴权失败的提示用服务器缺省语言，数据库故障原文不外泄。
func TestVerifierMessages(t *testing.T) {
	code, body := verify(t, pgx.ErrNoRows)
	if code != http.StatusUnauthorized || !strings.Contains(body, "令牌无效或已吊销") {
		t.Errorf("中文：%d %q", code, body)
	}
	code, body = verify(t, errors.New("closed pool: postgres"))
	if code == http.StatusOK || strings.Contains(body, "postgres") || !strings.Contains(body, "服务暂不可用") {
		t.Errorf("中文故障：%d %q", code, body)
	}

	i18n.SetDefault(i18n.EN)
	defer i18n.SetDefault(i18n.ZhCN)
	code, body = verify(t, pgx.ErrNoRows)
	if code != http.StatusUnauthorized || !strings.Contains(body, "token is invalid or has been revoked") {
		t.Errorf("英文：%d %q", code, body)
	}
	code, body = verify(t, errors.New("closed pool: postgres"))
	if strings.Contains(body, "postgres") || !strings.Contains(body, "Service temporarily unavailable") {
		t.Errorf("英文故障：%d %q", code, body)
	}
}
