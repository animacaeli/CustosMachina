package observ

import (
	"time"

	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/pkg/ratelimit"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
	// O2 告警回流限速（公开接口防刷）
	alertWebhookLimiter *ratelimit.Window
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, alertWebhookLimiter: ratelimit.NewWindow(60, time.Minute)}
}

func (h *Handler) Name() string { return "observ" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/observ")
	{
		g.GET("", h.list)
		g.PUT("/o2-url", h.setO2URL)
		g.GET("/status", h.status)
		g.POST("/deploy", h.deploy)
		g.POST("/uninstall", h.uninstall)
		// 告警闭环（P5 M3）
		g.GET("/o2-settings", h.getO2Settings)
		g.PUT("/o2-settings", h.putO2Settings)
		g.GET("/alerts", h.listAlerts)
		g.POST("/alerts", h.createAlert)
		g.PUT("/alerts/:id", h.updateAlert)
		g.DELETE("/alerts/:id", h.deleteAlert)
		g.POST("/alerts/sync", h.syncAllAlerts)
	}
	// O2 告警回流（公开；X-Custos-Token 校验 + 限速）
	r.Public.POST("/observ/alerts/webhook", h.alertWebhookLimiter.Middleware(), h.o2AlertWebhook)
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
	var in DeployInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Deploy(c.Request.Context(), in)
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

// ---- O2 告警闭环路由（P5 M3）----

func (h *Handler) getO2Settings(c *gin.Context) {
	httpx.OK(c, h.svc.O2Settings(c.Request.Context()))
}

func (h *Handler) putO2Settings(c *gin.Context) {
	var in O2SettingsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SaveO2Settings(c.Request.Context(), in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, h.svc.O2Settings(c.Request.Context()))
}

func (h *Handler) listAlerts(c *gin.Context) {
	out, err := h.svc.ListAlerts(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) createAlert(c *gin.Context) {
	var in SaveAlertInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.CreateAlert(c.Request.Context(), in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) updateAlert(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in SaveAlertInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if _, err := h.svc.UpdateAlert(c.Request.Context(), id, in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

func (h *Handler) deleteAlert(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteAlert(c.Request.Context(), id); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}

func (h *Handler) syncAllAlerts(c *gin.Context) {
	n, err := h.svc.SyncAllAlerts(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"synced": n})
}
