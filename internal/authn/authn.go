// Package authn 用成员令牌鉴权 HTTP 请求，MCP 与网页接口共用。
package authn

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/cohive-ai/cohive/internal/i18n"
	"github.com/cohive-ai/cohive/internal/store"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

const identityKey = "cohive.identity"

// Middleware 要求请求携带有效的 Bearer 令牌。每个请求都查库校验，吊销立即生效。
func Middleware(q store.DBTX) func(http.Handler) http.Handler {
	verifier := func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		id, err := store.LookupToken(ctx, q, token)
		if errors.Is(err, store.ErrUnauthorized) {
			// 令牌无效时不知道是谁：用服务器缺省语言
			return nil, i18n.Wrap(auth.ErrInvalidToken, "api.authn.token_invalid")
		}
		if err != nil {
			// 不把数据库错误原文返回给尚未鉴权的请求方
			log.Printf("token verification failed: %v", err)
			return nil, i18n.Errorf("api.authn.unavailable")
		}
		return &auth.TokenInfo{
			UserID: "member:" + strconv.FormatInt(id.MemberID, 10) + "/token:" + strconv.FormatInt(id.TokenID, 10),
			Extra:  map[string]any{identityKey: id},
		}, nil
	}
	return auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{AllowMissingExpiration: true})
}

// FromTokenInfo 取出鉴权中间件放入的身份。
func FromTokenInfo(ti *auth.TokenInfo) (store.Identity, bool) {
	if ti == nil {
		return store.Identity{}, false
	}
	id, ok := ti.Extra[identityKey].(store.Identity)
	return id, ok
}

// FromContext 从请求上下文取出身份。
func FromContext(ctx context.Context) (store.Identity, bool) {
	return FromTokenInfo(auth.TokenInfoFromContext(ctx))
}
