package mcp

import (
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	mcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/pkg/ratelimit"
	"github.com/custos-machina/backend/internal/server"
)

type Handler struct {
	svc *Service
	// MCP 端点公开（凭证鉴权）+ 限速（防凭证泄露后被刷爆）
	mcpLimiter *ratelimit.Window
	once       sync.Once
	mcpHTTP    *mcp.StreamableHTTPHandler
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc, mcpLimiter: ratelimit.NewWindow(600, time.Minute)}
}

func (h *Handler) Name() string { return "mcp" }

func (h *Handler) RegisterRoutes(r server.Router) {
	// MCP 协议端点：任意 MCP 客户端（Claude Desktop/IDE）经 Bearer 凭证接入。
	// Streamable HTTP 为单端点语义（会话经 Mcp-Session-Id 头管理，无 URL 子路径）
	r.Public.Any("/mcp", h.mcpEndpoint)

	t := r.Authed.Group("/mcp/tokens")
	{
		t.GET("", h.listTokens)
		t.POST("", h.createToken)
		t.POST("/:id/revoke", h.revokeToken)
		t.POST("/:id/enable", h.enableToken)
	}
}

// mcpEndpoint：Bearer 凭证校验 → 角色注入 ctx → Streamable HTTP handler。
func (h *Handler) mcpEndpoint(c *gin.Context) {
	if !h.mcpLimiter.Allow(c.ClientIP()) {
		httpx.Fail(c, http.StatusTooManyRequests, 429, "请求过于频繁")
		return
	}
	auth := c.GetHeader("Authorization")
	plaintext := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	if plaintext == "" || plaintext == auth {
		httpx.FailUnauthorized(c, "缺少 Bearer MCP 凭证")
		return
	}
	tc, err := h.svc.Verify(c.Request.Context(), plaintext)
	if err != nil {
		httpx.FailUnauthorized(c, err.Error())
		return
	}
	// 角色随请求进 ctx：getServer 据此选 server，tool handler 据此审计
	req := c.Request.WithContext(withToken(c.Request.Context(), tc))
	h.streamable().ServeHTTP(c.Writer, req)
}

// streamable 惰性构造（首次请求时注册 tools——此时 app 层数据源已注入）。
// getServer 按请求凭证角色返回对应 server（角色即 allowlist 维度）。
func (h *Handler) streamable() *mcp.StreamableHTTPHandler {
	h.once.Do(func() {
		h.mcpHTTP = mcp.NewStreamableHTTPHandler(
			func(r *http.Request) *mcp.Server {
				if tc := tokenFrom(r.Context()); tc != nil {
					return h.svc.ServerFor(tc.Role)
				}
				return h.svc.ServerFor(TokenRoleDev) // 理论不可达（中间件已拦）
			}, nil)
	})
	return h.mcpHTTP
}

// ---- 管理面（admin，casbin v15） ----

func (h *Handler) listTokens(c *gin.Context) {
	list, err := h.svc.ListTokens(c.Request.Context())
	if err != nil {
		httpx.FailServer(c, err)
		return
	}
	// 近 7 天调用次数一并返回（前端展示用）
	type row struct {
		Token
		Calls7d int64 `json:"calls7d"`
	}
	ids := make([]uint, len(list))
	for i, t := range list {
		ids[i] = t.ID
	}
	counts := map[uint]int64{}
	if len(ids) > 0 {
		var cnts []struct {
			TokenID uint
			N       int64
		}
		h.svc.db.WithContext(c.Request.Context()).Model(&Call{}).
			Select("token_id, COUNT(*) AS n").
			Where("token_id IN ? AND created_at > ?", ids, time.Now().Add(-7*24*time.Hour)).
			Group("token_id").Scan(&cnts)
		for _, r := range cnts {
			counts[r.TokenID] = r.N
		}
	}
	out := make([]row, len(list))
	for i, t := range list {
		out[i] = row{Token: t, Calls7d: counts[t.ID]}
	}
	httpx.OK(c, out)
}

func (h *Handler) createToken(c *gin.Context) {
	var in struct {
		Name string `json:"name" binding:"required,max=64"`
		Role string `json:"role" binding:"required,oneof=admin dev"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := h.svc.IssueToken(c.Request.Context(), in.Name, in.Role)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, out)
}

func (h *Handler) revokeToken(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.Revoke(c.Request.Context(), id); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}

func (h *Handler) enableToken(c *gin.Context) {
	id, ok := httpx.ParamID(c)
	if !ok {
		return
	}
	if err := h.svc.Enable(c.Request.Context(), id); err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	httpx.OK(c, nil)
}
