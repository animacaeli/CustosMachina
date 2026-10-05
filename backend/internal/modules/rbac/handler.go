package rbac

import (
	"github.com/gin-gonic/gin"
	"strconv"

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
	// 角色管理仅 admin（种子含 admin /roles 读+矩阵写；超管中间件放行）
	roles := r.Authed.Group("/roles")
	{
		roles.GET("", h.list)
		roles.PUT("/:role/policies", h.replace)
		// P7-M1：自定义角色 CRUD + 动作目录
		roles.GET("/actions", h.listActions)
		roles.POST("", h.createRole)
		roles.PUT("/:role", h.updateRole)
		roles.DELETE("/:role", h.deleteRole)
	}
	// 当前用户权限下发：任何登录用户可查自己的
	r.Authed.GET("/auth/permissions", h.myPermissions)
	// P6-M6 主机终端登录名级授权（堡垒机细粒度；admin）
	ta := r.Authed.Group("/rbac/terminal-acls")
	{
		ta.GET("/:serverId", h.getTerminalACLs)
		ta.PUT("/:serverId", h.setTerminalACLs)
	}
}

// listActions 动作目录（前端勾选面板数据源）。注意须先于 /:role 注册已保证不冲突。
func (h *Handler) listActions(c *gin.Context) {
	httpx.OK(c, ActionCatalog)
}

type saveRoleRequest struct {
	Name        string   `json:"name" binding:"required,max=32"`
	Description string   `json:"description" binding:"max=255"`
	Actions     []string `json:"actions" binding:"max=32,dive,max=64"`
	ProjectIDs  []uint   `json:"projectIds" binding:"max=100"`
}

// updateRoleRequest 更新载荷：角色名以路径为准、不可改。
type updateRoleRequest struct {
	Description string   `json:"description" binding:"max=255"`
	Actions     []string `json:"actions" binding:"max=32,dive,max=64"`
	ProjectIDs  []uint   `json:"projectIds" binding:"max=100"`
}

func (h *Handler) createRole(c *gin.Context) {
	var in saveRoleRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	claims := auth.ClaimsFromContext(c)
	role, err := h.svc.CreateRole(SaveRoleInput(in), claims != nil && claims.IsAdmin)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, role)
}

func (h *Handler) updateRole(c *gin.Context) {
	var in updateRoleRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	claims := auth.ClaimsFromContext(c)
	if err := h.svc.UpdateRole(c.Param("role"), SaveRoleInput{
		Name:        c.Param("role"),
		Description: in.Description,
		Actions:     in.Actions,
		ProjectIDs:  in.ProjectIDs,
	}, claims != nil && claims.IsAdmin); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) deleteRole(c *gin.Context) {
	if err := h.svc.DeleteRole(c.Param("role")); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) getTerminalACLs(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("serverId"), 10, 64)
	if err != nil || id == 0 {
		httpx.FailBadRequest(c, "serverId 无效")
		return
	}
	httpx.OK(c, h.svc.ServerTerminalACLs(uint(id)))
}

func (h *Handler) setTerminalACLs(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("serverId"), 10, 64)
	if err != nil || id == 0 {
		httpx.FailBadRequest(c, "serverId 无效")
		return
	}
	var in struct {
		Usernames []string `json:"usernames" binding:"max=100"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SetServerTerminalACLs(uint(id), in.Usernames); err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, nil)
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
