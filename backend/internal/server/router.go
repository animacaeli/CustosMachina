package server

import (
	"fmt"
	"runtime/debug"

	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/config"
	"github.com/custos-machina/backend/internal/pkg/httpx"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

// NewEngine 构建 gin 引擎并完成所有模块路由注册。
// auth 为认证中间件（JWT），authz 为鉴权中间件（casbin），均由业务模块注入。
func NewEngine(cfg *config.Config, modules Modules, auth AuthMiddleware, authz gin.HandlerFunc) *gin.Engine {
	gin.SetMode(cfg.HTTP.Mode)
	e := gin.New()
	// 限速与审计依赖 ClientIP：只信任配置的代理链，防 XFF 伪造轮换绕过限速
	if err := e.SetTrustedProxies(splitList(cfg.HTTP.TrustedProxies)); err != nil {
		logger.Warnf("[server] 可信代理配置无效 %q: %v", cfg.HTTP.TrustedProxies, err)
		_ = e.SetTrustedProxies(nil)
	}
	e.Use(requestLogger(), zapRecovery(), securityHeaders(), cors(cfg.CORS.Origins))

	api := e.Group("/api", bodyLimit(), bodyDeadline())
	public := api.Group("")
	authed := api.Group("")
	authed.Use(auth(), authz)

	router := Router{Public: public, Authed: authed}
	for _, mod := range modules {
		mod.RegisterRoutes(router)
	}
	return e
}

// requestLogger 用 zap 记录请求访问日志（替代 gin.Logger 的标准库输出）。
func requestLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Infof("[gin] %3d | %13v | %-7s %s",
			c.Writer.Status(), time.Since(start), c.Request.Method, c.Request.URL.Path)
	}
}

// uploadPrefixes 上传/部署类路由前缀（bodyLimit 放宽上限、bodyDeadline
// 放宽读取期限共用）。按 FullPath 前缀匹配，覆盖各环境段（:id 参数化路径）。
var uploadPrefixes = []string{
	"/api/server-files/",  // SFTP 上传（handler 限 100MB）
	"/api/server-compose", // compose 部署（yaml+伴随配置）
}

// bodyLimit 全局请求体上限兜底（v0.12.3 独立审核 T2）：路由层各自有业务
// 校验（binding max / io.LimitReader），但缺全局兜底——畸形大请求会先被
// 整体读入。默认 2MB；上传/部署类路由单独放宽。
func bodyLimit() gin.HandlerFunc {
	const def = 2 << 20 // 2MB
	exempt := map[string]int64{
		"/api/server-files/":  110 << 20,
		"/api/server-compose": 16 << 20,
	}
	return func(c *gin.Context) {
		var max int64 = def
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		for prefix, m := range exempt {
			if strings.HasPrefix(path, prefix) {
				max = m
				break
			}
		}
		if c.Request.ContentLength > max {
			c.Header("Connection", "close")
			httpx.Fail(c, http.StatusRequestEntityTooLarge, 413,
				fmt.Sprintf("请求体超过上限 %dMB", max>>20))
			c.Abort()
			return
		}
		// chunked（无 Content-Length）场景在读侧强制封顶
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		c.Next()
	}
}

// bodyDeadline 请求体读取期限（v0.12.14 独立审核 N3）：http.Server 只设了
// ReadHeaderTimeout（SSE/WebSocket 长连接不能全局 ReadTimeout），普通请求
// 的 body 因此无期限——慢速 body 可长期占用连接与 goroutine。分层设期限：
// 普通 JSON API 30s、上传/compose 10 分钟。仅对带 body 的请求生效——GET
// 无 body 的 SSE 与终端 WebSocket（升级请求无 body）天然豁免。
func bodyDeadline() gin.HandlerFunc {
	return bodyDeadlineWith(30*time.Second, 10*time.Minute)
}

func bodyDeadlineWith(def, upload time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		hasBody := c.Request.Body != nil &&
			(c.Request.ContentLength > 0 || len(c.Request.TransferEncoding) > 0)
		if !hasBody {
			c.Next()
			return
		}
		deadline := def
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}
		for _, prefix := range uploadPrefixes {
			if strings.HasPrefix(path, prefix) {
				deadline = upload
				break
			}
		}
		// ResponseController 把期限落到底层连接；handler 返回后 server
		// 会按 IdleTimeout 重置，不影响该连接后续请求
		_ = http.NewResponseController(c.Writer).SetReadDeadline(time.Now().Add(deadline))
		c.Next()
	}
}

// securityHeaders 基础安全响应头（v0.12.3 独立审核 T5）。HSTS 由前置
// nginx 在 TLS 终止处下发（HTTP 明文端口下发会被浏览器忽略）。
func securityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}

// zapRecovery panic 恢复走 zap（结构化、可关联请求）——gin.Recovery 会把
// 明文堆栈直写 stderr，绕过日志管线（v0.12.0 审计中等项）。
func zapRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if err := recover(); err != nil {
				logger.Errorf("[gin] panic: %v\n%s %s\n%s", err, c.Request.Method, c.Request.URL.Path, debug.Stack())
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func cors(origins string) gin.HandlerFunc {
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		allowed := false
		for _, o := range strings.Split(origins, ",") {
			if strings.TrimSpace(o) == "*" || strings.TrimSpace(o) == origin {
				allowed = true
				break
			}
		}
		if origin != "" && allowed {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
