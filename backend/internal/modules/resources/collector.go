package resources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/jobs"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

const (
	collectInterval = 1 * time.Second    // 调度粒度：每秒检查哪些服务器到期
	flushInterval   = 10 * time.Second   // 批量落库间隔
	retainInterval  = 1 * time.Hour      // 保留策略清理间隔
	retentionRaw    = 7 * 24 * time.Hour // 原始点保留 7 天（降采样 90 天后置）
	ringSize        = 360                // 内存环形缓冲点数（15s 间隔 × 360 ≈ 1.5h）
	failThreshold   = 3                  // 连续失败 N 次判不可达
	sshMetricCmd    = `head -n 1 /proc/stat; grep -E '^(MemTotal|MemAvailable):' /proc/meminfo`
)

// rawSample 一次 SSH 采集的原始值（CPU 为累计时间，需差值计算使用率）。
type rawSample struct {
	cpuTimes []float64 // /proc/stat cpu 聚合行的 10 个累计值（秒/USER_HZ）
	memTotal uint64    // 字节
	memAvail uint64    // 字节
}

// parseMetricOutput 解析 sshMetricCmd 的输出。纯函数，便于单测。
func parseMetricOutput(out string) (*rawSample, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		return nil, fmt.Errorf("采集输出行数不足: %q", out)
	}
	cpuLine := strings.Fields(strings.TrimSpace(lines[0]))
	if len(cpuLine) < 5 || cpuLine[0] != "cpu" {
		return nil, fmt.Errorf("cpu 行格式异常: %q", lines[0])
	}
	cpu := make([]float64, len(cpuLine)-1)
	for i := 1; i < len(cpuLine); i++ {
		v, err := strconv.ParseFloat(cpuLine[i], 64)
		if err != nil {
			return nil, fmt.Errorf("cpu 时间解析失败: %w", err)
		}
		cpu[i-1] = v
	}
	var total, avail uint64
	for _, ln := range lines[1:3] {
		fields := strings.Fields(ln)
		if len(fields) < 2 {
			return nil, fmt.Errorf("meminfo 行格式异常: %q", ln)
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("meminfo 解析失败: %w", err)
		}
		kb := v * 1024
		switch {
		case strings.HasPrefix(fields[0], "MemTotal"):
			total = kb
		case strings.HasPrefix(fields[0], "MemAvailable"):
			avail = kb
		}
	}
	if total == 0 {
		return nil, fmt.Errorf("MemTotal 缺失")
	}
	return &rawSample{cpuTimes: cpu, memTotal: total, memAvail: avail}, nil
}

// cpuPctBetween 用两次采样的累计 CPU 时间差计算使用率（0~100）。
func cpuPctBetween(prev, cur []float64) float64 {
	if len(prev) != len(cur) || len(prev) == 0 {
		return 0
	}
	var prevSum, curSum float64
	for i := range cur {
		prevSum += prev[i]
		curSum += cur[i]
	}
	totalDelta := curSum - prevSum
	if totalDelta <= 0 {
		return 0
	}
	var idle float64
	// idle = idle(3列起第4值) + iowait；不同内核列数一致时才取
	if len(cur) >= 5 {
		idle = (cur[3] - prev[3])
	}
	if len(cur) >= 6 {
		idle += (cur[4] - prev[4])
	}
	if idle < 0 {
		idle = 0
	}
	pct := (1 - idle/totalDelta) * 100
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// ring 定长环形缓冲，写满覆盖最旧。
type ring struct {
	mu   sync.Mutex
	data []MetricSample
	head int // 下一个写入位置
	n    int
}

func newRing() *ring { return &ring{data: make([]MetricSample, ringSize)} }

func (r *ring) push(s MetricSample) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.data[r.head] = s
	r.head = (r.head + 1) % ringSize
	if r.n < ringSize {
		r.n++
	}
}

// snapshot 按时间升序返回缓冲内样本。
func (r *ring) snapshot() []MetricSample {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]MetricSample, 0, r.n)
	start := (r.head - r.n + ringSize) % ringSize
	for i := 0; i < r.n; i++ {
		out = append(out, r.data[(start+i)%ringSize])
	}
	return out
}

// Collector agentless 指标采集器：调度、内存缓冲、批量落库、保留清理、状态事件。
type Collector struct {
	db      *gorm.DB
	servers *ServerRepository
	svc     *Service // 复用凭据解密

	group *jobs.Group

	notifier OpsNotifier // 可选：运维群推送（notify 模块注入）

	mu        sync.Mutex
	rings     map[uint]*ring
	prevCPU   map[uint][]float64 // 上一次累计 CPU 时间（差值计算用）
	nextDue   map[uint]time.Time // 每台服务器下次到期时间（错峰）
	failCount map[uint]int
	lastState map[uint]ServerStatus
	pending   []MetricSample
}

// OpsNotifier 平台级运维告警出口（notify.Service 实现；空实现 = 只落库不推送）。
type OpsNotifier interface {
	NotifyOps(ctx context.Context, title, detail string)
}

// NewCollector 构造并启动采集任务；wire 聚合返回的 cleanup 会在停机时调用 Stop。
func NewCollector(db *gorm.DB, servers *ServerRepository, svc *Service) (*Collector, func(), error) {
	c := &Collector{
		db: db, servers: servers, svc: svc,
		rings: map[uint]*ring{}, prevCPU: map[uint][]float64{},
		nextDue: map[uint]time.Time{}, failCount: map[uint]int{},
		lastState: map[uint]ServerStatus{}, pending: nil,
	}
	c.group = jobs.NewGroup(
		jobs.Job{Name: "metrics:collect", Interval: collectInterval, Fn: c.collectTick},
		jobs.Job{Name: "metrics:flush", Interval: flushInterval, Fn: c.flush},
		jobs.Job{Name: "metrics:retention", Interval: retainInterval, Fn: c.retention},
		jobs.Job{Name: "terminal-audit:retention", Interval: retainInterval, Fn: auditRetention},
		// 主机配置低频刷新（CPU/内存不变，磁盘使用率缓变，30 分钟足够新）
		jobs.Job{Name: "hostinfo:refresh", Interval: 30 * time.Minute, Fn: c.refreshHostInfo},
	)
	c.group.Start()
	return c, c.group.Stop, nil
}

// collectTick 每秒执行：找出到期服务器，逐台采集（并发度 4）。
func (c *Collector) collectTick(ctx context.Context) error {
	now := time.Now()
	list, err := c.servers.List(ctx)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i := range list {
		srv := list[i]
		c.mu.Lock()
		due, has := c.nextDue[srv.ID]
		c.mu.Unlock()
		if has && now.Before(due) {
			continue
		}
		interval := time.Duration(srv.MetricSecs) * time.Second
		if interval <= 0 {
			interval = time.Duration(DefaultMetricSecs) * time.Second
		}
		c.mu.Lock()
		c.nextDue[srv.ID] = now.Add(interval)
		c.mu.Unlock()
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer func() { <-sem; wg.Done() }()
			c.collectOne(ctx, srv)
		}()
	}
	wg.Wait()
	return nil
}

func (c *Collector) collectOne(ctx context.Context, srv Server) {
	cred, err := c.svc.decryptCredential(srv.Credential)
	if err != nil {
		c.recordFailure(ctx, &srv, err)
		return
	}
	client, err := DialSSH(srv.Host, srv.Port, cred)
	if err != nil {
		c.recordFailure(ctx, &srv, err)
		return
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		c.recordFailure(ctx, &srv, err)
		return
	}
	defer session.Close()
	out, err := session.Output(sshMetricCmd)
	if err != nil {
		c.recordFailure(ctx, &srv, err)
		return
	}
	raw, err := parseMetricOutput(string(out))
	if err != nil {
		c.recordFailure(ctx, &srv, err)
		return
	}
	now := time.Now()
	sample := MetricSample{
		ServerID: srv.ID, TS: now,
		MemUsed: raw.memTotal - raw.memAvail, MemTotal: raw.memTotal,
	}
	c.mu.Lock()
	prev := c.prevCPU[srv.ID]
	c.prevCPU[srv.ID] = raw.cpuTimes
	c.mu.Unlock()
	if prev != nil {
		sample.CPUPct = cpuPctBetween(prev, raw.cpuTimes)
	} else {
		sample.CPUPct = 0 // 首个采样点无差值可比
	}
	c.mu.Lock()
	r := c.rings[srv.ID]
	if r == nil {
		r = newRing()
		c.rings[srv.ID] = r
	}
	c.pending = append(c.pending, sample)
	c.mu.Unlock()
	r.push(sample)
	c.recordSuccess(ctx, &srv, now)
}

func (c *Collector) recordSuccess(ctx context.Context, srv *Server, now time.Time) {
	c.mu.Lock()
	fails := c.failCount[srv.ID]
	c.failCount[srv.ID] = 0
	c.mu.Unlock()
	was := srv.Status
	srv.Status = StatusReachable
	srv.LastSeen = &now
	if err := c.db.WithContext(ctx).Model(&Server{}).
		Where("id = ?", srv.ID).
		Updates(map[string]any{"status": StatusReachable, "last_seen": now}).Error; err != nil {
		return
	}
	if fails >= failThreshold || was == StatusUnreachable {
		c.emitEvent(ctx, srv.ID, "recovered", fmt.Sprintf("连续失败 %d 次后恢复", fails))
	}
}

func (c *Collector) recordFailure(ctx context.Context, srv *Server, cause error) {
	c.mu.Lock()
	c.failCount[srv.ID]++
	fails := c.failCount[srv.ID]
	c.mu.Unlock()
	if fails < failThreshold {
		return // 偶发失败不打状态、不落事件
	}
	if err := c.db.WithContext(ctx).Model(&Server{}).
		Where("id = ?", srv.ID).
		Update("status", StatusUnreachable).Error; err != nil {
		return
	}
	if fails == failThreshold { // 只在判定瞬间落一次事件，持续失败不重复
		c.emitEvent(ctx, srv.ID, "unreachable", cause.Error())
	}
}

// refreshHostInfo 刷新全部主机的配置缓存（逐台探测，失败只记日志）。
func (c *Collector) refreshHostInfo(ctx context.Context) error {
	var servers []Server
	if err := c.db.WithContext(ctx).Where("deleted_at IS NULL").Find(&servers).Error; err != nil {
		return err
	}
	for _, srv := range servers {
		if _, err := c.svc.ProbeHostInfo(ctx, srv.ID); err != nil {
			logger.Warnf("[resources] 主机配置刷新失败 server=%d: %v", srv.ID, err)
		}
	}
	return nil
}

// SetNotifier 注入运维告警出口（app 组装时调用；不注入则只落库不推送）。
func (c *Collector) SetNotifier(n OpsNotifier) { c.notifier = n }

// emitEvent 落 server_events、打日志并推运维群（第三阶段 M1 起 notify 已实现）。
func (c *Collector) emitEvent(ctx context.Context, serverID uint, typ, msg string) {
	ev := ServerEvent{ServerID: serverID, Type: typ, Message: truncate(msg, 255)}
	if err := c.db.WithContext(ctx).Create(&ev).Error; err != nil {
		fmt.Printf("[resources] 落事件失败 server=%d type=%s: %v\n", serverID, typ, err)
	}
	fmt.Printf("[resources] server=%d %s: %s\n", serverID, typ, msg)
	if c.notifier != nil && (typ == "unreachable" || typ == "recovered") {
		go c.notifier.NotifyOps(context.WithoutCancel(ctx),
			"服务器"+map[string]string{"unreachable": "不可达", "recovered": "已恢复"}[typ],
			fmt.Sprintf("服务器 ID=%d\n事件：%s", serverID, msg))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// flush 批量落库。
func (c *Collector) flush(ctx context.Context) error {
	c.mu.Lock()
	if len(c.pending) == 0 {
		c.mu.Unlock()
		return nil
	}
	batch := c.pending
	c.pending = nil
	c.mu.Unlock()
	if err := c.db.WithContext(ctx).CreateInBatches(batch, 200).Error; err != nil {
		// 失败塞回队首，下轮重试（会话内存有限，封顶防止膨胀）
		c.mu.Lock()
		if len(c.pending)+len(batch) <= 10000 {
			c.pending = append(batch, c.pending...)
		}
		c.mu.Unlock()
		return err
	}
	return nil
}

// retention 清理超期原始点（三方言通用的 DELETE）。
func (c *Collector) retention(ctx context.Context) error {
	return c.db.WithContext(ctx).
		Where("ts < ?", time.Now().Add(-retentionRaw)).
		Delete(&MetricSample{}).Error
}

// LatestFor 返回全部服务器的近期样本（列表 sparkline 用，走内存不打库）。
func (c *Collector) LatestFor() map[uint][]MetricSample {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[uint][]MetricSample, len(c.rings))
	for id, r := range c.rings {
		out[id] = r.snapshot()
	}
	return out
}

// QueryFor 单服务器查询：1.5h 内走内存，更早走库。
func (c *Collector) QueryFor(ctx context.Context, serverID uint, since time.Time) ([]MetricSample, error) {
	c.mu.Lock()
	r := c.rings[serverID]
	c.mu.Unlock()
	if r != nil && since.After(time.Now().Add(-retentionWindow())) {
		return r.snapshot(), nil
	}
	var out []MetricSample
	err := c.db.WithContext(ctx).
		Where("server_id = ? AND ts >= ?", serverID, since).
		Order("ts").Limit(2000).Find(&out).Error
	return out, err
}

// auditRetention 清理超期的终端会话审计文件（按文件 mtime，7 天）。
func auditRetention(_ context.Context) error {
	cutoff := time.Now().Add(-terminalAuditRetain)
	return filepath.WalkDir(terminalAuditDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil // 目录不存在或读失败：下轮再试
		}
		info, err := d.Info()
		if err != nil || info.ModTime().After(cutoff) {
			return nil
		}
		return os.Remove(path)
	})
}

func retentionWindow() time.Duration {
	return time.Duration(ringSize) * 15 * time.Second
}
