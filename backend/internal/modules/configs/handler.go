package configs

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/modules/rbac"
	"github.com/custos-machina/backend/internal/pkg/httpx"
	jwtpkg "github.com/custos-machina/backend/internal/pkg/jwt"
	"github.com/custos-machina/backend/internal/pkg/ratelimit"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
	// 公开拉取接口限速（P5 M1 纪律：新公开接口一律带限速）
	pullLimiter *ratelimit.Window
	// 60s 拉取短缓存（方案 B：不做推送/长连接）
	pullCache sync.Map // "app|env" -> pullCacheEntry
}

type pullCacheEntry struct {
	body    []byte
	version string
	format  string
	files   int
	at      time.Time
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, pullLimiter: ratelimit.NewWindow(60, time.Minute)}
}

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
		g.POST("/env-sync", h.envSync)
		// AgileConfig 式合并视图（只读预览，M5 同一合并语义）
		g.GET("/merged", h.mergedPreview)
	}
	// P6-M5 拉取凭证管理（admin，casbin v17）
	pt := r.Authed.Group("/config-pull-tokens")
	{
		pt.GET("", h.listPullTokens)
		pt.POST("", h.createPullToken)
		pt.PUT("/:id/enabled", h.setPullTokenEnabled)
	}
	// 公开拉取接口：服务启动调用（非用户凭证），应用 token 鉴权 + 限速
	pub := r.Public.Group("/config")
	pub.GET("/:app/:env", h.pullConfig)
	// P6-M7 配置中心（AgileConfig 纯后端通道）：同步/对账 + 连接设置
	// ——键值由 env/ini 文件下发自动派生，无独立 CRUD（文件是唯一编辑入口）
	kv := r.Authed.Group("/config-kv")
	{
		kv.POST("/sync", h.syncKV)
		kv.POST("/reconcile", h.reconcileKV)
		kv.GET("/provider-settings", h.kvSettings)
		kv.PUT("/provider-settings", h.saveKVSettings)
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
	// P7-M1：密钥明文查看是独立动作（与配置编辑分离），reveal 请求一律过 gate
	if reveal && !rbac.Can(c, "config.reveal") {
		httpx.Fail(c, http.StatusForbidden, 403, "密钥明文查看需独立权限（config.reveal）")
		return
	}
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
	// P7-M1：下发是与编辑分离的动作（POST /config-files/* 路由层不可分）
	if !rbac.Can(c, "config.deploy") {
		httpx.Fail(c, http.StatusForbidden, 403, "配置下发需独立权限（config.deploy）")
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
	// 路由是 /:id/versions/:vid——ParamID 只认 :id，版本号须显式取 :vid
	vid64, err := strconv.ParseUint(c.Param("vid"), 10, 64)
	if err != nil || vid64 == 0 {
		httpx.FailBadRequest(c, "无效的版本 id")
		return
	}
	vid := uint(vid64)
	reveal := c.Query("reveal") == "true"
	if reveal && !rbac.Can(c, "config.reveal") {
		httpx.Fail(c, http.StatusForbidden, 403, "密钥明文查看需独立权限（config.reveal）")
		return
	}
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
	// P7-M1：版本回退是编辑语义（PUT/POST 路由层不可分）
	if !rbac.Can(c, "config.edit") {
		httpx.Fail(c, http.StatusForbidden, 403, "配置回退需编辑权限（config.edit）")
		return
	}
	if err := h.svc.Rollback(c.Request.Context(), id, in.VersionID, actor(c)); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"rolled": true})
}

// envSync POST /config-files/env-sync 环境同步（admin/ops 写权限由 rbac 矩阵约束）。
func (h *Handler) envSync(c *gin.Context) {
	var in EnvSyncInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	// P7-M1：env 同步即下发语义
	if !rbac.Can(c, "config.deploy") {
		httpx.Fail(c, http.StatusForbidden, 403, "环境同步需下发权限（config.deploy）")
		return
	}
	created, updated, err := h.svc.EnvSync(c.Request.Context(), in, actor(c))
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"created": created, "updated": updated})
}

// ---- P6-M5 配置拉取（方案 B） ----

func (h *Handler) listPullTokens(c *gin.Context) {
	list, err := h.svc.ListPullTokens(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, list)
}

func (h *Handler) createPullToken(c *gin.Context) {
	var in struct {
		App  string `json:"app" binding:"required"`
		Envs string `json:"envs" binding:"required"`
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if in.Name == "" {
		in.Name = in.App
	}
	out, err := h.svc.IssuePullToken(c.Request.Context(), in.Name, in.App, in.Envs)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) setPullTokenEnabled(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		httpx.FailBadRequest(c, "id 无效")
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SetPullTokenEnabled(c.Request.Context(), uint(id), in.Enabled); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

// pullConfig GET /api/config/{app}/{env}：应用级聚合拉取。
// 鉴权 Bearer/X-Config-Token（应用级凭证）；ETag/304 + 60s 短缓存。
func (h *Handler) pullConfig(c *gin.Context) {
	if !h.pullLimiter.Allow(c.ClientIP()) {
		c.JSON(429, gin.H{"code": 429, "message": "请求过于频繁，请稍后再试"})
		return
	}
	app, env := c.Param("app"), c.Param("env")
	tok := strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
	if tok == "" {
		tok = c.GetHeader("X-Config-Token")
	}
	if tok == "" {
		c.JSON(401, gin.H{"code": 401, "message": "缺少拉取凭证（Authorization: Bearer 或 X-Config-Token）"})
		return
	}
	if _, err := h.svc.VerifyPullToken(c.Request.Context(), tok, app, env); err != nil {
		if strings.Contains(err.Error(), "不适用于") {
			c.JSON(403, gin.H{"code": 403, "message": err.Error()})
		} else {
			c.JSON(401, gin.H{"code": 401, "message": err.Error()})
		}
		return
	}

	// 60s 短缓存（鉴权在缓存前：吊销/越权不因缓存放过）
	var out *PullOut
	if e, ok := h.pullCache.Load(app + "|" + env); ok {
		entry := e.(pullCacheEntry)
		if time.Since(entry.at) < time.Minute {
			out = &PullOut{Body: entry.body, Version: entry.version, Format: entry.format, Files: entry.files}
		}
	}
	if out == nil {
		var err error
		out, err = h.svc.PullConfig(c.Request.Context(), app, env)
		if err != nil {
			c.JSON(404, gin.H{"code": 404, "message": err.Error()})
			return
		}
		h.pullCache.Store(app+"|"+env, pullCacheEntry{
			body: out.Body, version: out.Version, format: out.Format, files: out.Files, at: time.Now(),
		})
		// 惰性清扫：应用/环境被删后缓存键不再被读，避免只增不减
		h.pullCache.Range(func(k, v any) bool {
			if e, ok := v.(pullCacheEntry); ok && time.Since(e.at) > 10*time.Minute {
				h.pullCache.Delete(k)
			}
			return true
		})
	}

	etag := `"` + out.Version + `"`
	c.Header("X-Config-Version", out.Version)
	c.Header("ETag", etag)
	if c.Request.Header.Get("If-None-Match") == etag {
		c.Status(304)
		return
	}
	ct := "text/plain; charset=utf-8"
	switch out.Format {
	case "json":
		ct = "application/json; charset=utf-8"
	case FormatTOML:
		ct = "application/toml; charset=utf-8"
	}
	c.Header("Cache-Control", "public, max-age=60")
	c.Data(200, ct, out.Body)
}

// ---- P6-M7 K/V 配置（AgileConfig 共存）----

func (h *Handler) syncKV(c *gin.Context) {
	var in struct {
		ProjectID uint   `json:"projectId" binding:"required"`
		Env       string `json:"env" binding:"required,oneof=prod canary test"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	n, err := h.svc.SyncAgile(c.Request.Context(), in.ProjectID, in.Env, actor(c))
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, n)
}

func (h *Handler) reconcileKV(c *gin.Context) {
	var in struct {
		ProjectID uint   `json:"projectId" binding:"required"`
		Env       string `json:"env" binding:"required,oneof=prod canary test"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	diff, err := h.svc.ReconcileAgile(c.Request.Context(), in.ProjectID, in.Env, actor(c))
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, diff)
}

func (h *Handler) kvSettings(c *gin.Context) {
	httpx.OK(c, h.svc.KVSettings(c.Request.Context()))
}

func (h *Handler) saveKVSettings(c *gin.Context) {
	var in struct {
		Endpoint string `json:"endpoint"`
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.SaveKVSettings(c.Request.Context(), in.Endpoint, in.User, in.Password); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

// mergedPreview 合并视图：项目×环境聚合最终生效配置（JSON/YAML，只读预览）。
func (h *Handler) mergedPreview(c *gin.Context) {
	pid, _ := strconv.ParseUint(c.Query("project_id"), 10, 64)
	if pid == 0 {
		httpx.FailBadRequest(c, "project_id 必填")
		return
	}
	env := c.Query("env")
	if env == "" {
		env = "prod"
	}
	out, err := h.svc.MergedPreview(c.Request.Context(), uint(pid), env, c.Query("format"))
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}
