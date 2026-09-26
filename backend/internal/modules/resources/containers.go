package resources

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	dc "github.com/moby/moby/client"

	"github.com/custos-machina/backend/internal/modules/auth"
	"github.com/custos-machina/backend/internal/pkg/httpx"
)

// demuxDockerStream 解复用非 TTY 容器日志流（8 字节头：stream 类型 1 + 0 + 4 字节长度）。
// 等价 moby stdcopy，但不为这一函数引入整棵 moby/moby 依赖树。
func demuxDockerStream(w io.Writer, r io.Reader) error {
	header := make([]byte, 8)
	for {
		if _, err := io.ReadFull(r, header); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		size := binary.BigEndian.Uint32(header[4:8])
		if size == 0 {
			continue
		}
		if _, err := io.CopyN(w, r, int64(size)); err != nil {
			return err
		}
	}
}

// 容器管理 + 环境探测 + compose 部署（M4）。

type containerView struct {
	ID     string `json:"id"` // 短 ID
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Status string `json:"status"` // docker 的可读状态（Up 2 hours 等）
	// compose 部署的容器自带项目标签，用于按项目聚合（M4.5）
	ComposeProject string `json:"composeProject,omitempty"`
	ComposeFile    string `json:"composeFile,omitempty"`
	ComposeService string `json:"composeService,omitempty"`
}

func (h *Handler) listContainers(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	srv, cred, err := h.svc.serverWithCredential(c.Request.Context(), id)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	cli, sshConn, err := dockerClientFor(srv, cred)
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	defer sshConn.Close()
	list, err := cli.ContainerList(c.Request.Context(), dc.ContainerListOptions{All: true})
	cli.Close()
	if err != nil {
		httpx.FailUpstream(c, fmt.Sprintf("Docker API 失败: %v", err))
		return
	}
	out := make([]containerView, 0, len(list.Items))
	for _, ct := range list.Items {
		name := ""
		if len(ct.Names) > 0 {
			name = strings.TrimPrefix(ct.Names[0], "/")
		}
		out = append(out, containerView{
			ID: ct.ID[:12], Name: name, Image: ct.Image,
			State: string(ct.State), Status: ct.Status,
			ComposeProject: ct.Labels["com.docker.compose.project"],
			ComposeFile:    ct.Labels["com.docker.compose.project.config_files"],
			ComposeService: ct.Labels["com.docker.compose.service"],
		})
	}
	httpx.OK(c, out)
}

func (h *Handler) containerAction(c *gin.Context) {
	id, cid, ok := twoIDs(c)
	if !ok {
		return
	}
	srv, cred, err := h.svc.serverWithCredential(c.Request.Context(), id)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	cli, sshConn, err := dockerClientFor(srv, cred)
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	defer sshConn.Close()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	var aerr error
	switch c.Param("action") {
	case "start":
		_, aerr = cli.ContainerStart(ctx, cid, dc.ContainerStartOptions{})
	case "stop":
		_, aerr = cli.ContainerStop(ctx, cid, dc.ContainerStopOptions{})
	default:
		httpx.FailBadRequest(c, "不支持的操作")
		return
	}
	cli.Close()
	if aerr != nil {
		httpx.FailUpstream(c, aerr.Error())
		return
	}
	h.svc.recordSimpleEvent(ctx, srv.ID, "container_op",
		fmt.Sprintf("%s 容器 %s %s", h.operator(c), cid, c.Param("action")))
	httpx.OK(c, nil)
}

// containerLogs tail 模式返回 JSON 行数组；follow=1 时转 SSE 流。
func (h *Handler) containerLogs(c *gin.Context) {
	id, cid, ok := twoIDs(c)
	if !ok {
		return
	}
	tail := 200
	if v, err := strconv.Atoi(c.Query("tail")); err == nil && v > 0 && v <= 5000 {
		tail = v
	}
	follow := c.Query("follow") == "1"

	srv, cred, err := h.svc.serverWithCredential(c.Request.Context(), id)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	cli, sshConn, err := dockerClientFor(srv, cred)
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	defer sshConn.Close()
	reader, err := cli.ContainerLogs(c.Request.Context(), cid, dc.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Tail: strconv.Itoa(tail), Follow: follow,
	})
	if err != nil {
		cli.Close()
		httpx.FailUpstream(c, err.Error())
		return
	}
	// 非 TTY 容器日志是 8 字节头的双路复用流，需 stdcopy 解复用
	demuxR, demuxW := io.Pipe()
	go func() {
		_ = demuxDockerStream(demuxW, reader)
		demuxW.Close()
	}()

	if !follow {
		defer cli.Close()
		defer reader.Close()
		buf := &strings.Builder{}
		_, _ = io.Copy(buf, io.LimitReader(demuxR, 512*1024)) // 最多 512KB，防整段倒灌
		lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
		httpx.OK(c, lines)
		return
	}

	// SSE 跟随：读放到独立 goroutine 喂 channel，客户端断开（ctx 取消）即刻退出，
	// 不会因等待下一条日志而吊住连接与 SSH 资源
	logCh := make(chan []byte, 16)
	readDone := make(chan struct{})
	// 消费端放弃（客户端断开）后必须解除读 goroutine 的阻塞发送，
	// 否则 reader.Close() 无法唤醒它、<-readDone 永久挂起（handler 泄漏）
	consumerGone := make(chan struct{})
	go func() {
		defer close(readDone)
		buf := make([]byte, 4096)
		for {
			n, err := demuxR.Read(buf)
			if n > 0 {
				b := make([]byte, n)
				copy(b, buf[:n])
				select {
				case logCh <- b:
				case <-consumerGone:
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	c.Stream(func(_ io.Writer) bool {
		select {
		case <-c.Request.Context().Done():
			return false
		case <-readDone:
			return false
		case b := <-logCh:
			c.SSEvent("log", string(b))
			return true
		}
	})
	cli.Close()
	reader.Close()
	close(consumerGone)
	<-readDone
}

// containerStats 一次性资源占用（CPU% / 内存）。
func (h *Handler) containerStats(c *gin.Context) {
	id, cid, ok := twoIDs(c)
	if !ok {
		return
	}
	srv, cred, err := h.svc.serverWithCredential(c.Request.Context(), id)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	cli, sshConn, err := dockerClientFor(srv, cred)
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	defer sshConn.Close()
	resp, err := cli.ContainerStats(c.Request.Context(), cid, dc.ContainerStatsOptions{
		Stream: false, IncludePreviousSample: true,
	})
	cli.Close()
	if err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	defer resp.Body.Close()
	var v containerStatsJSON
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		httpx.FailUpstream(c, err.Error())
		return
	}
	cpuDelta := v.CPUStats.CPUUsage.TotalUsage - v.PreCPUStats.CPUUsage.TotalUsage
	sysDelta := v.CPUStats.SystemCPUUsage - v.PreCPUStats.SystemCPUUsage
	cpuPct := 0.0
	if sysDelta > 0 && cpuDelta > 0 && v.CPUStats.OnlineCPUs > 0 {
		cpuPct = (float64(cpuDelta) / float64(sysDelta)) * float64(v.CPUStats.OnlineCPUs) * 100
	}
	httpx.OK(c, gin.H{
		"cpuPct":   fmt.Sprintf("%.1f", cpuPct),
		"memUsed":  v.MemoryStats.Usage - min(v.MemoryStats.Stats["cache"], v.MemoryStats.Usage),
		"memLimit": v.MemoryStats.Limit,
	})
}

// docker stats API 的 JSON 片段（只取需要的字段）。
type containerStatsJSON struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64  `json:"system_cpu_usage"`
		OnlineCPUs     float64 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
}

func min(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

// dockerStatsLine docker stats --format '{{json .}}' 的单行结构。
type dockerStatsLine struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`  // "0.15%"
	MemPerc  string `json:"MemPerc"`  // "2.31%"
	MemUsage string `json:"MemUsage"` // "50MiB / 1.87GiB"
}

// allContainerStats 一条 SSH 命令批量取全部容器占用（docker stats --no-stream），
// 避免逐容器走 Docker API 各开一条 SSH 隧道连接。
func (h *Handler) allContainerStats(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	srv, cred, err := h.svc.serverWithCredential(c.Request.Context(), id)
	if err != nil {
		httpx.FailBadRequest(c, err.Error())
		return
	}
	out, err := sshRunOutput(srv, cred,
		`docker stats --no-stream --format '{{json .}}'`, 20*time.Second)
	if err != nil {
		httpx.FailUpstream(c, fmt.Sprintf("docker stats 失败: %v\n%s", err, out))
		return
	}
	type stat struct {
		ID       string `json:"id"`
		CPUPerc  string `json:"cpuPerc"`
		MemPerc  string `json:"memPerc"`
		MemUsage string `json:"memUsage"`
	}
	stats := []stat{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var l dockerStatsLine
		if err := json.Unmarshal([]byte(line), &l); err != nil || l.ID == "" {
			continue
		}
		stats = append(stats, stat{
			ID: l.ID, CPUPerc: l.CPUPerc, MemPerc: l.MemPerc, MemUsage: l.MemUsage,
		})
	}
	httpx.OK(c, stats)
}

func twoIDs(c *gin.Context) (uint, string, bool) {
	id, ok := idParam(c)
	if !ok {
		return 0, "", false
	}
	cid := c.Param("cid")
	if cid == "" {
		httpx.FailBadRequest(c, "缺少容器 id")
		return 0, "", false
	}
	return id, cid, true
}

func (h *Handler) operator(c *gin.Context) string {
	if claims := auth.ClaimsFromContext(c); claims != nil && claims.DisplayName != "" {
		return claims.DisplayName
	}
	return "unknown"
}
