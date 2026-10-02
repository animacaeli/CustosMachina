package configs

import (
	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	jwtpkg "github.com/custos-machina/backend/internal/pkg/jwt"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Name() string { return "configs" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/config-files")
	{
		g.GET("", h.list)
		g.POST("", h.create)
		g.PUT("/:id", h.update)
		g.DELETE("/:id", h.remove)
		g.GET("/:id/content", h.getContent)
		g.PUT("/:id/content", h.putContent)
		g.POST("/:id/deploy", h.deploy)
		g.GET("/:id/versions", h.listVersions)
		g.GET("/:id/versions/:vid", h.getVersionContent)
		g.POST("/:id/rollback", h.rollback)
	}
}

// actor 当前登录人（审计留名）。
func actor(c *gin.Context) string {
	if cl := jwtpkg.ClaimsFromContext(c); cl != nil {
		return cl.DisplayName
	}
	return ""
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
	var in SaveFileInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Create(c.Request.Context(), in, actor(c))
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, toOut(*out))
}

func (h *Handler) update(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in SaveFileInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.Update(c.Request.Context(), id, in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"ok": true})
}

func (h *Handler) remove(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}

func (h *Handler) getContent(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	reveal := c.Query("reveal") == "true"
	content, f, err := h.svc.GetContent(c.Request.Context(), id, reveal, actor(c))
	if err != nil {
		httpx.FailNotFound(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"content": content, "masked": f.Sensitive && !reveal, "sensitive": f.Sensitive})
}

func (h *Handler) putContent(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in struct {
		Content string `json:"content" binding:"max=1048576"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SaveContent(c.Request.Context(), id, in.Content, actor(c)); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"saved": true})
}

func (h *Handler) deploy(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.Deploy(c.Request.Context(), id, actor(c)); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"deployed": true})
}

func (h *Handler) listVersions(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListVersions(c.Request.Context(), id)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) getVersionContent(c *gin.Context) {
	vid, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	reveal := c.Query("reveal") == "true"
	content, _, err := h.svc.GetVersionContent(c.Request.Context(), vid, reveal)
	if err != nil {
		httpx.FailNotFound(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"content": content})
}

func (h *Handler) rollback(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in struct {
		VersionID uint `json:"versionId" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.Rollback(c.Request.Context(), id, in.VersionID, actor(c)); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"rolled": true})
}
