package observ

import (
	"time"

	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/modules/rbac"
	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/pkg/jwt"
	"github.com/custos-machina/backend/internal/pkg/ratelimit"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
	// 角色解析（项目侧模板化路径对 dev 强制在 handler 层做结构约束）
	users identity.UserRepository
	// O2 告警回流限速（公开接口防刷）
	alertWebhookLimiter *ratelimit.Window
}

func NewHandler(svc *Service, users identity.UserRepository) *Handler {
	return &Handler{svc: svc, users: users, alertWebhookLimiter: ratelimit.NewWindow(60, time.Minute)}
}

// canManagePlatform 平台级（手写 SQL）告警管理资格：本地超管或 ops 角色。
func (h *Handler) canManagePlatform(c *gin.Context) bool {
	claims := jwt.ClaimsFromContext(c)
	if claims == nil {
		return false
	}
	if claims.IsAdmin {
		return true
	}
	u, err := h.users.GetByID(c.Request.Context(), claims.UserID)
	if err != nil {
		return false
	}
	for _, r := range identity.ParseRoleList(u.Roles) {
		if r == "ops" {
			return true
		}
	}
	return false
}

func (h *Handler) Name() string { return "observ" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/observ")
	{
		g.GET("", h.list)
		g.PUT("/o2-url", h.setO2URL)
		g.GET("/status", h.status)
		g.POST("/deploy", h.deploy)
		g.POST("/uninstall", h.uninstall)
		// 告警闭环（P5 M3）
		g.GET("/o2-settings", h.getO2Settings)
		g.PUT("/o2-settings", h.putO2Settings)
		g.GET("/alerts", h.listAlerts)
		g.POST("/alerts", h.createAlert)
		g.PUT("/alerts/:id", h.updateAlert)
		g.DELETE("/alerts/:id", h.deleteAlert)
		g.POST("/alerts/sync", h.syncAllAlerts)
		// R1 模板化：管理员模板 CRUD；项目侧（含 dev）经 render 预览 + from-template 实例化
		g.GET("/alert-templates", h.listTemplates)
		g.POST("/alert-templates", h.createTemplate)
		g.PUT("/alert-templates/:id", h.updateTemplate)
		g.DELETE("/alert-templates/:id", h.deleteTemplate)
		g.POST("/alert-templates/render", h.renderTemplate)
		g.POST("/alerts/from-template", h.upsertAlertFromTemplate)
		// 告警事件留痕（v0.12.0 审计产品项：历史回溯 + 手动标记已处理）
		g.GET("/alert-events", h.listAlertEvents)
		g.POST("/alert-events/:id/handle", h.handleAlertEvent)
	}
	// O2 告警回流（公开；X-Custos-Token 校验 + 限速）
	r.Public.POST("/observ/alerts/webhook", h.alertWebhookLimiter.Middleware(), h.o2AlertWebhook)
}

func (h *Handler) list(c *gin.Context) {
	res, err := h.svc.List(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) setO2URL(c *gin.Context) {
	var in struct {
		// P8-M2：允许空串（清空 O2 地址，观测组件部署前的回退）；合法性由 SetO2URL 校验
		URL string `json:"url" binding:"omitempty,max=1024"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SetO2URL(c.Request.Context(), in.URL); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) status(c *gin.Context) {
	serverID := uint(atoiDefault(c.Query("serverId"), 0))
	if serverID == 0 {
		httpx.FailBadRequest(c, "serverId 必填")
		return
	}
	res, err := h.svc.Status(c.Request.Context(), serverID)
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, res)
}

func (h *Handler) deploy(c *gin.Context) {
	var in DeployInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Deploy(c.Request.Context(), in)
	if err != nil {
		httpx.FailUpstream(c, err.Error()+"\n"+out)
		return
	}
	httpx.OK(c, gin.H{"output": out})
}

func (h *Handler) uninstall(c *gin.Context) {
	var in struct {
		ServerID  uint   `json:"serverId" binding:"required"`
		Component string `json:"component" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Uninstall(c.Request.Context(), in.ServerID, in.Component)
	if err != nil {
		httpx.FailUpstream(c, err.Error()+"\n"+out)
		return
	}
	httpx.OK(c, gin.H{"output": out})
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// ---- O2 告警闭环路由（P5 M3）----

func (h *Handler) getO2Settings(c *gin.Context) {
	httpx.OK(c, h.svc.O2Settings(c.Request.Context()))
}

func (h *Handler) putO2Settings(c *gin.Context) {
	var in O2SettingsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SaveO2Settings(c.Request.Context(), in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, h.svc.O2Settings(c.Request.Context()))
}

func (h *Handler) listAlerts(c *gin.Context) {
	if !h.canManagePlatform(c) { // dev：仅项目侧策略，且限定授权项目
		all, ids := rbac.ProjectScope(c)
		var out []Alert
		var err error
		if all {
			out, err = h.svc.listProjectAlerts(c.Request.Context(), nil)
		} else {
			out, err = h.svc.listProjectAlerts(c.Request.Context(), ids)
		}
		if err != nil {
			httpx.FailServer(c, err)
			return
		}
		httpx.OK(c, out)
		return
	}
	out, err := h.svc.ListAlerts(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) createAlert(c *gin.Context) {
	var in SaveAlertInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if !h.canManagePlatform(c) {
		httpx.Fail(c, 403, 403, "平台级告警（手写 SQL）仅管理员/运维可建，项目侧请用告警模板")
		return
	}
	out, err := h.svc.CreateAlert(c.Request.Context(), in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) updateAlert(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in SaveAlertInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if !h.canManagePlatform(c) {
		httpx.Fail(c, 403, 403, "平台级告警仅管理员/运维可改，项目侧请用告警模板")
		return
	}
	if _, err := h.svc.UpdateAlert(c.Request.Context(), id, in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

// listAlertEvents 告警历史（admin/ops 全量；dev 只读——与 alerts 列表同可见面）。
func (h *Handler) listAlertEvents(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	out, err := h.svc.ListAlertEvents(c.Request.Context(),
		c.Query("level"), c.Query("status"), page, size)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

// handleAlertEvent 标记已处理（admin/ops；dev 不可）。
func (h *Handler) handleAlertEvent(c *gin.Context) {
	if !h.canManagePlatform(c) {
		httpx.Fail(c, 403, 403, "仅管理员/运维可处理告警事件")
		return
	}
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	claims := jwt.ClaimsFromContext(c)
	by := ""
	if claims != nil {
		by = claims.DisplayName
	}
	if err := h.svc.HandleAlertEvent(c.Request.Context(), id, by); err != nil {
		httpx.FailNotFound(c, "事件不存在或已处理")
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

func (h *Handler) deleteAlert(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if !h.canManagePlatform(c) {
		a, err := h.svc.getAlert(c.Request.Context(), id)
		if err != nil || a.ProjectID == 0 {
			httpx.Fail(c, 403, 403, "平台级告警仅管理员/运维可删")
			return
		}
		// 项目侧告警还须在本人授权项目范围内（v0.12.0 审计中等项：
		// dev 曾可删任意项目的告警）
		if !rbac.InProjectScope(c, a.ProjectID) {
			httpx.Fail(c, 403, 403, "该项目不在你的授权范围内")
			return
		}
	}
	if err := h.svc.DeleteAlert(c.Request.Context(), id); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}

func (h *Handler) syncAllAlerts(c *gin.Context) {
	if !h.canManagePlatform(c) {
		httpx.Fail(c, 403, 403, "全量同步仅管理员/运维可用")
		return
	}
	n, err := h.svc.SyncAllAlerts(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"synced": n})
}
