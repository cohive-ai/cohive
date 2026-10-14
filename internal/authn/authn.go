// Package authn 用成员令牌鉴权 HTTP 请求，MCP 与网页接口共用。
package authn

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/cohive-ai/cohive/internal/store"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

const identityKey = "cohive.identity"

// Middleware 要求请求携带有效的 Bearer 令牌。每个请求都查库校验，吊销立即生效。
func Middleware(q store.DBTX) func(http.Handler) http.Handler {
	verifier := func(ctx context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		id, err := store.LookupToken(ctx, q, token)
		if errors.Is(err, store.ErrUnauthorized) {
			return nil, fmt.Errorf("%w：令牌无效或已吊销", auth.ErrInvalidToken)
		}
		if err != nil {
			// 不把数据库错误原文返回给尚未鉴权的请求方
			log.Printf("校验令牌失败：%v", err)
			return nil, errors.New("服务暂不可用")
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
