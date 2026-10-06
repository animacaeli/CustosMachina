package notify

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/pkg/ratelimit"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
	// 业务告警入口限速（P7-M5 纪律：新公开接口一律带限速）
	bizLimiter *ratelimit.Window
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, bizLimiter: ratelimit.NewWindow(60, time.Minute)}
}

func (h *Handler) Name() string { return "notify" }

// listRecords GET /notify/records?groupId=&page=&size=（admin，种子 v24）。
func (h *Handler) listRecords(c *gin.Context) {
	groupID, _ := strconv.ParseUint(c.Query("groupId"), 10, 64)
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	out, err := h.svc.ListSendRecords(c.Request.Context(), uint(groupID), page, size)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/notify-groups")
	{
		g.GET("", h.list)
		g.POST("", h.create)
		g.PUT("/:id", h.update)
		g.DELETE("/:id", h.remove)
		g.POST("/:id/test", h.test) // 发一条测试消息验证 webhook
	}
	// 通知投递记录（v0.12.0 审计：notify_records 此前只写不读，排障只能连库）
	r.Authed.GET("/notify/records", h.listRecords)
	s := r.Authed.Group("/notify-settings")
	{
		s.GET("/ops-group", h.getOpsGroup)
		s.PUT("/ops-group", h.putOpsGroup)
		// P6-M9 渠道凭据（Telegram Bot / SMTP）
		s.GET("/channels", h.getChannelSettings)
		s.PUT("/channels", h.putChannelSettings)
	}
	ru := r.Authed.Group("/notify-rules")
	{
		ru.GET("", h.listRules)
		ru.POST("", h.createRule)
		ru.PUT("/:id", h.updateRule)
		ru.DELETE("/:id", h.deleteRule)
		ru.POST("/:id/test", h.testRule)
	}
	// P7-M5 业务告警凭证管理（admin，casbin v21）
	bt := r.Authed.Group("/notify-business-tokens")
	{
		bt.GET("", h.listBusinessTokens)
		bt.POST("", h.createBusinessToken)
		bt.PUT("/:id/enabled", h.setBusinessTokenEnabled)
	}
	// P7-M5 业务告警公开入口：业务服务调用（非用户凭证），
	// 应用级 token 鉴权 + IP 限速，投递复用统一通知路由
	r.Public.POST("/notify/business", h.businessAlert)
}

// ---- P7-M5 业务告警 ----

func (h *Handler) businessAlert(c *gin.Context) {
	if !h.bizLimiter.Allow(c.ClientIP()) {
		c.JSON(429, gin.H{"code": 429, "message": "请求过于频繁，请稍后重试"})
		return
	}
	tok := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if tok == "" {
		tok = c.GetHeader("X-Biz-Alert-Token")
	}
	if tok == "" {
		c.JSON(401, gin.H{"code": 401, "message": "缺少业务告警凭证（Authorization: Bearer 或 X-Biz-Alert-Token）"})
		return
	}
	var in BusinessAlertInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(400, gin.H{"code": 400, "message": err.Error()})
		return
	}
	if _, err := h.svc.VerifyBusinessToken(c.Request.Context(), tok, in.App); err != nil {
		c.JSON(401, gin.H{"code": 401, "message": err.Error()})
		return
	}
	title, detail, dedupKey := RenderBusinessAlert(in)
	// 投递异步化：webhook 投递可达秒级（企微 API 慢），同步会把业务侧响应拖到
	// 3s+——入口受理即返回，脱离 request ctx 投递（只发消息，无半完成态）
	go h.svc.NotifyEvent(context.WithoutCancel(c.Request.Context()), SourceBusiness, in.Level, dedupKey, title, detail)
	httpx.OK(c, gin.H{"accepted": true})
}

func (h *Handler) listBusinessTokens(c *gin.Context) {
	out, err := h.svc.ListBusinessTokens(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) createBusinessToken(c *gin.Context) {
	var in struct {
		Name string `json:"name" binding:"max=64"`
		App  string `json:"app" binding:"required,max=64"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.IssueBusinessToken(c.Request.Context(), in.Name, in.App)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) setBusinessTokenEnabled(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SetBusinessTokenEnabled(c.Request.Context(), id, in.Enabled); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

// ---- 路由规则（统一通知路由，P5 M1）----

func (h *Handler) listRules(c *gin.Context) {
	out, err := h.svc.ListRules(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) createRule(c *gin.Context) {
	var in SaveRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.CreateRule(c.Request.Context(), in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) updateRule(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in SaveRuleInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.UpdateRule(c.Request.Context(), id, in)
	if err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) deleteRule(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteRule(c.Request.Context(), id); err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}

// testRule 用规则的 source/级别发一条测试事件，走完整路由管线（含静默/聚合）。
func (h *Handler) testRule(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	rule, err := h.svc.getRule(c.Request.Context(), id)
	if err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailServer(c, err)
		return
	}
	h.svc.NotifyEvent(c.Request.Context(), rule.Source, rule.MinLevel, "rule-test",
		"【测试】"+rule.Name, fmt.Sprintf("路由规则测试事件（source=%s level=%s）。若配置了聚合窗口，请等待窗口到期后查看群消息。", rule.Source, rule.MinLevel))
	httpx.OK(c, gin.H{"ok": true})
}

func (h *Handler) list(c *gin.Context) {
	out, err := h.svc.List(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) create(c *gin.Context) {
	var in SaveGroupInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Create(c.Request.Context(), in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) update(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in SaveGroupInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Update(c.Request.Context(), id, in)
	if err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) remove(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) test(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	g, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.FailNotFound(c, err.Error())
		return
	}
	if err := h.svc.Send(c.Request.Context(), g, "CustosMachina 测试消息", "通知群配置验证成功。"); err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) getOpsGroup(c *gin.Context) {
	id, ok := h.svc.OpsGroupID(c.Request.Context())
	httpx.OK(c, gin.H{"groupId": id, "configured": ok})
}

type opsGroupInput struct {
	GroupID uint `json:"groupId" binding:"required"`
}

func (h *Handler) putOpsGroup(c *gin.Context) {
	var in opsGroupInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if _, err := h.svc.Get(c.Request.Context(), in.GroupID); err != nil {
		httpx.FailBadRequest(c, "通知群不存在")
		return
	}
	if err := h.svc.SetOpsGroup(c.Request.Context(), in.GroupID); err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, nil)
}

// ---- P6-M9 渠道凭据设置（Telegram Bot / SMTP）----

func (h *Handler) getChannelSettings(c *gin.Context) {
	httpx.OK(c, h.svc.ChannelSettings(c.Request.Context()))
}

func (h *Handler) putChannelSettings(c *gin.Context) {
	var in struct {
		SMTPHost string `json:"smtpHost"`
		SMTPPort string `json:"smtpPort"`
		SMTPUser string `json:"smtpUser"`
		SMTPPass string `json:"smtpPass"`
		SMTPFrom string `json:"smtpFrom"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SaveChannelSettings(c.Request.Context(),
		in.SMTPHost, in.SMTPPort, in.SMTPUser, in.SMTPPass, in.SMTPFrom); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}
