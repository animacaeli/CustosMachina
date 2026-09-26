package ci

import (
	"io"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Name() string { return "ci" }

func (h *Handler) RegisterRoutes(r server.Router) {
	// webhook 回调：gitea 服务器调用，不能走 JWT；靠 HMAC 签名鉴权
	r.Public.POST("/ci/webhook/gitea", h.webhookGitea)

	g := r.Authed.Group("/ci")
	{
		g.GET("/global", h.getGlobal)
		g.PUT("/global", h.putGlobal)
	}
	regs := r.Authed.Group("/registries")
	{
		regs.GET("", h.listRegistries)
		regs.POST("", h.createRegistry)
		regs.PUT("/:id", h.updateRegistry)
		regs.DELETE("/:id", h.deleteRegistry)
	}
	builds := r.Authed.Group("/builds")
	{
		builds.GET("", h.listBuilds)
		builds.GET("/:id/log", h.buildLog)
	}
	branches := r.Authed.Group("/project-branches")
	{
		branches.GET("/:id", h.branches)
	}
}

func (h *Handler) webhookGitea(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20))
	if err != nil {
		httpx.FailBadRequest(c, "读取 body 失败")
		return
	}
	if err := h.svc.VerifySignature(c.Request.Context(), body, c.GetHeader("X-Gitea-Signature")); err != nil {
		httpx.FailUnauthorized(c, err.Error())
		return
	}
	b, err := h.svc.HandleTagPush(c.Request.Context(), body)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"buildId": optionalID(b)})
}

func optionalID(b *Build) uint {
	if b == nil {
		return 0
	}
	return b.ID
}

func (h *Handler) getGlobal(c *gin.Context) {
	out, err := h.svc.GetGlobal(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) putGlobal(c *gin.Context) {
	var in SaveGlobalInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.SaveGlobal(c.Request.Context(), in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) listRegistries(c *gin.Context) {
	out, err := h.svc.ListRegistries(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) createRegistry(c *gin.Context) {
	var in SaveRegistryInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.SaveRegistry(c.Request.Context(), 0, in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) updateRegistry(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in SaveRegistryInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.SaveRegistry(c.Request.Context(), id, in)
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

func (h *Handler) deleteRegistry(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteRegistry(c.Request.Context(), id); err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) listBuilds(c *gin.Context) {
	q := BuildQuery{
		EnvType: c.Query("env"),
		Page:    atoiDefault(c.Query("page"), 1),
		Size:    atoiDefault(c.Query("size"), 20),
	}
	if v := c.Query("projectId"); v != "" {
		q.ProjectID = uint(atoiDefault(v, 0))
	}
	list, total, err := h.svc.ListBuilds(c.Request.Context(), q)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": list, "total": total})
}

func (h *Handler) buildLog(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	logs, err := h.svc.BuildLog(c.Request.Context(), id)
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, logs)
}

func (h *Handler) branches(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	names, err := h.svc.Branches(c.Request.Context(), id)
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, names)
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
