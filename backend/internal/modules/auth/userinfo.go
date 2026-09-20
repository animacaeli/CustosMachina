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
	} else {
		roles = []string{"admin", "super"}
	}
	httpx.OK(c, UserInfo{
		UserID:   claims.UserID,
		Username: claims.DisplayName,
		RealName: claims.DisplayName,
		Roles:    roles,
	})
}
