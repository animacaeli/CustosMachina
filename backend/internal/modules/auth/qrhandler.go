package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

// registerQRLoginRoutes 扫码登录相关路由（公开）+ 提供商配置管理（admin）。
func (h *Handler) registerQRLoginRoutes(r server.Router) {
	r.Public.GET("/auth/qrlogin/url", h.qrLoginURL)
	r.Public.GET("/auth/qrlogin/callback", h.qrCallback)

	im := r.Authed.Group("/im-configs")
	{
		im.GET("/wecom", h.wecomStatus)
		im.PUT("/wecom", h.wecomSave)
		im.POST("/wecom/verify", h.wecomVerify)
	}
}

func (h *Handler) qrLoginURL(c *gin.Context) {
	url, err := h.svc.QRLoginURL(c.Request.Context())
	if err != nil {
		httpx.Fail(c, http.StatusServiceUnavailable, 503, err.Error())
		return
	}
	httpx.OK(c, gin.H{"url": url})
}

func (h *Handler) qrCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	result, err := h.svc.HandleQRCallback(c.Request.Context(), code, state)
	if err != nil {
		// 回调由企微浏览器发起，返回 JSON 无意义，重定向到前端错误态
		httpx.OK(c, gin.H{"error": err.Error()})
		return
	}
	c.Redirect(http.StatusFound, h.svc.FrontendCallbackURL(result.Token))
}

type wecomConfigInput struct {
	CorpID  string `json:"corpId" binding:"required"`
	AgentID string `json:"agentId" binding:"required"`
	Secret  string `json:"secret" binding:"required"`
	Enabled bool   `json:"enabled"`
}

func (h *Handler) wecomStatus(c *gin.Context) {
	status, err := h.svc.WeComStatus(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, status)
}

func (h *Handler) wecomSave(c *gin.Context) {
	var in wecomConfigInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SaveWeComConfig(c.Request.Context(), WeComConfig{
		CorpID: in.CorpID, AgentID: in.AgentID, Secret: in.Secret,
	}, in.Enabled); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"saved": true})
}

// wecomVerify 测试凭证连通性；body 为空时测试已保存凭证。
func (h *Handler) wecomVerify(c *gin.Context) {
	var in *wecomConfigInput
	if err := c.ShouldBindJSON(&in); err != nil {
		in = nil // 允许空 body：测试已保存的凭证
	}
	var cfg *WeComConfig
	if in != nil && in.CorpID != "" {
		cfg = &WeComConfig{CorpID: in.CorpID, AgentID: in.AgentID, Secret: in.Secret}
	}
	if err := h.svc.VerifyWeComConfig(c.Request.Context(), cfg); err != nil {
		httpx.Fail(c, http.StatusBadGateway, 502, "连通性测试失败: "+err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}
