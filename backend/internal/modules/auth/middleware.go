package auth

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	jwtpkg "github.com/custos-machina/backend/internal/pkg/jwt"
	"github.com/custos-machina/backend/internal/server"
)

// CtxClaims 兼容旧引用；实际存取统一走 jwt 包。
const CtxClaims = jwtpkg.CtxClaimsKey

// Middleware 返回 JWT 认证中间件；通过 server.AuthMiddleware 注入引擎。
// token 优先取 Authorization: Bearer；缺失时按序回退：
//   - query 参数 `ticket`：一次性短时 ticket（WS/SSE 用，见 ticket.go，用完即焚）
//   - query 参数 `token`：直传 JWT（兼容旧路径，不推荐，会暴露给访问日志）
func (s *AuthService) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token, _ := strings.CutPrefix(header, "Bearer ")
		if token == "" {
			if t := c.Query("ticket"); t != "" {
				if claims := s.tickets.Consume(t); claims != nil {
					c.Set(CtxClaims, claims)
					c.Next()
					return
				}
				httpx.FailUnauthorized(c, "ticket 无效或已使用")
				c.Abort()
				return
			}
			token = c.Query("token")
		}
		if token == "" {
			httpx.FailUnauthorized(c, "缺少 Bearer token")
			c.Abort()
			return
		}
		claims, err := s.ParseToken(token)
		if err != nil {
			httpx.FailUnauthorized(c, err.Error())
			c.Abort()
			return
		}
		c.Set(CtxClaims, claims)
		c.Next()
	}
}

// ProvideAuthMiddleware 适配 server.AuthMiddleware 函数类型（wire 绑定）。
func ProvideAuthMiddleware(s *AuthService) server.AuthMiddleware { return s.Middleware }

// ClaimsFromContext 供其他模块读取当前登录人。
var ClaimsFromContext = jwtpkg.ClaimsFromContext
