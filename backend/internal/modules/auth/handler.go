package auth

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/pkg/ratelimit"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *AuthService
	// 公共认证接口限速（P5 M1 安全欠账）：登录失败锁定 + 扫码接口请求限速。
	// 单实例内存态；多实例化时落 Redis。
	loginLock   *ratelimit.Lockout
	loginWindow *ratelimit.Window
	qrWindow    *ratelimit.Window
}

func NewHandler(svc *AuthService) *Handler {
	return &Handler{
		svc:         svc,
		loginLock:   ratelimit.NewLockout(10, 10*time.Minute, 10*time.Minute),
		loginWindow: ratelimit.NewWindow(30, time.Minute),
		qrWindow:    ratelimit.NewWindow(60, time.Minute),
	}
}

func (h *Handler) Name() string { return "auth" }

func (h *Handler) RegisterRoutes(r server.Router) {
	r.Public.POST("/auth/login", h.loginLock.Middleware(), h.loginWindow.Middleware(), h.login)
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
		h.loginLock.ReportFail(c.ClientIP())
		httpx.FailUnauthorized(c, err.Error())
		return
	}
	h.loginLock.Reset(c.ClientIP())
	httpx.OK(c, result)
}

func (h *Handler) me(c *gin.Context) {
	httpx.OK(c, ClaimsFromContext(c))
}
