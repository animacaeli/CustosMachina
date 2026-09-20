// Package server 组装 HTTP 服务：gin 引擎、全局中间件、模块路由注册。
// 业务模块实现 Module 接口并由 wire 注入 Modules 列表，新增模块无需改动本包。
package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/config"
)

// Router 交给各模块的路由注册视图：
// Public 挂在 /api 下不做认证（登录、健康检查、webhook 等）；
// Authed 挂在 /api 下并已应用 JWT 认证，模块内按需追加 casbin 鉴权。
type Router struct {
	Public *gin.RouterGroup
	Authed *gin.RouterGroup
}

// Module 是业务模块的接入契约。模块名需唯一，用于诊断与路由分组。
type Module interface {
	Name() string
	RegisterRoutes(r Router)
}

// Modules 聚合所有注入的模块（wire 用）。
type Modules []Module

func (m Modules) Lookup(name string) (Module, bool) {
	for _, mod := range m {
		if mod.Name() == name {
			return mod, true
		}
	}
	return nil, false
}

// AuthMiddleware 返回 JWT 认证中间件；具体实现由 auth 模块通过 wire 注入。
type AuthMiddleware func() gin.HandlerFunc

type Server struct {
	cfg  *config.Config
	http *http.Server
}

func New(cfg *config.Config, engine *gin.Engine) *Server {
	return &Server{cfg: cfg, http: &http.Server{
		Addr:    cfg.HTTP.Addr,
		Handler: engine,
	}}
}

func (s *Server) Run() error {
	if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http 服务退出: %w", err)
	}
	return nil
}

// GracefulShutdown 按配置的超时时间优雅停机。
func (s *Server) GracefulShutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.HTTP.ShutdownTimeout)
	defer cancel()
	return s.http.Shutdown(ctx)
}
