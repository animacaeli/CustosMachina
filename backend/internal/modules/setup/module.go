// Package setup 首次启动向导（FR1）。向导窗口 = 用户表为空；
// 步骤：① IM 提供商三选一（企微/钉钉/飞书）+ 凭证 ② Redis（可跳过）
// ③ 创建本地超管（break-glass，完成即关闭向导）。后续（组件纳管 / AI 配置 /
// 通知路由）随批次 2~3 补齐。
package setup

import (
	"errors"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/custos-machina/backend/internal/modules/auth"
	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

var ErrSetupClosed = errors.New("初始化已完成，向导已关闭")

type Handler struct {
	users *identity.UserService
	auth  *auth.SetupGate
}

func NewHandler(users *identity.UserService, authSvc *auth.AuthService) *Handler {
	return &Handler{users: users, auth: auth.NewSetupGate(authSvc)}
}

func (h *Handler) Name() string { return "setup" }

func (h *Handler) RegisterRoutes(r server.Router) {
	r.Public.GET("/setup/status", h.status)
	// 向导窗口（用户表为空）内开放的步骤；完成后永久关闭（FR1.1）
	r.Public.POST("/setup/im", h.setupIM)
	r.Public.POST("/setup/redis", h.setupRedis)
	r.Public.POST("/setup/admin", h.createAdmin)
}

func (h *Handler) status(c *gin.Context) {
	count, err := h.users.CountUsers(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"needed": count == 0})
}

// gate 向导是否仍开放（用户表为空）。
func (h *Handler) gate(c *gin.Context) bool {
	count, err := h.users.CountUsers(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return false
	}
	if count > 0 {
		httpx.Fail(c, 403, 403, ErrSetupClosed.Error())
		return false
	}
	return true
}

type setupIMInput struct {
	Provider string            `json:"provider" binding:"required,oneof=wecom dingtalk feishu"`
	Config   map[string]string `json:"config" binding:"required"`
	Enabled  bool              `json:"enabled"`
}

func (h *Handler) setupIM(c *gin.Context) {
	var in setupIMInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if !h.gate(c) {
		return
	}
	if err := h.auth.SaveIMProviderConfigMap(c.Request.Context(), in.Provider, in.Config, in.Enabled); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"saved": true})
}

type setupRedisInput struct {
	Addr     string `json:"addr" binding:"required"`
	Password string `json:"password"`
	DB       int    `json:"db"`
}

func (h *Handler) setupRedis(c *gin.Context) {
	var in setupRedisInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if !h.gate(c) {
		return
	}
	if err := h.auth.SaveRedis(c.Request.Context(), in.Addr, in.Password, in.DB); err != nil {
		httpx.Fail(c, 502, 502, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

type createAdminInput struct {
	Username    string `json:"username" binding:"required,min=3,max=64"`
	DisplayName string `json:"displayName"`
	Password    string `json:"password" binding:"required,min=8,max=72"`
}

func (h *Handler) createAdmin(c *gin.Context) {
	var in createAdminInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if !h.gate(c) {
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	displayName := in.DisplayName
	if displayName == "" {
		displayName = in.Username
	}
	u, err := h.users.EnsureLocalAdmin(c.Request.Context(), in.Username, displayName, string(hash))
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"username": u.Username, "displayName": u.DisplayName})
}
