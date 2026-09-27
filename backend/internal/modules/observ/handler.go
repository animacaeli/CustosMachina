package observ

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Name() string { return "observ" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/observ")
	{
		g.GET("", h.list)
		g.PUT("/o2-url", h.setO2URL)
		g.GET("/status", h.status)
		g.POST("/deploy", h.deploy)
		g.POST("/uninstall", h.uninstall)
	}
}

func (h *Handler) list(c *gin.Context) {
	res, err := h.svc.List(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) setO2URL(c *gin.Context) {
	var in struct {
		URL string `json:"url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SetO2URL(c.Request.Context(), in.URL); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) status(c *gin.Context) {
	serverID := uint(atoiDefault(c.Query("serverId"), 0))
	if serverID == 0 {
		httpx.FailBadRequest(c, "serverId 必填")
		return
	}
	res, err := h.svc.Status(c.Request.Context(), serverID)
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) deploy(c *gin.Context) {
	var in struct {
		ServerID  uint   `json:"serverId" binding:"required"`
		Component string `json:"component" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Deploy(c.Request.Context(), in.ServerID, in.Component)
	if err != nil {
		httpx.FailUpstream(c, err.Error()+"\n"+out)
		return
	}
	httpx.OK(c, gin.H{"output": out})
}

func (h *Handler) uninstall(c *gin.Context) {
	var in struct {
		ServerID  uint   `json:"serverId" binding:"required"`
		Component string `json:"component" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Uninstall(c.Request.Context(), in.ServerID, in.Component)
	if err != nil {
		httpx.FailUpstream(c, err.Error()+"\n"+out)
		return
	}
	httpx.OK(c, gin.H{"output": out})
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
