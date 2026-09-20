package rbac

import (
	"net/http"
	"strings"

	"github.com/casbin/casbin/v2"
	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/modules/auth"
	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/pkg/httpx"
)

// exemptAnyRole 任何已登录用户可访问的路径（自查信息），不进策略裁决。
var exemptAnyRole = map[string]bool{
	"/auth/me":          true,
	"/auth/permissions": true,
	"/user/info":        true,
}

// Middleware 返回 casbin 鉴权中间件，挂在 JWT 认证之后。
// 裁决链：本地超管（claims.IsAdmin）直接放行 → 查库取用户当前角色（角色变更即时生效）
// → 逐角色 Enforce（对象为去掉 /api 前缀的路径）。
type MiddlewareDeps struct {
	Enforcer *casbin.SyncedEnforcer
	Users    identity.UserRepository
}

func NewMiddleware(deps MiddlewareDeps) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims := auth.ClaimsFromContext(c)
		if claims == nil {
			httpx.FailUnauthorized(c, "未认证")
			c.Abort()
			return
		}
		if claims.IsAdmin {
			c.Next()
			return
		}
		u, err := deps.Users.GetByID(c.Request.Context(), claims.UserID)
		if err != nil {
			httpx.Fail(c, http.StatusForbidden, 403, "用户不存在或已删除")
			c.Abort()
			return
		}
		if u.Status == identity.StatusDisabled {
			httpx.Fail(c, http.StatusForbidden, 403, "账号已禁用")
			c.Abort()
			return
		}
		obj := strings.TrimPrefix(c.Request.URL.Path, "/api")
		if !exemptAnyRole[obj] {
			ok, err := EnforceAny(deps.Enforcer, identity.ParseRoleList(u.Roles), obj, c.Request.Method)
			if err != nil {
				httpx.FailServer(c, err)
				c.Abort()
				return
			}
			if !ok {
				httpx.Fail(c, http.StatusForbidden, 403, "无权访问该资源")
				c.Abort()
				return
			}
		}
		c.Next()
	}
}
