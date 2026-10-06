package auth

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"fmt"
	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/pkg/logger"
	"github.com/custos-machina/backend/internal/server"
	"strings"
)

// registerQRLoginRoutes 扫码登录相关路由（公开）+ 会话与配置管理。
func (h *Handler) registerQRLoginRoutes(r server.Router) {
	qrm := h.qrWindow.Middleware()
	r.Public.GET("/auth/qrlogin/url", qrm, h.qrLoginURL)
	r.Public.GET("/auth/qrlogin/callback", qrm, h.qrCallback)
	r.Public.POST("/auth/qrlogin/exchange", qrm, h.qrExchange)
	r.Public.POST("/auth/refresh", h.refresh)
	r.Authed.POST("/auth/logout", h.logout)

	im := r.Authed.Group("/im-configs")
	{
		im.GET("", h.imStatus)
		im.PUT("/:provider", h.imSave)
		im.POST("/:provider/verify", h.imVerify)
	}

	st := r.Authed.Group("/settings")
	{
		st.GET("/token-ttl", h.tokenTTLGet)
		st.PUT("/token-ttl", h.tokenTLTPut)
		st.GET("/redis", h.redisGet)
		st.PUT("/redis", h.redisPut)
	}
}

func (h *Handler) qrLoginURL(c *gin.Context) {
	result, err := h.svc.QRLoginURL(c.Request.Context())
	if err != nil {
		// 公开接口：细节进日志，对外只给泛化文案（避免暴露部署配置）
		logger.Warnf("[auth] 生成扫码登录地址失败: %v", err)
		httpx.Fail(c, http.StatusServiceUnavailable, 503, "扫码登录暂不可用，请稍后重试或联系管理员")
		return
	}
	httpx.OK(c, result)
}

// qrExchange 内嵌登录面板（企微 JSSDK login_type=code）回调 code 换 token，
// 走 AJAX 而非 iframe 302，前端直接落会话。
func (h *Handler) qrExchange(c *gin.Context) {
	var in struct {
		Code  string `json:"code" binding:"required"`
		State string `json:"state" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	result, err := h.svc.HandleQRCallback(c.Request.Context(), in.Code, in.State)
	if err != nil {
		httpx.Fail(c, http.StatusUnauthorized, 401, err.Error())
		return
	}
	httpx.OK(c, gin.H{
		"accessToken": result.AccessToken, "refreshToken": result.RefreshToken,
	})
}

func (h *Handler) qrCallback(c *gin.Context) {
	code := c.Query("code")
	state := c.Query("state")
	result, err := h.svc.HandleQRCallback(c.Request.Context(), code, state)
	if err != nil {
		// 回调发生在授权 iframe 内，JSON 无处渲染；重定向回前端扫码页展示错误
		fe := h.svc.FrontendErrorURL(err.Error())
		c.Redirect(http.StatusFound, fe)
		return
	}
	c.Redirect(http.StatusFound, h.svc.FrontendCallbackURL(result.AccessToken, result.RefreshToken))
}

type refreshInput struct {
	RefreshToken string `json:"refreshToken" binding:"required"`
}

// refresh 用 refresh token 换新的 token 对（轮换）。
func (h *Handler) refresh(c *gin.Context) {
	var in refreshInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	result, err := h.svc.Refresh(c.Request.Context(), in.RefreshToken)
	if err != nil {
		httpx.FailUnauthorized(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{
		"accessToken": result.AccessToken, "refreshToken": result.RefreshToken,
	})
}

// logout 吊销 refresh token（access 短效自然过期）。
func (h *Handler) logout(c *gin.Context) {
	var in refreshInput
	_ = c.ShouldBindJSON(&in) // 允许空 body：access 自行过期
	if in.RefreshToken != "" {
		if err := h.svc.Logout(c.Request.Context(), in.RefreshToken); err != nil {
			httpx.FailServer(c, err)
			return
		}
	}
	httpx.OK(c, gin.H{"ok": true})
}

// --- IM 提供商配置（通用，三家） ---

func (h *Handler) imStatus(c *gin.Context) {
	httpx.OK(c, h.svc.IMProviderStatus(c.Request.Context()))
}

type imSaveInput struct {
	Config  map[string]string `json:"config" binding:"required"`
	Enabled bool              `json:"enabled"`
}

// imRequiredFields P8-M1：保存时的必填字段校验（#13——残缺凭证原先要到
// verify/扫码时才暴露，保存即报错把反馈提前到填写时）。
var imRequiredFields = map[string][]string{
	"wecom":    {"corpId", "agentId", "secret"},
	"dingtalk": {"clientKey", "clientSecret"},
	"feishu":   {"appId", "appSecret"},
}

func validateIMConfig(provider string, cfg map[string]string) error {
	req, ok := imRequiredFields[provider]
	if !ok {
		return fmt.Errorf("未知 IM 提供商: %s", provider)
	}
	for _, k := range req {
		if strings.TrimSpace(cfg[k]) == "" {
			return fmt.Errorf("缺少必填字段: %s", k)
		}
	}
	return nil
}

func (h *Handler) imSave(c *gin.Context) {
	provider := c.Param("provider")
	var in imSaveInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := validateIMConfig(provider, in.Config); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	cfgJSON, err := jsonMarshalString(in.Config)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SaveIMProviderConfig(c.Request.Context(), provider, cfgJSON, in.Enabled); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"saved": true})
}

// imVerify 连通性测试；body 带 config 时测新凭证，空 body 测已存凭证。
func (h *Handler) imVerify(c *gin.Context) {
	provider := c.Param("provider")
	var in imSaveInput
	cfgJSON := ""
	if err := c.ShouldBindJSON(&in); err == nil && in.Config != nil {
		b, _ := jsonMarshalString(in.Config)
		cfgJSON = b
	}
	if err := h.svc.VerifyProviderConfig(c.Request.Context(), provider, cfgJSON); err != nil {
		httpx.Fail(c, http.StatusBadGateway, 502, "连通性测试失败: "+err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

// --- token 有效期与 Redis 设置 ---

// token TTL 统一以秒为单位对外（UI 用数字输入，避免时长字符串格式坑）。
func (h *Handler) tokenTTLGet(c *gin.Context) {
	access, refresh := h.svc.TokenTTLs()
	httpx.OK(c, gin.H{
		"accessSeconds":  int64(access.Seconds()),
		"refreshSeconds": int64(refresh.Seconds()),
	})
}

type tokenTTLInput struct {
	AccessSeconds  int64 `json:"accessSeconds" binding:"required,min=1"`  // access 有效期（秒）
	RefreshSeconds int64 `json:"refreshSeconds" binding:"required,min=1"` // refresh 有效期（秒）
}

func (h *Handler) tokenTLTPut(c *gin.Context) {
	var in tokenTTLInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	access := time.Duration(in.AccessSeconds) * time.Second
	refresh := time.Duration(in.RefreshSeconds) * time.Second
	if err := h.svc.SetTokenTTLs(c.Request.Context(), access, refresh); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{
		"accessSeconds":  in.AccessSeconds,
		"refreshSeconds": in.RefreshSeconds,
	})
}

func (h *Handler) redisGet(c *gin.Context) {
	cfg := h.svc.RedisConfigCurrent()
	httpx.OK(c, gin.H{"addr": cfg.Addr, "db": cfg.DB, "configured": cfg.Addr != ""})
}

type redisInput struct {
	Addr     string `json:"addr" binding:"required"`
	Password string `json:"password"`
	DB       int    `json:"db"`
}

func (h *Handler) redisPut(c *gin.Context) {
	var in redisInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SaveRedisConfig(c.Request.Context(), RedisConfig{
		Addr: in.Addr, Password: in.Password, DB: in.DB,
	}); err != nil {
		httpx.Fail(c, http.StatusBadGateway, 502, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}
