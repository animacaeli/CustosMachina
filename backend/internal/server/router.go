package server

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/config"
)

// NewEngine 构建 gin 引擎并完成所有模块路由注册。
// auth 为认证中间件（JWT），authz 为鉴权中间件（casbin），均由业务模块注入。
func NewEngine(cfg *config.Config, modules Modules, auth AuthMiddleware, authz gin.HandlerFunc) *gin.Engine {
	gin.SetMode(cfg.HTTP.Mode)
	e := gin.New()
	e.Use(gin.Logger(), gin.Recovery(), cors())

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

func cors() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
