// Package setup 首次启动向导（FR1）。向导窗口 = 用户表为空；
// 仅创建本地超管（break-glass），完成即关闭。其余全部配置（IM 提供商、
// Redis、token 有效期等）登录后在「管理后台」设置。
package setup

import (
	"errors"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

var ErrSetupClosed = errors.New("初始化已完成，向导已关闭")

type Handler struct {
	users *identity.UserService
}

func NewHandler(users *identity.UserService) *Handler {
	return &Handler{users: users}
}

func (h *Handler) Name() string { return "setup" }

func (h *Handler) RegisterRoutes(r server.Router) {
	r.Public.GET("/setup/status", h.status)
	// 完成后向导永久关闭（FR1.1）；其余配置（IM / Redis）在管理后台设置
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
	httpx.OK(c, gin.H{"username": u.UsernameOf(), "displayName": u.DisplayName})
}
