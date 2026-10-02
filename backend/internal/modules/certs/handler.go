package certs

import (
	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Name() string { return "certs" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/certs")
	{
		g.GET("", h.list)
		g.POST("", h.create)
		g.PUT("/:id", h.update)
		g.DELETE("/:id", h.remove)
		g.POST("/:id/renew", h.renew)
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
	var in SaveInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Create(c.Request.Context(), in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, toOut(*out))
}

func (h *Handler) update(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in SaveInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Update(c.Request.Context(), id, in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, toOut(*out))
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
	httpx.OK(c, gin.H{"deleted": true})
}

func (h *Handler) renew(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.RenewNow(c.Request.Context(), id); err != nil {
		httpx.Fail(c, 502, 502, "签发/续期失败: "+err.Error())
		return
	}
	httpx.OK(c, gin.H{"renewed": true})
}
