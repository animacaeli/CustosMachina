// Package health 平台健康检查（无认证）。
package health

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/server"
)

type Handler struct{}

func NewHandler() *Handler { return &Handler{} }

func (h *Handler) Name() string { return "health" }

func (h *Handler) RegisterRoutes(r server.Router) {
	r.Public.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
}
