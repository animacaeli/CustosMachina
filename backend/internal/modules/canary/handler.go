package canary

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

func (h *Handler) Name() string { return "canary" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/canary-policies")
	{
		g.GET("/:projectId", h.list)
		g.POST("/:projectId", h.create)
		g.PUT("/:projectId/:id", h.update)
		g.DELETE("/:projectId/:id", h.remove)
		g.POST("/:projectId/publish", h.publish)
	}
}

func (h *Handler) list(c *gin.Context) {
	pid, ok := projectParam(c)
	if !ok {
		return
	}
	ps, current, err := h.svc.List(c.Request.Context(), pid)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"policies": ps, "publishedVersion": current})
}

func (h *Handler) create(c *gin.Context) {
	pid, ok := projectParam(c)
	if !ok {
		return
	}
	var in SavePolicyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	p, err := h.svc.Create(c.Request.Context(), pid, in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, p)
}

func (h *Handler) update(c *gin.Context) {
	pid, ok := projectParam(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in SavePolicyInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	p, err := h.svc.Update(c.Request.Context(), pid, id, in)
	if err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, p)
}

func (h *Handler) remove(c *gin.Context) {
	pid, ok := projectParam(c)
	if !ok {
		return
	}
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), pid, id); err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) publish(c *gin.Context) {
	pid, ok := projectParam(c)
	if !ok {
		return
	}
	operator := "unknown"
	if claims := jwt.ClaimsFromContext(c); claims != nil && claims.DisplayName != "" {
		operator = claims.DisplayName
	}
	version, output, err := h.svc.Publish(c.Request.Context(), pid, operator)
	if err != nil {
		httpx.FailUpstream(c, err.Error()+outputTail(output))
		return
	}
	httpx.OK(c, gin.H{"version": version, "output": output})
}

func outputTail(out string) string {
	if out == "" {
		return ""
	}
	if len(out) > 400 {
		return "\n" + out[len(out)-400:]
	}
	return "\n" + out
}

func projectParam(c *gin.Context) (uint, bool) {
	id64, err := strconv.ParseUint(c.Param("projectId"), 10, 64)
	if err != nil || id64 == 0 {
		httpx.FailBadRequest(c, "无效的 projectId")
		return 0, false
	}
	return uint(id64), true
}

func idParam(c *gin.Context) (uint, bool) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id64 == 0 {
		httpx.FailBadRequest(c, "无效的 id")
		return 0, false
	}
	return uint(id64), true
}
