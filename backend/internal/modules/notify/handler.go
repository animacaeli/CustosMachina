package notify

import (
	"strconv"

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
	id, ok := idParam(c)
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
	id, ok := idParam(c)
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
	id, ok := idParam(c)
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

func idParam(c *gin.Context) (uint, bool) {
	id64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || id64 == 0 {
		httpx.FailBadRequest(c, "无效的 id")
		return 0, false
	}
	return uint(id64), true
}
