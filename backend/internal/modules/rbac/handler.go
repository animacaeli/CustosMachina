package rbac

import (
	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/modules/auth"
	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc   *Service
	users identity.UserRepository
}

func NewHandler(svc *Service, users identity.UserRepository) *Handler {
	return &Handler{svc: svc, users: users}
}

func (h *Handler) Name() string { return "rbac" }

func (h *Handler) RegisterRoutes(r server.Router) {
	// 权限矩阵管理仅 admin（中间件对非 admin 校验策略，默认矩阵不含 /roles，即仅超管可用）
	roles := r.Authed.Group("/roles")
	{
		roles.GET("", h.list)
		roles.PUT("/:role/policies", h.replace)
	}
	// 当前用户权限下发：任何登录用户可查自己的
	r.Authed.GET("/auth/permissions", h.myPermissions)
}

func (h *Handler) list(c *gin.Context) {
	httpx.OK(c, h.svc.ListRoles())
}

type replaceInput struct {
	Policies []Policy `json:"policies" binding:"required,dive"`
}

func (h *Handler) replace(c *gin.Context) {
	role := c.Param("role")
	var in replaceInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.ReplaceRolePolicies(role, in.Policies); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, h.svc.listPolicies(role))
}

func (h *Handler) myPermissions(c *gin.Context) {
	claims := auth.ClaimsFromContext(c)
	if claims == nil {
		httpx.FailUnauthorized(c, "未认证")
		return
	}
	var roles []string
	if !claims.IsAdmin {
		if u, err := h.users.GetByID(c.Request.Context(), claims.UserID); err == nil {
			roles = identity.ParseRoleList(u.Roles)
		}
	}
	httpx.OK(c, h.svc.UserPermissions(roles, claims.IsAdmin))
}
