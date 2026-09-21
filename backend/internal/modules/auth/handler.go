package auth

import (
	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *AuthService
}

func NewHandler(svc *AuthService) *Handler { return &Handler{svc: svc} }

func (h *Handler) Name() string { return "auth" }

func (h *Handler) RegisterRoutes(r server.Router) {
	r.Public.POST("/auth/login", h.login)
	r.Authed.GET("/auth/me", h.me)
	r.Authed.GET("/user/info", h.userInfo)
	h.registerQRLoginRoutes(r)
}

type loginInput struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (h *Handler) login(c *gin.Context) {
	var in loginInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	result, err := h.svc.LoginLocal(c.Request.Context(), in.Username, in.Password)
	if err != nil {
		httpx.FailUnauthorized(c, err.Error())
		return
	}
	httpx.OK(c, result)
}

func (h *Handler) me(c *gin.Context) {
	httpx.OK(c, ClaimsFromContext(c))
}
