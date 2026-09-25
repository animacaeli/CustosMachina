package slots

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/pkg/jwt"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Name() string { return "slots" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/slots")
	{
		g.GET("/:projectId", h.list)
		g.POST("/:projectId/occupy", h.occupy)
		g.POST("/:projectId/:slotName/release", h.release)
		g.POST("/:projectId/:slotName/renew", h.renew)
	}
}

func (h *Handler) list(c *gin.Context) {
	pid, ok := projectParam(c)
	if !ok {
		return
	}
	out, err := h.svc.List(c.Request.Context(), pid)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) occupy(c *gin.Context) {
	pid, ok := projectParam(c)
	if !ok {
		return
	}
	var in OccupyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	uid, display := actor(c)
	slot, err := h.svc.Occupy(c.Request.Context(), pid, in, uid, display)
	if err != nil {
		if err == ErrOccupied {
			httpx.Fail(c, 409, 409, "槽位已被占用")
			return
		}
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, slot)
}

func (h *Handler) release(c *gin.Context) {
	pid, ok := projectParam(c)
	if !ok {
		return
	}
	uid, _ := actor(c)
	isAdmin := false
	if claims := jwt.ClaimsFromContext(c); claims != nil {
		isAdmin = claims.IsAdmin
	}
	if err := h.svc.Release(c.Request.Context(), pid, c.Param("slotName"), uid, isAdmin); err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) renew(c *gin.Context) {
	pid, ok := projectParam(c)
	if !ok {
		return
	}
	var in DurationInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	uid, _ := actor(c)
	isAdmin := false
	if claims := jwt.ClaimsFromContext(c); claims != nil {
		isAdmin = claims.IsAdmin
	}
	slot, err := h.svc.Renew(c.Request.Context(), pid, c.Param("slotName"), in, uid, isAdmin)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, slot)
}

func actor(c *gin.Context) (uint, string) {
	if claims := jwt.ClaimsFromContext(c); claims != nil {
		return claims.UserID, claims.DisplayName
	}
	return 0, "unknown"
}

func projectParam(c *gin.Context) (uint, bool) {
	id64, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
	if err != nil || id64 == 0 {
		httpx.FailBadRequest(c, "无效的 projectId")
		return 0, false
	}
	return uint(id64), true
}
