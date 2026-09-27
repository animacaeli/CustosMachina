package cron

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/robfig/cron/v3"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/resources"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

var ErrNotFound = errors.New("任务或脚本不存在")

// Runner 跨模块 SSH 能力（resources.Service 提供实现，wire.Bind 装配）。
type Runner interface {
	RunCommandOn(ctx context.Context, serverID uint, cmd, stdin string, timeout time.Duration) (string, error)
	// RunCommandStreamOn 输出增量回调（手动执行的实时日志）
	RunCommandStreamOn(ctx context.Context, serverID uint, cmd, stdin string, timeout time.Duration, onChunk func(string)) (string, error)
	RecordEvent(ctx context.Context, serverID uint, typ, msg string)
}

// OpsNotifier 运维群推送出口（notify.Service 实现，app 层注入）：
// 任务失败/超时推运维群——定时任务半夜失败不能等第二天才发现。
type OpsNotifier interface {
	NotifyOps(ctx context.Context, title, detail string)
}

// DomainResolver 项目基础名 → 当前活跃隔离域名（release.Service 提供，
// app 层 SetDomainResolver 事后注入）。蓝绿项目的 compose 载体任务跟随
// 活跃颜色域执行（同版本同网络）；未启用蓝绿时返回原名。
type DomainResolver interface {
	ActiveDomainFor(ctx context.Context, baseName string) string
}

type Service struct {
	db       *gorm.DB
	ssh      Runner
	domains  DomainResolver // 可空：未注入时按原名执行
	notifier OpsNotifier    // 可空：未注入时只落库不推送
}

// SetNotifier 注入运维群推送出口（notify.Service 提供）。
func (s *Service) SetNotifier(n OpsNotifier) { s.notifier = n }

func NewService(db *gorm.DB, ssh Runner) *Service { return &Service{db: db, ssh: ssh} }

// SetDomainResolver 注入域名解析器（release.Service 提供，避免构造环）。
func (s *Service) SetDomainResolver(r DomainResolver) { s.domains = r }

// composeDomain compose 载体的实际隔离域名：蓝绿项目解析为活跃颜色域。
func (s *Service) composeDomain(ctx context.Context, job *CronJob) string {
	if s.domains == nil {
		return job.ProjectName
	}
	return s.domains.ActiveDomainFor(ctx, job.ProjectName)
}

// ---- 脚本库 CRUD ----

type ScriptInput struct {
	Name    string `json:"name" binding:"required,max=128"`
	Project string `json:"project" binding:"omitempty,max=64"` // 所属项目（筛选用，可选）
	Type    string `json:"type" binding:"required,oneof=shell python compose-run"`
	Content string `json:"content" binding:"max=65536"`
	Remark  string `json:"remark" binding:"max=255"`
}

// ScriptWithBound 脚本 + 引用计数（编辑时提示"修改立即生效于 N 个任务"）。
type ScriptWithBound struct {
	CronScript
	BoundCount int64 `json:"boundCount"`
}

func (s *Service) ListScripts(ctx context.Context) ([]ScriptWithBound, error) {
	var list []CronScript
	if err := s.db.WithContext(ctx).Order("id DESC").Find(&list).Error; err != nil {
		return nil, err
	}
	type cnt struct {
		ScriptID uint
		N        int64
	}
	var counts []cnt
	s.db.WithContext(ctx).Model(&CronJob{}).
		Select("script_id, COUNT(*) AS n").Group("script_id").Scan(&counts)
	bound := map[uint]int64{}
	for _, c := range counts {
		bound[c.ScriptID] = c.N
	}
	out := make([]ScriptWithBound, 0, len(list))
	for _, sc := range list {
		out = append(out, ScriptWithBound{CronScript: sc, BoundCount: bound[sc.ID]})
	}
	return out, nil
}

func (s *Service) SaveScript(ctx context.Context, id uint, in ScriptInput, operator string) (*CronScript, error) {
	var sc *CronScript
	if id == 0 {
		sc = &CronScript{CreatedBy: operator}
	} else {
		if err := s.db.WithContext(ctx).First(&sc, id).Error; err != nil {
			return nil, ErrNotFound
		}
	}
	sc.Name, sc.Type, sc.Content, sc.Remark, sc.Project = in.Name, in.Type, in.Content, in.Remark, in.Project
	if err := s.db.WithContext(ctx).Save(sc).Error; err != nil {
		return nil, err
	}
	return sc, nil
}

func (s *Service) DeleteScript(ctx context.Context, id uint) error {
	// 有任务绑定的脚本不允许删（先解绑），避免任务悬空
	var n int64
	if err := s.db.WithContext(ctx).Model(&CronJob{}).Where("script_id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("仍有 %d 个任务绑定该脚本，请先删除或改绑任务", n)
	}
	return s.db.WithContext(ctx).Delete(&CronScript{}, id).Error
}

// ---- 任务 CRUD ----

var (
	// 镜像名首字符必须字母数字：拒绝前导 -（--privileged 等 docker run 标志注入）
	imageRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9/._:@-]*$`)
	svcRe   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
	// 附加参数白名单：字母数字与安全的分隔符，杜绝 shell 元字符（;|&$` 等会
	// 在宿主机 sh -c 里执行，绕过"执行=一次性容器"的沙箱模型）
	commandRe = regexp.MustCompile(`^[a-zA-Z0-9 =:/_.,@%+-]+$`)
)

// parseSchedule 用 robfig/cron 的解析器解析标准 5 段 crontab 表达式并算下次触发
// 时间（只用解析器，不用它的调度器：调度权威保持是本模块的扫描型 Job，见
// scheduler.go）。只收 5 段式（分 时 日 月 周）：@every/@daily 等描述符不收，
// 与系统 crontab 语义一致，降低使用者的心智分叉。
func parseSchedule(expr string, from time.Time) (time.Time, error) {
	if strings.HasPrefix(strings.TrimSpace(expr), "@") {
		return time.Time{}, fmt.Errorf("只支持标准 5 段 crontab（分 时 日 月 周），不支持 @ 描述符")
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("crontab 表达式不合法（标准 5 段：分 时 日 月 周，如 0 3 * * *）: %w", err)
	}
	return sched.Next(from), nil
}

// SchedulePreview 计算表达式自 now 起的未来 count 次触发时间（表单预览用）。
func (s *Service) SchedulePreview(expr string, count int) ([]time.Time, error) {
	if strings.HasPrefix(strings.TrimSpace(expr), "@") {
		return nil, fmt.Errorf("只支持标准 5 段 crontab（分 时 日 月 周），不支持 @ 描述符")
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return nil, fmt.Errorf("crontab 表达式不合法（标准 5 段：分 时 日 月 周，如 0 3 * * *）: %w", err)
	}
	if count <= 0 || count > 20 {
		count = 5
	}
	times := make([]time.Time, 0, count)
	next := time.Now()
	for len(times) < count {
		next = sched.Next(next)
		times = append(times, next)
	}
	return times, nil
}

// validateCarrier 按计划的"脚本类型 × 执行载体"矩阵校验合法组合。
func validateCarrier(scriptType, carrier, image, project, service string) error {
	switch scriptType {
	case ScriptShell, ScriptPython:
		if carrier == CarrierCompose {
			// shell/python 走 compose run 也合法（跑在项目服务环境内）
			if project == "" || service == "" {
				return fmt.Errorf("compose 载体需填写项目名与服务名")
			}
			return nil
		}
		if carrier != CarrierRun {
			return fmt.Errorf("未知载体 %q", carrier)
		}
		if image == "" {
			return fmt.Errorf("%s 脚本走 docker run 需绑定镜像（%s 须含 %s）",
				scriptType, image, map[string]string{ScriptShell: "sh", ScriptPython: "python3"}[scriptType])
		}
		if !imageRe.MatchString(image) {
			return fmt.Errorf("镜像名含非法字符")
		}
	case ScriptComposeRun:
		if carrier != CarrierCompose {
			return fmt.Errorf("compose-run 脚本只能搭配 compose 载体")
		}
		if project == "" || service == "" {
			return fmt.Errorf("compose 载体需填写项目名与服务名")
		}
	default:
		return fmt.Errorf("未知脚本类型 %q", scriptType)
	}
	if project != "" && !svcRe.MatchString(project) {
		return fmt.Errorf("项目名含非法字符")
	}
	if service != "" && !svcRe.MatchString(service) {
		return fmt.Errorf("服务名含非法字符")
	}
	return nil
}

type JobInput struct {
	Name        string `json:"name" binding:"required,max=128"`
	ScriptID    uint   `json:"scriptId" binding:"required"`
	Schedule    string `json:"schedule" binding:"omitempty,max=64"` // 空 = 仅手动执行（一次性脚本）
	ServerID    uint   `json:"serverId"`                            // run 载体必填；compose 载体留空 = 按项目正式环境部署目标解析
	Carrier     string `json:"carrier" binding:"required,oneof=run compose-run"`
	Image       string `json:"image" binding:"omitempty,max=255"`
	ProjectName string `json:"projectName" binding:"omitempty,max=64"`
	Service     string `json:"service" binding:"omitempty,max=64"`
	Command     string `json:"command" binding:"omitempty,max=512"`
	Network     string `json:"network" binding:"omitempty,max=64"` // carrier=run 可选：docker --network
	TimeoutSecs int    `json:"timeoutSecs" binding:"omitempty,min=10,max=86400"`
	Retry       int    `json:"retry" binding:"omitempty,min=0,max=3"` // 失败重试次数
	Enabled     *bool  `json:"enabled"`
}

func (s *Service) SaveJob(ctx context.Context, id uint, in JobInput, operator string) (*CronJob, error) {
	var sc CronScript
	if err := s.db.WithContext(ctx).First(&sc, in.ScriptID).Error; err != nil {
		return nil, fmt.Errorf("脚本不存在")
	}
	if err := validateCarrier(sc.Type, in.Carrier, in.Image, in.ProjectName, in.Service); err != nil {
		return nil, err
	}
	if in.Schedule != "" {
		if _, err := parseSchedule(in.Schedule, time.Now()); err != nil {
			return nil, err
		}
	}
	if in.Carrier == CarrierRun && in.ServerID == 0 {
		return nil, fmt.Errorf("run 载体需指定目标主机（compose 载体留空则按项目部署目标执行）")
	}
	if in.Network != "" && !svcRe.MatchString(in.Network) {
		return nil, fmt.Errorf("网络名含非法字符")
	}
	if in.Command != "" && !commandRe.MatchString(in.Command) {
		return nil, fmt.Errorf("附加参数含非法字符（只允许字母数字与 =:/_.,@%%+- 和空格）")
	}

	var job *CronJob
	if id == 0 {
		job = &CronJob{CreatedBy: operator, TimeoutSecs: 600}
	} else {
		if err := s.db.WithContext(ctx).First(&job, id).Error; err != nil {
			return nil, ErrNotFound
		}
	}
	job.Name, job.ScriptID, job.Schedule, job.ServerID = in.Name, in.ScriptID, in.Schedule, in.ServerID
	job.Carrier, job.Image, job.ProjectName, job.Service = in.Carrier, in.Image, in.ProjectName, in.Service
	job.Command, job.Network = in.Command, in.Network
	if in.TimeoutSecs > 0 {
		job.TimeoutSecs = in.TimeoutSecs
	}
	job.Retry = in.Retry
	if in.Enabled != nil {
		job.Enabled = *in.Enabled
	}
	// 无表达式 = 仅手动任务：不推进 next_run_at（调度器按非空表达式排除）
	if job.Enabled && job.Schedule != "" {
		next, err := parseSchedule(job.Schedule, time.Now())
		if err != nil {
			return nil, err
		}
		job.NextRunAt = &next
	} else {
		job.NextRunAt = nil
	}
	if id == 0 {
		// GORM 坑：Create 跳过带 default 标签的零值字段且会把 default 回填进结构体
		// ——enabled=false 落库吃 default true 且 job.Enabled 被改回 true（禁用任务
		// 变启用、next_run_at 为 NULL 被调度器立即扫到，真机测试实测踩中）。
		// 先记期望值，创建后按期望回写。
		wantEnabled := job.Enabled
		if err := s.db.WithContext(ctx).Create(job).Error; err != nil {
			return nil, err
		}
		if job.Enabled != wantEnabled {
			if err := s.db.WithContext(ctx).Model(job).
				Update("enabled", wantEnabled).Error; err != nil {
				return nil, err
			}
			job.Enabled = wantEnabled
		}
	} else if err := s.db.WithContext(ctx).Save(job).Error; err != nil {
		return nil, err
	}
	return job, nil
}

func (s *Service) DeleteJob(ctx context.Context, id uint) error {
	var running int64
	if err := s.db.WithContext(ctx).Model(&CronRun{}).
		Where("job_id = ? AND status = ?", id, RunRunning).Count(&running).Error; err == nil && running > 0 {
		return fmt.Errorf("任务正在执行中，请等待结束后再删除")
	}
	return s.db.WithContext(ctx).Delete(&CronJob{}, id).Error
}

// ListJobs 附带脚本名（前端列表直接渲染）。
func (s *Service) ListJobs(ctx context.Context) ([]map[string]any, error) {
	var jobs []CronJob
	if err := s.db.WithContext(ctx).Order("id DESC").Find(&jobs).Error; err != nil {
		return nil, err
	}
	scriptIDs := map[uint]struct{}{}
	for _, j := range jobs {
		scriptIDs[j.ScriptID] = struct{}{}
	}
	names := map[uint]string{}
	if len(scriptIDs) > 0 {
		ids := make([]uint, 0, len(scriptIDs))
		for id := range scriptIDs {
			ids = append(ids, id)
		}
		var scripts []CronScript
		if err := s.db.WithContext(ctx).Select("id, name, type").Where("id IN ?", ids).Find(&scripts).Error; err != nil {
			return nil, err
		}
		for _, sc := range scripts {
			names[sc.ID] = sc.Name
		}
	}
	out := make([]map[string]any, 0, len(jobs))
	for _, j := range jobs {
		item := map[string]any{
			"job": j, "scriptName": names[j.ScriptID],
		}
		out = append(out, item)
	}
	return out, nil
}

// ---- 运行历史 ----

func (s *Service) ListRuns(ctx context.Context, jobID uint, page, size int) ([]CronRun, int64, error) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 100 {
		size = 20
	}
	q := s.db.WithContext(ctx).Model(&CronRun{})
	if jobID > 0 {
		q = q.Where("job_id = ?", jobID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var runs []CronRun
	err := q.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&runs).Error
	return runs, total, err
}

// ---- 触发与执行（手动触发与调度共用一条链路）----

// Trigger 手动立即执行一次；Forbid：上次（任意触发）未结束时拒绝而不是排队。
func (s *Service) Trigger(ctx context.Context, jobID uint) (*CronRun, error) {
	var job CronJob
	if err := s.db.WithContext(ctx).First(&job, jobID).Error; err != nil {
		return nil, ErrNotFound
	}
	serverID, err := s.resolveServer(ctx, &job)
	if err != nil {
		return nil, err
	}
	job.ServerID = serverID // 内存内填充（不回写：部署目标变更后下次执行重新解析）
	run, ok := s.startRun(&job, TriggerManual)
	if !ok {
		return nil, fmt.Errorf("上一次执行仍在进行中（Forbid），请稍后再试")
	}
	go s.execute(context.WithoutCancel(ctx), &job, run)
	return run, nil
}

// runMu 串行化 startRun：Count+Create 必须原子，否则手动触发与调度并发时
// 双方都数到 0 各起一条 running（Forbid 失效双跑）。进程内锁足够（平台单实例，
// 与 pkg/jobs 的假设一致）。
var runMu sync.Mutex

// resolveServer 解析任务的执行主机：显式绑定 > 项目正式环境部署目标
// （compose 载体的业务任务与项目同机——用户反馈：任务应与项目绑定，
// 只有备份/清理类主机任务才需要手动选主机）。查表先例同 release.envTarget。
func (s *Service) resolveServer(ctx context.Context, job *CronJob) (uint, error) {
	if job.ServerID > 0 {
		return job.ServerID, nil
	}
	if job.Carrier != CarrierCompose || job.ProjectName == "" {
		return 0, fmt.Errorf("任务未绑定执行主机（run 载体须选主机，compose 载体须填项目名）")
	}
	domain := s.composeDomain(ctx, job) // 蓝绿项目解析活跃色后取项目基础名
	base := strings.TrimSuffix(strings.TrimSuffix(domain, "-blue"), "-green")
	var pid uint
	if err := s.db.WithContext(ctx).Table("projects").
		Select("id").Where("name IN ?", []string{base, job.ProjectName}).
		Scan(&pid).Error; err != nil || pid == 0 {
		return 0, fmt.Errorf("项目 %q 不存在，无法解析执行主机", job.ProjectName)
	}
	var sid uint
	if err := s.db.WithContext(ctx).Table("project_env_targets").
		Select("server_id").Where("project_id = ? AND env_type = ?", pid, "prod").
		Scan(&sid).Error; err != nil || sid == 0 {
		return 0, fmt.Errorf("项目 %q 未配置正式环境部署目标", job.ProjectName)
	}
	return sid, nil
}

// startRun 落一条 running 记录；并发策略 Forbid：已有 running 则拒绝。
func (s *Service) startRun(job *CronJob, trigger string) (*CronRun, bool) {
	runMu.Lock()
	defer runMu.Unlock()
	var running int64
	if err := s.db.Model(&CronRun{}).Where("job_id = ? AND status = ?", job.ID, RunRunning).Count(&running).Error; err != nil {
		logger.Warnf("[cron] 查询运行中记录失败 job=%d: %v", job.ID, err)
		return nil, false // 查不动就不跑：宁可漏跑不可双跑
	}
	if running > 0 {
		return nil, false
	}
	now := time.Now()
	run := &CronRun{JobID: job.ID, ServerID: job.ServerID, Trigger: trigger, Status: RunRunning, StartedAt: now}
	if err := s.db.Create(run).Error; err != nil {
		logger.Warnf("[cron] 创建运行记录失败 job=%d: %v", job.ID, err)
		return nil, false
	}
	job.LastRunAt, job.LastStatus = &now, RunRunning
	s.db.Model(&CronJob{}).Where("id = ?", job.ID).
		Updates(map[string]any{"last_run_at": now, "last_status": RunRunning})
	return run, true
}

func (s *Service) finishRun(run *CronRun, status, output string, start time.Time) {
	output = truncateUTF8(output, 64*1024) // 所有路径统一截断（成功/失败/超时）
	now := time.Now()
	run.Status, run.Output, run.FinishedAt = status, output, &now
	run.DurationSecs = int(now.Sub(start).Seconds())
	s.db.Model(run).Updates(map[string]any{
		"status": status, "output": output, "finished_at": now,
		"duration_secs": run.DurationSecs,
	})
	s.db.Model(&CronJob{}).Where("id = ?", run.JobID).Update("last_status", status)
}

// buildCommand 按载体拼目标机 shell 命令（hostScriptPath 已由调用方落好脚本文件；
// domain 为 compose 载体的实际隔离域名——蓝绿项目已解析为活跃颜色域）。
func buildCommand(job *CronJob, script *CronScript, hostScriptPath, runLabel, domain string) string {
	var interpreter string
	switch script.Type {
	case ScriptPython:
		interpreter = "python3"
	default:
		interpreter = "sh"
	}
	extra := ""
	if job.Command != "" {
		extra = " " + job.Command
	}
	if job.Carrier == CarrierCompose {
		// docker compose run：项目 compose 文件在部署固定目录（resources.DeployComposeTo 约定）
		composeFile := resources.ComposeFileFor(domain)
		if hostScriptPath != "" {
			return fmt.Sprintf(
				`docker compose -p %s -f %s run --rm %s -v %s:/tmp/cron-task:ro %s %s /tmp/cron-task%s`,
				domain, composeFile, runLabel, hostScriptPath, job.Service, interpreter, extra)
		}
		return fmt.Sprintf(`docker compose -p %s -f %s run --rm %s %s%s`,
			domain, composeFile, runLabel, job.Service, extra)
	}
	// docker run：脚本以只读卷挂进一次性容器；可选 --network 连业务网络（查数据用）
	netFlag := ""
	if job.Network != "" {
		netFlag = "--network " + job.Network
	}
	return fmt.Sprintf(`docker run --rm %s %s -v %s:/tmp/cron-task:ro %s %s /tmp/cron-task%s`,
		runLabel, netFlag, hostScriptPath, job.Image, interpreter, extra)
}

func shellQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }

// truncateUTF8 按字节截断但回退到 rune 边界（防止切碎多字节字符出非法 UTF-8）。
func truncateRunes(s string, n int) string { return truncateUTF8(s, n) }

func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n-- // 截点落在多字节字符中间：回退到 rune 起点
	}
	return s[:n] + "\n...（输出超 64KB 已截断）"
}

// execute 上传脚本 → 起一次性容器 → 超时强杀 → 全量输出落文件 → 落结果、
// 失败推运维群、按 job.Retry 链式重试（间隔 5 分钟，Trigger=retry）。
func (s *Service) execute(ctx context.Context, job *CronJob, run *CronRun) {
	s.executeWithRetry(ctx, job, run, 0)
}

const retryDelay = 5 * time.Minute

func (s *Service) executeWithRetry(ctx context.Context, job *CronJob, run *CronRun, attempt int) {
	start := time.Now()
	var script CronScript
	if err := s.db.First(&script, job.ScriptID).Error; err != nil {
		s.finishAndMaybeRetry(ctx, job, run, attempt, RunFailed, "脚本不存在（可能已被删除）", start, "")
		return
	}
	timeout := time.Duration(job.TimeoutSecs) * time.Second
	killCmd := fmt.Sprintf(`docker ps -q --filter label=custos.cron.run=%d | xargs -r docker kill`, run.ID)
	runLabel := fmt.Sprintf("--label custos.cron.run=%d", run.ID)

	hostScriptPath := ""
	if script.Type == ScriptShell || script.Type == ScriptPython {
		ext := map[string]string{ScriptShell: "sh", ScriptPython: "py"}[script.Type]
		hostScriptPath = fmt.Sprintf("/opt/custos-machina/cron/task-%d.%s", run.ID, ext)
		mkdir := fmt.Sprintf("mkdir -p /opt/custos-machina/cron && cat > %s && chmod 644 %s", hostScriptPath, hostScriptPath)
		if out, err := s.ssh.RunCommandOn(ctx, job.ServerID, mkdir, script.Content, 30*time.Second); err != nil {
			s.finishAndMaybeRetry(ctx, job, run, attempt, RunFailed, fmt.Sprintf("上传脚本失败: %v\n%s", err, out), start, "")
			s.audit(ctx, job, run, false)
			return
		}
	}

	cmd := buildCommand(job, &script, hostScriptPath, runLabel, s.composeDomain(ctx, job))
	if hostScriptPath != "" {
		// 执行完顺手清理脚本文件（不因清理失败判任务失败）
		cmd = fmt.Sprintf(`sh -c %s`, shellQuote(cmd+fmt.Sprintf("; rc=$?; rm -f %s; exit $rc", hostScriptPath)))
	}
	// 流式执行：输出增量落库（节流 1s），手动触发后前端轮询即可近实时看到日志
	var live strings.Builder
	lastFlush := time.Now()
	onChunk := func(chunk string) {
		live.WriteString(chunk)
		if time.Since(lastFlush) >= time.Second {
			lastFlush = time.Now()
			s.db.Model(&CronRun{}).Where("id = ?", run.ID).
				Update("output", truncateUTF8(live.String(), 64*1024))
		}
	}
	out, err := s.ssh.RunCommandStreamOn(ctx, job.ServerID, cmd, "", timeout, onChunk)
	if err != nil {
		if errors.Is(err, resources.ErrCommandTimeout) {
			// SSH 会话已杀，容器可能还在跑：按运行标签强杀
			if killOut, killErr := s.ssh.RunCommandOn(ctx, job.ServerID, killCmd, "", 30*time.Second); killErr != nil {
				out += fmt.Sprintf("\n[超时强杀失败: %v\n%s]", killErr, killOut)
			}
			// 超时时 sh -c 尾部的 rm 没执行到：补删宿主脚本文件（含敏感内容的脚本不能留）
			if hostScriptPath != "" {
				_, _ = s.ssh.RunCommandOn(ctx, job.ServerID, "rm -f "+hostScriptPath, "", 15*time.Second)
			}
			s.finishAndMaybeRetry(ctx, job, run, attempt, RunTimeout, fmt.Sprintf("执行超时（%s），容器已强制终止\n%s", timeout, out), start, out)
			s.audit(ctx, job, run, false)
			return
		}
		s.finishAndMaybeRetry(ctx, job, run, attempt, RunFailed, fmt.Sprintf("%v\n%s", err, out), start, out)
		s.audit(ctx, job, run, false)
		return
	}
	s.finishAndMaybeRetry(ctx, job, run, attempt, RunSuccess, out, start, out)
	s.audit(ctx, job, run, true)
}

// finishAndMaybeRetry 落结果 + 全量输出落目标机文件 + 失败推运维群 + 重试链。
func (s *Service) finishAndMaybeRetry(ctx context.Context, job *CronJob, run *CronRun, attempt int, status, output string, start time.Time, fullOut string) {
	outputFile := s.writeFullOutput(ctx, job, run, fullOut)
	s.finishRun(run, status, output, start)
	if outputFile != "" {
		s.db.Model(run).Update("output_file", outputFile)
		run.OutputFile = outputFile
	}
	if status == RunFailed || status == RunTimeout {
		// 失败推运维群（半夜失败不能等第二天）
		if s.notifier != nil {
			title := fmt.Sprintf("定时任务失败：%s", job.Name)
			detail := fmt.Sprintf("状态：%s（第 %d 次尝试）\n触发：%s\n服务器 ID：%d\n\n%s",
				status, attempt+1, run.Trigger, job.ServerID, truncateRunes(output, 500))
			go func() { s.notifier.NotifyOps(context.WithoutCancel(ctx), title, detail) }()
		}
		// 重试链：间隔 5 分钟（Forbid 语义不变——重试前若有新调度触发会被它顶掉）
		if attempt < job.Retry {
			next := attempt + 1
			logger.Infof("[cron] 任务 %s 失败，%s 后第 %d/%d 次重试", job.Name, retryDelay, next, job.Retry)
			time.AfterFunc(retryDelay, func() {
				var j CronJob
				if err := s.db.First(&j, job.ID).Error; err != nil {
					return // 任务已删：不再重试
				}
				if r2, ok := s.startRun(&j, TriggerRetry); ok {
					s.executeWithRetry(context.Background(), &j, r2, next)
				}
			})
		}
	}
}

// writeFullOutput 全量输出落目标机（截断留痕的兜底：前端可下载完整日志）。
func (s *Service) writeFullOutput(ctx context.Context, job *CronJob, run *CronRun, out string) string {
	if strings.TrimSpace(out) == "" {
		return ""
	}
	path := fmt.Sprintf("/opt/custos-machina/cron/output-%d.log", run.ID)
	cmd := fmt.Sprintf("mkdir -p /opt/custos-machina/cron && cat > %s", path)
	if _, err := s.ssh.RunCommandOn(ctx, job.ServerID, cmd, out, 30*time.Second); err != nil {
		return ""
	}
	return path
}

func (s *Service) audit(ctx context.Context, job *CronJob, run *CronRun, success bool) {
	s.ssh.RecordEvent(context.WithoutCancel(ctx), job.ServerID, "cron_run",
		fmt.Sprintf("定时任务 %s（#%d）执行%s：%s", job.Name, run.ID,
			map[bool]string{true: "成功", false: "失败"}[success], run.Status))
}
