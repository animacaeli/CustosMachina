package cron

import (
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

func (h *Handler) Name() string { return "cron" }

func (h *Handler) RegisterRoutes(r server.Router) {
	// 写操作仅 admin：计划的安全约束（脚本创建/修改/触发均 admin），
	// casbin 种子只给 ops/dev GET；admin 由中间件全量放行
	g := r.Authed.Group("/cron-scripts")
	{
		g.GET("", h.listScripts)
		g.POST("", h.saveScript)
		g.PUT("/:id", h.saveScript)
		g.DELETE("/:id", h.deleteScript)
	}
	r.Authed.GET("/cron-jobs/preview", h.previewSchedule)
	j := r.Authed.Group("/cron-jobs")
	{
		j.GET("", h.listJobs)
		j.POST("", h.saveJob)
		j.PUT("/:id", h.saveJob)
		j.DELETE("/:id", h.deleteJob)
		j.POST("/:id/trigger", h.trigger)
	}
	r.Authed.GET("/cron-runs", h.listRuns)
	r.Authed.GET("/cron-runs/:id", h.getRun)
}

func (h *Handler) listScripts(c *gin.Context) {
	list, err := h.svc.ListScripts(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": list})
}

func (h *Handler) saveScript(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var in ScriptInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	sc, err := h.svc.SaveScript(c.Request.Context(), uint(id), in, operatorOf(c))
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, sc)
}

func (h *Handler) deleteScript(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteScript(c.Request.Context(), id); err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

// previewSchedule GET /cron-jobs/preview?schedule=0 3 * * *&count=5 表单预览未来触发时间。
func (h *Handler) previewSchedule(c *gin.Context) {
	expr := c.Query("schedule")
	if expr == "" {
		httpx.FailBadRequest(c, "schedule 必填")
		return
	}
	count := atoiDefault(c.Query("count"), 5)
	times, err := h.svc.SchedulePreview(expr, count)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"times": times})
}

func (h *Handler) listJobs(c *gin.Context) {
	list, err := h.svc.ListJobs(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": list})
}

func (h *Handler) saveJob(c *gin.Context) {
	id, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	var in JobInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	job, err := h.svc.SaveJob(c.Request.Context(), uint(id), in, operatorOf(c))
	if err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, job)
}

func (h *Handler) deleteJob(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteJob(c.Request.Context(), id); err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) trigger(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	// P7-M1：手动触发是独立动作（等于在目标机执行任意脚本），动作 gate 裁决
	// （替代原 IsAdmin 判断；admin/超管种子即含该动作，行为不变）
	if !rbac.Can(c, "cron.trigger") {
		httpx.Fail(c, http.StatusForbidden, 403, "手动执行需独立权限（cron.trigger）")
		return
	}
	run, err := h.svc.Trigger(c.Request.Context(), id, TriggerManual)
	if err != nil {
		if err == ErrNotFound {
			httpx.FailNotFound(c, err.Error())
			return
		}
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, run)
}

// getRun 单条运行记录（前端轮询实时日志用：running 状态时 output 为增量刷库值）。
func (h *Handler) getRun(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var run CronRun
	if err := h.svc.db.WithContext(c.Request.Context()).First(&run, id).Error; err != nil {
		httpx.FailNotFound(c, "运行记录不存在")
		return
	}
	httpx.OK(c, run)
}

func (h *Handler) listRuns(c *gin.Context) {
	q := struct {
		jobID uint
		page  int
		size  int
	}{page: atoiDefault(c.Query("page"), 1), size: atoiDefault(c.Query("size"), 20)}
	if v := c.Query("jobId"); v != "" {
		q.jobID = uint(atoiDefault(v, 0))
	}
	runs, total, err := h.svc.ListRuns(c.Request.Context(), q.jobID, q.page, q.size)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, gin.H{"items": runs, "total": total})
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
