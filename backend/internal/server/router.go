package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/config"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

// NewEngine 构建 gin 引擎并完成所有模块路由注册。
// auth 为认证中间件（JWT），authz 为鉴权中间件（casbin），均由业务模块注入。
func NewEngine(cfg *config.Config, modules Modules, auth AuthMiddleware, authz gin.HandlerFunc) *gin.Engine {
	gin.SetMode(cfg.HTTP.Mode)
	e := gin.New()
	e.Use(requestLogger(), gin.Recovery(), cors(cfg.CORS.Origins))

	api := e.Group("/api")
	public := api.Group("")
	authed := api.Group("")
	authed.Use(auth(), authz)

	router := Router{Public: public, Authed: authed}
	for _, mod := range modules {
		mod.RegisterRoutes(router)
	}
	return e
}

// requestLogger 用 zap 记录请求访问日志（替代 gin.Logger 的标准库输出）。
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Infof("[gin] %3d | %13v | %-7s %s",
			c.Writer.Status(), time.Since(start), c.Request.Method, c.Request.URL.Path)
	}
}

func cors(origins string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		allowed := false
		for _, o := range strings.Split(origins, ",") {
			if strings.TrimSpace(o) == "*" || strings.TrimSpace(o) == origin {
				allowed = true
				break
			}
		}
		if origin != "" && allowed {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
