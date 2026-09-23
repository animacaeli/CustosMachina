package resources

import (
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc       *Service
	collector *Collector
}

func NewHandler(svc *Service, collector *Collector) *Handler {
	return &Handler{svc: svc, collector: collector}
}

func (h *Handler) Name() string { return "resources" }

func (h *Handler) RegisterRoutes(r server.Router) {
	servers := r.Authed.Group("/servers")
	{
		servers.GET("", h.listServers)
		servers.POST("", h.createServer)
		servers.POST("/test-connection", h.testConnection) // 保存前即席测试（不落库）
		servers.GET("/:id", h.getServer)
		servers.PUT("/:id", h.updateServer)
		servers.DELETE("/:id", h.deleteServer)
		servers.POST("/:id/test", h.testServer)
		servers.GET("/:id/terminal", h.handleTerminal) // WebSocket；admin 专属（casbin 种子未授予其他角色）
	}
	// 独立前缀避免与 /servers/:id 通配冲突；只读，走内存环形缓冲优先
	metrics := r.Authed.Group("/server-metrics")
	{
		metrics.GET("/latest", h.latestMetrics)
		metrics.GET("/:id", h.serverMetrics)
	}
	// 批量容器占用：独立前缀避免与 /server-containers/:cid 静态段冲突
	cstats := r.Authed.Group("/server-container-stats")
	{
		cstats.GET("/:id", h.allContainerStats)
	}
	events := r.Authed.Group("/server-events")
	{
		events.GET("/:id", h.serverEvents)
	}
	// 容器管理 + 环境探测 + compose 部署（M4）
	containers := r.Authed.Group("/server-containers")
	{
		containers.GET("/:id", h.listContainers)
		containers.GET("/:id/stats-all", h.allContainerStats)   // 批量占用（一条 SSH 命令）
		containers.POST("/:id/:cid/:action", h.containerAction) // start | stop
		containers.GET("/:id/:cid/logs", h.containerLogs)
		containers.GET("/:id/:cid/stats", h.containerStats)
	}
	env := r.Authed.Group("/server-env")
	{
		env.GET("/:id", h.probeEnv)
	}
	envGuide := r.Authed.Group("/server-env-guide")
	{
		envGuide.GET("/:distro", h.installGuide)
	}
	compose := r.Authed.Group("/server-compose")
	{
		compose.POST("/:id", h.deployCompose)
		compose.GET("/:id/file", h.composeFile)
		compose.PUT("/:id/file", h.saveComposeFile)
		compose.POST("/:id/recreate", h.recreateCompose)
	}
	groups := r.Authed.Group("/server-groups")
	{
		groups.GET("", h.listGroups)
		groups.POST("", h.createGroup)
		groups.PUT("/:id", h.updateGroup)
		groups.DELETE("/:id", h.deleteGroup)
	}
}

func (h *Handler) latestMetrics(c *gin.Context) {
	httpx.OK(c, h.collector.LatestFor())
}

func (h *Handler) serverMetrics(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	hours := 1
	if v, err := strconv.Atoi(c.Query("hours")); err == nil && v > 0 && v <= 168 {
		hours = v
	}
	since := time.Now().Add(-time.Duration(hours) * time.Hour)
	list, err := h.collector.QueryFor(c.Request.Context(), id, since)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, list)
}

func (h *Handler) serverEvents(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	events, err := h.svc.EventsFor(c.Request.Context(), id)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, events)
}

func idParam(c *gin.Context) (uint, bool) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id == 0 {
		httpx.FailBadRequest(c, "非法的 id")
		return 0, false
	}
	return uint(id), true
}

func (h *Handler) listServers(c *gin.Context) {
	list, err := h.svc.ListServers(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, list)
}

func (h *Handler) createServer(c *gin.Context) {
	var in CreateServerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.CreateServer(c.Request.Context(), &in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) getServer(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	srv, err := h.svc.servers.GetByID(c.Request.Context(), id)
	if err != nil {
		httpx.FailNotFound(c, "服务器不存在")
		return
	}
	httpx.OK(c, toOut(srv))
}

func (h *Handler) updateServer(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in UpdateServerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.UpdateServer(c.Request.Context(), id, &in)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			httpx.FailNotFound(c, "服务器不存在")
			return
		}
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) deleteServer(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteServer(c.Request.Context(), id); err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) testServer(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	ok2, msg := h.svc.TestServer(c.Request.Context(), id)
	if !ok2 {
		httpx.FailUpstream(c, msg)
		return
	}
	httpx.OK(c, gin.H{"message": msg})
}

// testConnection 表单"测试连通"按钮：用填写的凭据即席连一次，不落库不回存。
func (h *Handler) testConnection(c *gin.Context) {
	var in CreateServerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	cred := &credential{
		Username: in.Username, Password: in.Password,
		PrivateKey: in.PrivateKey, Passphrase: in.Passphrase,
	}
	ms, err := TryConnectivity(in.Host, in.Port, cred)
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	httpx.OK(c, gin.H{"message": fmt.Sprintf("连接成功（%s）", ms)})
}

func (h *Handler) listGroups(c *gin.Context) {
	list, err := h.svc.ListGroups(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, list)
}

func (h *Handler) createGroup(c *gin.Context) {
	var in GroupInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	g, err := h.svc.CreateGroup(c.Request.Context(), &in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, g)
}

func (h *Handler) updateGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in GroupInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	g, err := h.svc.UpdateGroup(c.Request.Context(), id, &in)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			httpx.FailNotFound(c, "分组不存在")
			return
		}
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, g)
}

func (h *Handler) deleteGroup(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteGroup(c.Request.Context(), id); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}
