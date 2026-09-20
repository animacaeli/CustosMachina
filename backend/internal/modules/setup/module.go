// Package setup 首次启动向导（FR1）：status 判断用户表是否为空；
// createAdmin 创建本地超管（break-glass，FR1.3）。后续步骤（IM 提供商 / 组件纳管 /
// AI 配置 / 通知路由）随批次 2~3 补齐。
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

func NewHandler(users *identity.UserService) *Handler { return &Handler{users: users} }

func (h *Handler) Name() string { return "setup" }

func (h *Handler) RegisterRoutes(r server.Router) {
	r.Public.GET("/setup/status", h.status)
	// 完成后向导永久关闭（FR1.1）：createAdmin 仅在用户表为空时可用。
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
	ctx := c.Request.Context()
	count, err := h.users.CountUsers(ctx)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	if count > 0 {
		httpx.Fail(c, 403, 403, ErrSetupClosed.Error())
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
	u, err := h.users.EnsureLocalAdmin(ctx, in.Username, displayName, string(hash))
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"username": u.Username, "displayName": u.DisplayName})
}
