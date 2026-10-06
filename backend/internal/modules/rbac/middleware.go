package rbac

import (
	"net/http"
	"strconv"
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
	"/auth/logout":      true, // 登出吊销，任何已登录用户可用
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
		// 超管也每请求查库（主键查询，成本可忽略）：禁用/删除/降级即时生效，
		// 不再依赖 access token TTL 自然过期（v0.12.0 审计中等项：此前存在
		// 最长 30 分钟的全权真空期）
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
		if claims.IsAdmin {
			if !u.IsLocalAdmin {
				// 已被降级：按当前角色走 casbin 裁决，并纠正本请求内的
				// claims（rbac.Can 等 handler gate 读的是 claims.IsAdmin）
				claims.IsAdmin = false
			} else {
				c.Next()
				return
			}
		}
		obj := strings.TrimPrefix(c.Request.URL.Path, "/api")
		if !exemptAnyRole[obj] {
			// sub 除角色外并入登录名：用户级授权策略（如单主机终端授权，M6）
			// 生效点——username 只会命中显式写给它的策略，无通配副作用
			subs := identity.ParseRoleList(u.Roles)
			username := u.UsernameOf()
			if username != "" {
				subs = append(subs, username)
			}
			// P7-M1：主体塞入 context，动作 gate（rbac.Can）免二次查库
			c.Set(subjectCtxKey, subject{IsAdmin: false, Roles: identity.ParseRoleList(u.Roles), Username: username})
			if isTerminalPath(obj) {
				// 终端路径不走通配裁决：keyMatch 前缀语义下 /servers/* 会放行
				// dev/ops/自定义角色的读面通配，终端这种 root shell 级能力必须
				// 精确授权——只认内置 admin 角色或写给登录名的逐主机 ACL。
				if !TerminalAllowedFor(c, serverIDFromTerminalPath(obj), identity.ParseRoleList(u.Roles), username) {
					httpx.Fail(c, http.StatusForbidden, 403, "无该主机的终端授权（需管理员角色或主机级授权）")
					c.Abort()
					return
				}
			} else {
				ok, err := EnforceAny(deps.Enforcer, subs, obj, c.Request.Method)
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
		}
		c.Next()
	}
}

// isTerminalPath 主机 Web 终端资源点（WS 升级走 GET）。
func isTerminalPath(obj string) bool {
	return strings.HasPrefix(obj, "/servers/") && strings.HasSuffix(obj, "/terminal")
}

// serverIDFromTerminalPath 从 /servers/:id/terminal 提取主机 ID（格式不合法返回 0）。
func serverIDFromTerminalPath(obj string) uint {
	id64, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(obj, "/servers/"), "/terminal"), 10, 64)
	if err != nil {
		return 0
	}
	return uint(id64)
}
