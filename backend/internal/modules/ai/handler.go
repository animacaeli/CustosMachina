package ai

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	relay *Service
}

func NewHandler(relay *Service) *Handler { return &Handler{relay: relay} }

func (h *Handler) Name() string { return "ai" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/ai")
	{
		g.GET("/settings", h.getSettings)
		g.PUT("/settings", h.putSettings)
		g.POST("/test", h.test)
		g.GET("/usages", h.usages)
	}
}

func (h *Handler) getSettings(c *gin.Context) {
	httpx.OK(c, h.relay.Settings(c.Request.Context()))
}

func (h *Handler) putSettings(c *gin.Context) {
	var in struct {
		Endpoint string `json:"endpoint" binding:"omitempty,max=255"`
		Model    string `json:"model" binding:"omitempty,max=64"`
		APIKey   string `json:"apiKey" binding:"omitempty,max=255"`
		// P7-M4：上下文窗口（token；0 = 恢复默认 32768）
		ContextWindow *int `json:"contextWindow" binding:"omitempty,min=0,max=2000000"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.relay.SaveSettings(c.Request.Context(), in.Endpoint, in.Model, in.APIKey); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if in.ContextWindow != nil {
		if err := h.relay.SaveContextWindow(c.Request.Context(), *in.ContextWindow); err != nil {
			httpx.FailServer(c, err)
			return
		}
	}
	httpx.OK(c, h.relay.Settings(c.Request.Context()))
}

func (h *Handler) test(c *gin.Context) {
	if err := h.relay.Test(c.Request.Context()); err != nil {
		httpx.Fail(c, http.StatusBadGateway, 502, "连通性测试失败: "+err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

func (h *Handler) usages(c *gin.Context) {
	var us []Usage
	q := h.relay.db.WithContext(c.Request.Context()).Order("id DESC").Limit(50)
	if caller := strings.TrimSpace(c.Query("caller")); caller != "" {
		q = q.Where("caller = ?", caller)
	}
	if err := q.Find(&us).Error; err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, us)
}
