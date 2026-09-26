package release

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

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
	list, total, err := h.svc.List(c.Request.Context(), q.projectID, q.env, q.page, q.size)
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

func (h *Handler) execute(c *gin.Context) {
	var in ReleaseInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	// 正式环境发布高危：仅管理员（灰度允许 ops，与 casbin 层互补）
	if in.EnvType == "prod" {
		if claims := jwt.ClaimsFromContext(c); claims == nil || !claims.IsAdmin {
			httpx.Fail(c, http.StatusForbidden, 403, "正式环境发布仅管理员可操作")
			return
		}
	}
	rel, err := h.svc.Execute(c.Request.Context(), in, operatorOf(c))
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
