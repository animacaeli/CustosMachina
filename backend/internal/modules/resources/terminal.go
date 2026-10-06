package resources

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/custos-machina/backend/internal/modules/auth"
	"github.com/custos-machina/backend/internal/modules/rbac"
	"github.com/custos-machina/backend/internal/pkg/httpx"
)

// Web 终端（M3）：WebSocket ↔ SSH PTY 字节搬运，网关不解释内容。
// 会话审计：默认开启，输出落 data/terminal-logs/<serverID>/<sessionID>.log
// （首行 JSON 元数据：operator/起止时间），按 server 滚动保留 7 天
// （auditRetention 任务清理），会话关闭时落 server_events。

const (
	terminalAuditDir    = "data/terminal-logs"
	terminalAuditRetain = 7 * 24 * time.Hour
	ptyCols             = 120
	ptyRows             = 32
	terminalIdleTimeout = 30 * time.Minute // 无输入即断，防挂死会话占用 SSH/审计句柄
	maxSessionsPerUser  = 3                // 同一操作人并发终端数上限
)

var wsUpgrader = websocket.Upgrader{
	// 同源校验（按 hostname，忽略端口）：严格同 host:port 会误伤开发场景——
	// vite 代理 changeOrigin 会把 Host 改写成后端端口，Origin 仍是前端端口。
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true // curl 等非浏览器客户端
		}
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}
		oh, _, err2 := net.SplitHostPort(u.Host)
		if err2 != nil {
			oh = u.Hostname()
		}
		rh, _, err3 := net.SplitHostPort(r.Host)
		if err3 != nil {
			rh = r.Host
		}
		if strings.EqualFold(oh, rh) {
			return true
		}
		// loopback 别名等价：localhost / 127.0.0.1 / [::1] 视为同机
		return isLoopbackHost(oh) && isLoopbackHost(rh)
	},
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
}

func isLoopbackHost(h string) bool {
	return strings.EqualFold(h, "localhost") || h == "127.0.0.1" || h == "::1"
}

// activeSessions 操作人 -> 活跃会话计数（并发上限用）。
var activeSessions sync.Map

func acquireSession(operator string) bool {
	v, _ := activeSessions.LoadOrStore(operator, new(int32))
	n := v.(*int32)
	if atomic.AddInt32(n, 1) > maxSessionsPerUser {
		atomic.AddInt32(n, -1)
		return false
	}
	return true
}

func releaseSession(operator string) {
	if v, ok := activeSessions.Load(operator); ok {
		atomic.AddInt32(v.(*int32), -1)
	}
}

type terminalSession struct {
	server   *Server
	operator string
	file     *os.File
	started  time.Time
}

// openAudit 创建审计文件并写 asciinema v2 cast 头（P6-M6：录制带时序帧，
// 支持网页回放）。env 内嵌 operator/server 元数据（列表解析用）。
func (ts *terminalSession) openAudit() error {
	dir := filepath.Join(terminalAuditDir, fmt.Sprintf("%d", ts.server.ID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("%s.cast", ts.started.Format("20060102-150405.000"))
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	ts.file = f
	header, _ := json.Marshal(map[string]any{
		"version": 2, "width": ptyCols, "height": ptyRows,
		"timestamp": ts.started.Unix(),
		"title":     fmt.Sprintf("%s@%s", ts.operator, ts.server.Name),
		"env": map[string]string{
			"TERM": "xterm-256color", "operator": ts.operator,
			"serverId": fmt.Sprint(ts.server.ID), "server": ts.server.Name,
		},
	})
	_, err = f.Write(append(header, '\n'))
	return err
}

// writeAudit 追加 cast 输出帧：[相对秒, "o", 数据]（JSON 字符串转义由
// Marshal 处理）。审计写失败不影响会话。
func (ts *terminalSession) writeAudit(p []byte) {
	if ts.file != nil {
		delta := float64(time.Since(ts.started).Nanoseconds()) / 1e9
		frame, _ := json.Marshal([]any{delta, "o", string(p)})
		_, _ = ts.file.Write(append(frame, '\n'))
	}
}

func (ts *terminalSession) closeAudit() {
	if ts.file != nil {
		_ = ts.file.Close()
		ts.file = nil
	}
}

// resizeMsg 前端窗口尺寸控制消息（JSON 文本帧）。
type resizeMsg struct {
	Action string `json:"action"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

// handleTerminal GET /servers/:id/terminal —— WebSocket 升级后全双工转发。
func (h *Handler) handleTerminal(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	// 兜底 gate（中间件已按同一口径裁决）：终端不认通配策略，
	// 仅内置 admin 角色或该登录名的逐主机精确 ACL。
	if !rbac.TerminalAllowedForServer(c, id) {
		httpx.Fail(c, http.StatusForbidden, 403, "无该主机的终端授权（需管理员角色或主机级授权）")
		return
	}
	srv, err := h.svc.servers.GetByID(c.Request.Context(), id)
	if err != nil {
		httpx.FailNotFound(c, "服务器不存在")
		return
	}
	claims := auth.ClaimsFromContext(c)
	operator := "unknown"
	if claims != nil && claims.DisplayName != "" {
		operator = claims.DisplayName
	}
	if !acquireSession(operator) {
		httpx.Fail(c, http.StatusTooManyRequests, 429,
			fmt.Sprintf("并发终端会话超过上限 %d，请先关闭其他窗口", maxSessionsPerUser))
		return
	}
	defer releaseSession(operator)
	cred, err := h.svc.decryptCredential(srv.Credential)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	client, err := DialSSH(srv.Host, srv.Port, cred, srv.ID)
	if err != nil {
		httpx.FailUpstream(c, fmt.Sprintf("SSH 连接失败: %v", err))
		return
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		httpx.FailUpstream(c, fmt.Sprintf("打开会话失败: %v", err))
		return
	}
	defer session.Close()
	if err := session.RequestPty("xterm-256color", ptyRows, ptyCols, nil); err != nil {
		httpx.FailUpstream(c, fmt.Sprintf("申请 PTY 失败: %v", err))
		return
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	if err := session.Shell(); err != nil {
		httpx.FailUpstream(c, fmt.Sprintf("启动 shell 失败: %v", err))
		return
	}

	ws, err := wsUpgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // Upgrade 已写错误响应
	}
	defer ws.Close()

	ts := &terminalSession{server: srv, operator: operator, started: time.Now()}
	_ = ts.openAudit()
	defer func() {
		ts.closeAudit()
		h.svc.recordTerminalEvent(c.Request.Context(), srv, operator, time.Since(ts.started))
	}()

	// SSH → 浏览器
	var wmu sync.Mutex // websocket 并发写需串行化
	writeErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 8192)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				wmu.Lock()
				_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
				werr := ws.WriteMessage(websocket.BinaryMessage, buf[:n])
				wmu.Unlock()
				ts.writeAudit(buf[:n])
				if werr != nil {
					writeErr <- werr
					return
				}
			}
			if err != nil {
				wmu.Lock()
				_ = ws.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, "ssh closed"))
				wmu.Unlock()
				writeErr <- err
				return
			}
		}
	}()

	// 浏览器 → SSH（resize 控制消息走文本帧 {"action":"resize","cols":..,"rows":..}）
	// 读侧设置空闲超时：30 分钟无任何输入即断开（输出不算活跃）。
	readErr := make(chan error, 1)
	go func() {
		defer func() { _ = ws.SetReadDeadline(time.Time{}) }()
		for {
			_ = ws.SetReadDeadline(time.Now().Add(terminalIdleTimeout))
			mt, data, err := ws.ReadMessage()
			if err != nil {
				readErr <- err
				return
			}
			switch mt {
			case websocket.TextMessage:
				var rm resizeMsg
				if err := json.Unmarshal(data, &rm); err == nil && rm.Action == "resize" &&
					rm.Cols > 0 && rm.Cols <= 500 && rm.Rows > 0 && rm.Rows <= 200 {
					_ = session.WindowChange(rm.Rows, rm.Cols)
				}
			case websocket.BinaryMessage:
				ts.writeAudit(data)
				if _, err := stdin.Write(data); err != nil {
					readErr <- err
					return
				}
			}
		}
	}()

	select {
	case <-readErr:
	case <-writeErr:
	}
	_ = session.Close()
}
