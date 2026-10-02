package notify

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Name() string { return "notify" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/notify-groups")
	{
		g.GET("", h.list)
		g.POST("", h.create)
		g.PUT("/:id", h.update)
		g.DELETE("/:id", h.remove)
		g.POST("/:id/test", h.test) // 发一条测试消息验证 webhook
	}
	s := r.Authed.Group("/notify-settings")
	{
		s.GET("/ops-group", h.getOpsGroup)
		s.PUT("/ops-group", h.putOpsGroup)
	}
	ru := r.Authed.Group("/notify-rules")
	{
		ru.GET("", h.listRules)
		ru.POST("", h.createRule)
		ru.PUT("/:id", h.updateRule)
		ru.DELETE("/:id", h.deleteRule)
		ru.POST("/:id/test", h.testRule)
	}
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
