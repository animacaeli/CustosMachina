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
func (s *AuthService) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		token, ok := strings.CutPrefix(header, "Bearer ")
		if !ok || token == "" {
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
