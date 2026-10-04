// chat-handler.go P6-M1 对话路由：CRUD + SSE 流式。
// SSE 事件：delta（增量文本）/ done（终态，含 messageId 与 status）/
// error（上游或平台错误）/ ping（15s 心跳，前端忽略）。治理设计见
// docs/design-chat-sse.md §4：单一发送 goroutine，退出路径穷举。
package ai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/pkg/jwt"
	"github.com/custos-machina/backend/internal/server"
)

type ChatHandler struct {
	svc *ChatService
}

func NewChatHandler(svc *ChatService) *ChatHandler { return &ChatHandler{svc: svc} }

func (h *ChatHandler) Name() string { return "ai-chat" }

func (h *ChatHandler) RegisterRoutes(r server.Router) {
	g := r.Authed.Group("/ai/chat")
	{
		g.GET("/conversations", h.listConvs)
		g.POST("/conversations", h.createConv)
		g.DELETE("/conversations/:id", h.deleteConv)
		g.PUT("/conversations/:id/mount", h.updateMount)
		g.GET("/conversations/:id/messages", h.messages)
		g.POST("/conversations/:id/messages", h.chat) // SSE 流式回复
	}
	// 技能：GET 全角色（命令面板列自己可用的）；写操作 admin（casbin v16）
	sk := r.Authed.Group("/ai/skills")
	{
		sk.GET("", h.listSkills)
		sk.POST("", h.createSkill)
		sk.PUT("/:id", h.updateSkill)
		sk.DELETE("/:id", h.deleteSkill)
	}
}

func (h *ChatHandler) listSkills(c *gin.Context) {
	var roles []string
	if c.Query("admin") != "1" || !viewerAdmin(c) {
		roles = viewerRoles(c) // 普通请求只看自己可用的
	}
	list, err := h.svc.Skills.List(c.Request.Context(), roles)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, list)
}

func (h *ChatHandler) createSkill(c *gin.Context) {
	var in SaveSkillInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Skills.Save(c.Request.Context(), 0, in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *ChatHandler) updateSkill(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in SaveSkillInput
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.Skills.Save(c.Request.Context(), id, in)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *ChatHandler) deleteSkill(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.Skills.Delete(c.Request.Context(), id); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

// viewerRoles 会话视角：admin 布尔映射三级角色语义（superadmin/admin 同归
// privileged；普通用户按 dev 过滤——Sensitive 块不可见）。
func viewerRoles(c *gin.Context) []string {
	if claims := jwt.ClaimsFromContext(c); claims != nil && claims.IsAdmin {
		return []string{"admin"}
	}
	return []string{"dev"}
}

// viewerAdmin 当前用户是否管理员（会话跨用户查看/管理）。
func viewerAdmin(c *gin.Context) bool {
	claims := jwt.ClaimsFromContext(c)
	return claims != nil && claims.IsAdmin
}

func (h *ChatHandler) listConvs(c *gin.Context) {
	claims := jwt.ClaimsFromContext(c)
	// user_id：admin 查看指定用户的对话历史（缺省=当前用户；普通用户该参数被忽略）
	// deleted=1：admin 含已软删会话（留档视图；普通用户忽略）
	filter := claims.UserID
	if v, err := strconv.ParseUint(c.Query("user_id"), 10, 64); err == nil && v > 0 && viewerAdmin(c) {
		filter = uint(v)
	}
	includeDeleted := c.Query("deleted") == "1" && viewerAdmin(c)
	list, err := h.svc.ListConversations(c.Request.Context(), claims.UserID, viewerAdmin(c), filter, includeDeleted)
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	httpx.OK(c, list)
}

func (h *ChatHandler) createConv(c *gin.Context) {
	claims := jwt.ClaimsFromContext(c)
	var in struct {
		Mode  string `json:"mode" binding:"omitempty,oneof=general platform"`
		Mount *Mount `json:"mount"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.CreateConversation(c.Request.Context(), claims.UserID, in.Mode, in.Mount)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *ChatHandler) deleteConv(c *gin.Context) {
	claims := jwt.ClaimsFromContext(c)
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.DeleteConversation(c.Request.Context(), claims.UserID, id, viewerAdmin(c)); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

func (h *ChatHandler) updateMount(c *gin.Context) {
	claims := jwt.ClaimsFromContext(c)
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in struct {
		Mount *Mount `json:"mount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	if err := h.svc.UpdateMount(c.Request.Context(), claims.UserID, id, in.Mount); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

func (h *ChatHandler) messages(c *gin.Context) {
	claims := jwt.ClaimsFromContext(c)
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	list, err := h.svc.Messages(c.Request.Context(), claims.UserID, id, viewerAdmin(c))
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, list)
}

type chatEvent struct {
	name string
	data gin.H
}

// chat SSE 流式对话。错误一律以 error 事件发出（此时响应头已写，无法改状态码）；
// 连接层错误（鉴权前的 401/404）不在此列。
func (h *ChatHandler) chat(c *gin.Context) {
	claims := jwt.ClaimsFromContext(c)
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	var in struct {
		Content     string           `json:"content" binding:"required,max=32000"`
		Skill       string           `json:"skill" binding:"omitempty,max=64"`
		Attachments []ChatAttachment `json:"attachments" binding:"omitempty,max=3,dive"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	// 附件限制：单文件 4MB（base64 后约 5.5M 字符）；超限拒绝整条消息
	for _, a := range in.Attachments {
		if len(a.Data) > (4<<20)/3*4 {
			httpx.FailBadRequest(c, fmt.Sprintf("附件 %s 超过 4MB 限制", a.Name))
			return
		}
	}

	// SSE 头：X-Accel-Buffering 让 nginx 直发不缓冲（否则整流攒完才到前端）
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")

	reqCtx := c.Request.Context()
	ch := make(chan chatEvent, 64)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// 生产者：CompleteStream 的 onDelta/onTool 投递；断连时靠 reqCtx 解除阻塞
		msg, err := h.svc.ChatStream(reqCtx, claims.UserID, id, in.Content, in.Skill, in.Attachments, viewerRoles(c), func(delta string) {
			select {
			case ch <- chatEvent{"delta", gin.H{"text": delta}}:
			case <-reqCtx.Done():
			}
		}, func(name, args string) {
			select {
			case ch <- chatEvent{"tool", gin.H{"name": name, "args": args}}:
			case <-reqCtx.Done():
			}
		}, func(a ActionTrace) {
			select {
			case ch <- chatEvent{"action", gin.H{
				"type": a.Type, "summary": a.Summary, "route": a.Route,
			}}:
			case <-reqCtx.Done():
			}
		})
		final := chatEvent{"done", gin.H{"status": "done"}}
		if msg != nil {
			final.data["messageId"] = msg.ID
			final.data["status"] = msg.Status
		}
		if err != nil && !errors.Is(err, context.Canceled) {
			final = chatEvent{"error", gin.H{"message": err.Error()}}
		}
		select {
		case ch <- final:
		case <-reqCtx.Done(): // 消费者已断开，丢弃终态事件（落库已在 ChatStream 内完成）
		default:
		}
		close(ch)
	}()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	c.Stream(func(_ io.Writer) bool {
		select {
		case <-reqCtx.Done():
			return false
		case <-heartbeat.C:
			c.SSEvent("ping", "")
			return true
		case e, ok := <-ch:
			if !ok {
				return false
			}
			c.SSEvent(e.name, e.data)
			return true
		}
	})
	// 等 producer 退出：断连路径下它经 ctx cancel 停上游、落库部分回复后返回
	wg.Wait()
}
