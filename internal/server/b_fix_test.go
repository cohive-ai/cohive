package server_test

import (
	"testing"
)

// B1：改密码只能在网页会话里做，令牌不行（没设过密码的成员可不验旧密码直接设）。
func TestPasswordChangeRequiresSession(t *testing.T) {
	f := newFixture(t, builtWeb)
	b := newBrowser(t, f.url)
	b.origin = ""
	code, out := b.do("POST", "/api/v1/auth/password", map[string]string{"old_password": "", "new_password": "token-set-password"}, f.tokenA)
	if code != 403 {
		t.Fatalf("令牌不能改密码：%d %v", code, out)
	}
}
