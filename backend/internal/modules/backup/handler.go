package backup

import (
	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Name() string { return "backup" }

func (h *Handler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/backup-jobs")
	{
		g.GET("", h.list)
		g.POST("", h.create)
		g.PUT("/:id", h.update)
		g.DELETE("/:id", h.remove)
		g.POST("/:id/run", h.run)
		g.GET("/:id/runs", h.runs)
	}
}

func (h *Handler) list(c *gin.Context) {
	out, err := h.svc.ListJobs(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) create(c *gin.Context) {
	var in SaveJobInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.CreateJob(c.Request.Context(), in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, toJobOut(*out))
}

func (h *Handler) update(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in SaveJobInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.UpdateJob(c.Request.Context(), id, in)
	if err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, toJobOut(*out))
}

func (h *Handler) remove(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteJob(c.Request.Context(), id); err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}

func (h *Handler) run(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	run, err := h.svc.TriggerRun(c.Request.Context(), id)
	if err != nil && err != ErrNotFound {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err == ErrNotFound {
		httpx.FailNotFound(c, err.Error())
		return
	}
	httpx.OK(c, run)
}

func (h *Handler) runs(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListRuns(c.Request.Context(), id, 0)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, out)
}
