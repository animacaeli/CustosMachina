// handler.go k3s 集群管理 HTTP（admin，casbin v23）。
package k3s

import (
	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Name() string { return "k3s" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/k3s-clusters")
	{
		g.GET("", h.list)
		g.POST("", h.create)
		g.PUT("/:id", h.update)
		g.DELETE("/:id", h.remove)
		g.POST("/:id/test", h.test)
	}
}

func (h *Handler) list(c *gin.Context) {
	out, err := h.svc.List(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) create(c *gin.Context) {
	var in SaveClusterInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Save(c.Request.Context(), 0, in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) update(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in SaveClusterInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Save(c.Request.Context(), id, in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) remove(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) test(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	version, err := h.svc.Test(c.Request.Context(), id)
	if err != nil {
		httpx.Fail(c, 502, 502, err.Error())
		return
	}
	httpx.OK(c, gin.H{"version": version})
}
