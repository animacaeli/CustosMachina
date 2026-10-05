package projects

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/modules/rbac"
	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Name() string { return "projects" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/projects")
	{
		g.GET("", h.list)
		g.POST("", h.create)
		g.GET("/:id", h.get)
		g.PUT("/:id", h.update)
		g.DELETE("/:id", h.remove)
		g.PUT("/:id/targets", h.saveTargets)
	}
}

// requireScope P7-M1 项目级授权：scoped 用户访问范围外项目 → 403。
func requireScope(c *gin.Context, id uint) bool {
	if !rbac.InProjectScope(c, id) {
		httpx.Fail(c, http.StatusForbidden, 403, "该项目不在你的授权范围内")
		return false
	}
	return true
}

func (h *Handler) list(c *gin.Context) {
	out, err := h.svc.List(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	// scoped 用户过滤列表（全局角色不过滤）
	if all, ids := rbac.ProjectScope(c); !all {
		allowed := map[uint]struct{}{}
		for _, id := range ids {
			allowed[id] = struct{}{}
		}
		filtered := out[:0]
		for _, p := range out {
			if _, ok := allowed[p.ID]; ok {
				filtered = append(filtered, p)
			}
		}
		out = filtered
	}
	httpx.OK(c, out)
}

func (h *Handler) create(c *gin.Context) {
	var in SaveProjectInput
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

func (h *Handler) get(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if !requireScope(c, id) {
		return
	}
	p, targets, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		httpx.FailNotFound(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"project": p, "targets": targets})
}

func (h *Handler) update(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if !requireScope(c, id) {
		return
	}
	var in SaveProjectInput
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
	if !requireScope(c, id) {
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

func (h *Handler) saveTargets(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if !requireScope(c, id) {
		return
	}
	var in SaveTargetsInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SaveTargets(c.Request.Context(), id, in); err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}
