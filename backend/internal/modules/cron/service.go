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
	RecordEvent(ctx context.Context, serverID uint, typ, msg string)
}

type Service struct {
	db  *gorm.DB
	ssh Runner
}

func NewService(db *gorm.DB, ssh Runner) *Service { return &Service{db: db, ssh: ssh} }

// ---- 脚本库 CRUD ----

type ScriptInput struct {
	Name    string `json:"name" binding:"required,max=128"`
	Type    string `json:"type" binding:"required,oneof=shell python compose-run"`
	Content string `json:"content" binding:"max=65536"`
	Remark  string `json:"remark" binding:"max=255"`
}

func (s *Service) ListScripts(ctx context.Context) ([]CronScript, error) {
	var list []CronScript
	err := s.db.WithContext(ctx).Order("id DESC").Find(&list).Error
	return list, err
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
	sc.Name, sc.Type, sc.Content, sc.Remark = in.Name, in.Type, in.Content, in.Remark
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
	// @every 支持多段时长（@every 1h30m）；至少 1 分钟在 parseSchedule 里判
	scheduleRe = regexp.MustCompile(`^@every\s+(\d+[smhd])+$`)
	// 镜像名首字符必须字母数字：拒绝前导 -（--privileged 等 docker run 标志注入）
	imageRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9/._:@-]*$`)
	svcRe   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
	// 附加参数白名单：字母数字与安全的分隔符，杜绝 shell 元字符（;|&$` 等会
	// 在宿主机 sh -c 里执行，绕过"执行=一次性容器"的沙箱模型）
	commandRe = regexp.MustCompile(`^[a-zA-Z0-9 =:/_.,@%+-]+$`)
)

// parseSchedule 用 robfig/cron 的解析器算下次触发时间（只用解析器，不用它的调度器：
// 调度权威保持是本模块的扫描型 Job，见 scheduler.go）。
func parseSchedule(expr string, from time.Time) (time.Time, error) {
	if strings.HasPrefix(expr, "@every") {
		if !scheduleRe.MatchString(expr) {
			return time.Time{}, fmt.Errorf("不支持的 @every 格式（示例 @every 30m / @every 1h30m，最小 1 分钟）")
		}
		d, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(expr, "@every")))
		if err != nil || d < time.Minute {
			return time.Time{}, fmt.Errorf("@every 间隔须 ≥ 1 分钟")
		}
		return from.Add(d), nil
	}
	sched, err := cron.ParseStandard(expr)
	if err != nil {
		return time.Time{}, fmt.Errorf("cron 表达式不合法（5 段式或 @every）: %w", err)
	}
	return sched.Next(from), nil
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
	Schedule    string `json:"schedule" binding:"required,max=64"`
	ServerID    uint   `json:"serverId" binding:"required"`
	Carrier     string `json:"carrier" binding:"required,oneof=run compose-run"`
	Image       string `json:"image" binding:"omitempty,max=255"`
	ProjectName string `json:"projectName" binding:"omitempty,max=64"`
	Service     string `json:"service" binding:"omitempty,max=64"`
	Command     string `json:"command" binding:"omitempty,max=512"`
	TimeoutSecs int    `json:"timeoutSecs" binding:"omitempty,min=10,max=86400"`
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
	if _, err := parseSchedule(in.Schedule, time.Now()); err != nil {
		return nil, err
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
	job.Command = in.Command
	if in.TimeoutSecs > 0 {
		job.TimeoutSecs = in.TimeoutSecs
	}
	if in.Enabled != nil {
		job.Enabled = *in.Enabled
	}
	if job.Enabled {
		next, err := parseSchedule(job.Schedule, time.Now())
		if err != nil {
			return nil, err
		}
		job.NextRunAt = &next
	} else {
		job.NextRunAt = nil
	}
	if err := s.db.WithContext(ctx).Save(job).Error; err != nil {
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
	run := &CronRun{JobID: job.ID, Trigger: trigger, Status: RunRunning, StartedAt: now}
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

// buildCommand 按载体拼目标机 shell 命令（hostScriptPath 已由调用方落好脚本文件）。
func buildCommand(job *CronJob, script *CronScript, hostScriptPath, runLabel string) string {
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
		composeFile := resources.ComposeFileFor(job.ProjectName)
		if hostScriptPath != "" {
			return fmt.Sprintf(
				`docker compose -p %s -f %s run --rm %s -v %s:/tmp/cron-task:ro %s %s /tmp/cron-task%s`,
				job.ProjectName, composeFile, runLabel, hostScriptPath, job.Service, interpreter, extra)
		}
		return fmt.Sprintf(`docker compose -p %s -f %s run --rm %s %s%s`,
			job.ProjectName, composeFile, runLabel, job.Service, extra)
	}
	// docker run：脚本以只读卷挂进一次性容器
	return fmt.Sprintf(`docker run --rm %s -v %s:/tmp/cron-task:ro %s %s /tmp/cron-task%s`,
		runLabel, hostScriptPath, job.Image, interpreter, extra)
}

func shellQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'" }

// truncateUTF8 按字节截断但回退到 rune 边界（防止切碎多字节字符出非法 UTF-8）。
func truncateUTF8(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n-- // 截点落在多字节字符中间：回退到 rune 起点
	}
	return s[:n] + "\n...（输出超 64KB 已截断）"
}

// execute 上传脚本 → 起一次性容器 → 超时强杀 → 落结果与审计。
func (s *Service) execute(ctx context.Context, job *CronJob, run *CronRun) {
	start := time.Now()
	var script CronScript
	if err := s.db.First(&script, job.ScriptID).Error; err != nil {
		s.finishRun(run, RunFailed, "脚本不存在（可能已被删除）", start)
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
			s.finishRun(run, RunFailed, fmt.Sprintf("上传脚本失败: %v\n%s", err, out), start)
			s.audit(ctx, job, run, false)
			return
		}
	}

	cmd := buildCommand(job, &script, hostScriptPath, runLabel)
	if hostScriptPath != "" {
		// 执行完顺手清理脚本文件（不因清理失败判任务失败）
		cmd = fmt.Sprintf(`sh -c %s`, shellQuote(cmd+fmt.Sprintf("; rc=$?; rm -f %s; exit $rc", hostScriptPath)))
	}
	out, err := s.ssh.RunCommandOn(ctx, job.ServerID, cmd, "", timeout)
	if err != nil {
		if errors.Is(err, resources.ErrCommandTimeout) {
			// SSH 会话已杀，容器可能还在跑：按运行标签强杀
			if killOut, killErr := s.ssh.RunCommandOn(ctx, job.ServerID, killCmd, "", 30*time.Second); killErr != nil {
				out += fmt.Sprintf("\n[超时强杀失败: %v\n%s]", killErr, killOut)
			}
			s.finishRun(run, RunTimeout, fmt.Sprintf("执行超时（%s），容器已强制终止\n%s", timeout, out), start)
			s.audit(ctx, job, run, false)
			return
		}
		s.finishRun(run, RunFailed, fmt.Sprintf("%v\n%s", err, out), start)
		s.audit(ctx, job, run, false)
		return
	}
	s.finishRun(run, RunSuccess, out, start)
	s.audit(ctx, job, run, true)
}

func (s *Service) audit(ctx context.Context, job *CronJob, run *CronRun, success bool) {
	s.ssh.RecordEvent(context.WithoutCancel(ctx), job.ServerID, "cron_run",
		fmt.Sprintf("定时任务 %s（#%d）执行%s：%s", job.Name, run.ID,
			map[bool]string{true: "成功", false: "失败"}[success], run.Status))
}
