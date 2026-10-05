package release

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/modules/rbac"
	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/pkg/jwt"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Name() string { return "release" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/releases")
	{
		g.GET("", h.list)
		g.GET("/passed-tags", h.passedTags)
		g.GET("/active-color", h.activeColor)
		g.GET("/:id", h.detail)
		g.POST("", h.execute)
		g.POST("/:id/rollback", h.rollback)
	}
}

func (h *Handler) list(c *gin.Context) {
	q := struct {
		projectID uint
		env       string
		page      int
		size      int
	}{
		env:  c.Query("env"),
		page: atoiDefault(c.Query("page"), 1),
		size: atoiDefault(c.Query("size"), 20),
	}
	if v := c.Query("projectId"); v != "" {
		q.projectID = uint(atoiDefault(v, 0))
	}
	// P7-M1 项目范围：scoped 用户只见授权项目（nil = 全局不限）
	var scopeIDs []uint
	if all, ids := rbac.ProjectScope(c); !all {
		scopeIDs = ids
		if len(scopeIDs) == 0 {
			scopeIDs = []uint{0} // 无任何授权项目：恒空集
		}
	}
	list, total, err := h.svc.List(c.Request.Context(), q.projectID, q.env, q.page, q.size, scopeIDs)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": list, "total": total})
}

func (h *Handler) passedTags(c *gin.Context) {
	projectID := uint(atoiDefault(c.Query("projectId"), 0))
	env := c.Query("env")
	if projectID == 0 || env == "" {
		httpx.FailBadRequest(c, "projectId 与 env 必填")
		return
	}
	tags, err := h.svc.PassedTags(c.Request.Context(), projectID, env)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, tags)
}

// detail 单条发布记录（蓝绿异步执行时前端轮询进度：status running + output 阶段日志）。
func (h *Handler) detail(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var rel Release
	if err := h.svc.DB().WithContext(c.Request.Context()).First(&rel, id).Error; err != nil {
		httpx.FailNotFound(c, ErrNotFound.Error())
		return
	}
	httpx.OK(c, rel)
}

func (h *Handler) activeColor(c *gin.Context) {
	projectID := uint(atoiDefault(c.Query("projectId"), 0))
	if projectID == 0 {
		httpx.FailBadRequest(c, "projectId 必填")
		return
	}
	httpx.OK(c, gin.H{"color": h.svc.ActiveColor(c.Request.Context(), projectID)})
}

func (h *Handler) execute(c *gin.Context) {
	var in ReleaseInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	// P7-M1 环境隔离：发布权限按环境分派（release.publish.test/canary/prod），
	// 环境在 body 里、路由层不可表达——handler gate 裁决
	envAction := map[string]string{
		"test": "release.publish.test", "canary": "release.publish.canary", "prod": "release.publish.prod",
	}[in.EnvType]
	if envAction == "" {
		httpx.FailBadRequest(c, "envType 须为 test/canary/prod")
		return
	}
	if !rbac.Can(c, envAction) {
		httpx.Fail(c, http.StatusForbidden, 403, "发布到 "+in.EnvType+" 环境需要对应权限（"+envAction+"）")
		return
	}
	// 发布是长链路（门禁最长 3 分钟 + drain 30s）：脱离 request ctx，
	// 客户端断开不产生"conf 已切但记录未落"的半完成状态
	rel, err := h.svc.Execute(context.WithoutCancel(c.Request.Context()), in, operatorOf(c))
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, rel)
}

func (h *Handler) rollback(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	// P7-M1：回滚是独立发布动作（与 POST /releases 同路由面，gate 分派）
	if !rbac.Can(c, "release.rollback") {
		httpx.Fail(c, http.StatusForbidden, 403, "回滚发布需独立权限（release.rollback）")
		return
	}
	rel, err := h.svc.Rollback(c.Request.Context(), id, operatorOf(c))
	if err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, rel)
}

func operatorOf(c *gin.Context) string {
	if claims := jwt.ClaimsFromContext(c); claims != nil && claims.DisplayName != "" {
		return claims.DisplayName
	}
	return "unknown"
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
