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
	r.Authed.POST("/auth/tickets", h.issueTicket)
	h.registerQRLoginRoutes(r)
}

// issueTicket 签发一次性短时 ticket（WS/SSE 握手用，见 ticket.go）。
func (h *Handler) issueTicket(c *gin.Context) {
	claims := ClaimsFromContext(c)
	if claims == nil {
		httpx.FailUnauthorized(c, "未认证")
		return
	}
	t, err := h.svc.tickets.Issue(claims)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"ticket": t, "ttlSeconds": int(ticketTTL.Seconds())})
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
