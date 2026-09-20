package identity

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *UserService
}

func NewHandler(svc *UserService) *Handler { return &Handler{svc: svc} }

func (h *Handler) Name() string { return "identity" }

func (h *Handler) RegisterRoutes(r server.Router) {
	users := r.Authed.Group("/users")
	{
		users.GET("", h.list)
		users.POST("", h.create)
		users.PUT("/:id/roles", h.updateRoles)
	}
}

func (h *Handler) list(c *gin.Context) {
	users, err := h.svc.List(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, users)
}

func (h *Handler) create(c *gin.Context) {
	var in CreateUserInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	u, err := h.svc.Create(c.Request.Context(), in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, u)
}

type updateRolesInput struct {
	Roles string `json:"roles" binding:"required"`
}

func (h *Handler) updateRoles(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		httpx.FailBadRequest(c, "非法的用户 id")
		return
	}
	var in updateRolesInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	u, err := h.svc.UpdateRoles(c.Request.Context(), uint(id), in.Roles)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			httpx.Fail(c, 404, 404, err.Error())
			return
		}
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, u)
}
