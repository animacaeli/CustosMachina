package auth

import (
	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/pkg/httpx"
)

// UserInfo 前端（vben）约定的用户信息结构。
type UserInfo struct {
	UserID      uint     `json:"userId"`
	Username    string   `json:"username"`
	RealName    string   `json:"realName"`
	Avatar      string   `json:"avatar,omitempty"`
	Roles       []string `json:"roles"`
	HomePath    string   `json:"homePath,omitempty"`
	Description string   `json:"description,omitempty"`
}

// userInfo GET /user/info：前端（vben）登录后拉取的用户信息（RBAC 中间件豁免路径）。
func (h *Handler) userInfo(c *gin.Context) {
	claims := ClaimsFromContext(c)
	if claims == nil {
		httpx.FailUnauthorized(c, "未认证")
		return
	}
	var roles []string
	if !claims.IsAdmin {
		u, err := h.svc.users.GetByID(c.Request.Context(), claims.UserID)
		if err != nil {
			httpx.Fail(c, 403, 403, "用户不存在")
			return
		}
		roles = identity.ParseRoleList(u.Roles)
		// P7-M1 菜单基线：仅绑自定义角色的用户在下发角色里补 dev——前端路由
		// authority 数组是内置角色名，不补则整个侧边栏只剩首页。后端 casbin
		// 仍按真实角色裁决，此处只影响菜单可见性（API 未授权面照旧 403）。
		hasMenuBaseline, hasCustom := false, false
		for _, r := range roles {
			switch {
			case r == "admin" || r == "ops" || r == "dev":
				hasMenuBaseline = true
			case !identity.IsBuiltinRole(r) &&
				identity.ExtraRoleValidator != nil &&
				identity.ExtraRoleValidator(r):
				hasCustom = true
			}
		}
		if hasCustom && !hasMenuBaseline {
			roles = append(roles, "dev")
		}
	} else {
		roles = []string{"superadmin"}
	}
	httpx.OK(c, UserInfo{
		UserID:   claims.UserID,
		Username: claims.DisplayName,
		RealName: claims.DisplayName,
		Roles:    roles,
	})
}
