package cron

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/custos-machina/backend/internal/pkg/jobs"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

// missedGrace 错失宽限：next_run_at 落后现在超过该值视为"平台宕机期间错过"，
// 记 missed（状态 skipped、输出注明）不补跑；宽限内的当轮正常分发
// （30s 扫描间隔下最多晚一个周期）。
const missedGrace = 10 * time.Minute

// Scheduler 扫描型调度 Job（占位主类型，便于 wire 聚合 cleanup）。
// 不改造 pkg/jobs 框架：一个 30s 间隔的扫描轮询查到点任务，
// 任务增删改不需要重启调度 goroutine。
type Scheduler struct{}

// NewScheduler 注册扫描任务；启动前先把宕机遗留的 running 记录标记为 unknown，
// 并按运行标签清扫目标机上可能残留的一次性容器与脚本文件。
func NewScheduler(svc *Service) (*Scheduler, func(), error) {
	svc.recoverDangling()
	g := jobs.NewGroup(jobs.Job{
		Name: "cron:sched", Interval: 30 * time.Second, Fn: svc.scanDue,
	})
	g.Start()
	return &Scheduler{}, g.Stop, nil
}

// recoverDangling 平台重启恢复：上次进程发起、结果未知的 running 记录标 unknown
// （不自动重跑，计划的错失策略），并尽力清扫目标机上残留的一次性容器与脚本文件
// （SSH 会话断开后 docker run 的 --rm 清理不会触发，容器会一直跑到自然退出）。
func (s *Service) recoverDangling() {
	var dangling []CronRun
	if err := s.db.Where("status = ?", RunRunning).Find(&dangling).Error; err != nil {
		logger.Warnf("[cron] 查询悬空运行记录失败: %v", err)
		return
	}
	if len(dangling) == 0 {
		return
	}
	res := s.db.Model(&CronRun{}).Where("status = ?", RunRunning).
		Updates(map[string]any{"status": RunUnknown, "output": "平台重启，执行结果未知（不自动重跑）"})
	if res.Error != nil {
		logger.Warnf("[cron] 标记悬空运行记录失败: %v", res.Error)
		return
	}
	logger.Infof("[cron] %d 条运行中记录因平台重启标记为 unknown", len(dangling))
	// 按 job 的目标机分组，每台一条命令清容器 + 清脚本（best-effort，失败只记日志）
	servers := map[uint][]uint{}
	for _, r := range dangling {
		var job CronJob
		if err := s.db.Select("server_id").First(&job, r.JobID).Error; err != nil {
			continue // job 已删：容器无标签可查，留给目标机自然回收
		}
		servers[job.ServerID] = append(servers[job.ServerID], r.ID)
	}
	ctx := context.Background()
	for serverID, runIDs := range servers {
		var parts []string
		for _, id := range runIDs {
			parts = append(parts,
				fmt.Sprintf("docker ps -aq --filter label=custos.cron.run=%d | xargs -r docker kill", id),
				fmt.Sprintf("rm -f /opt/custos-machina/cron/task-%d.sh /opt/custos-machina/cron/task-%d.py", id, id))
		}
		if out, err := s.ssh.RunCommandOn(ctx, serverID, strings.Join(parts, "; "), "", time.Minute); err != nil {
			logger.Warnf("[cron] 孤儿容器清扫失败 server=%d: %v\n%s", serverID, err, out)
		}
	}
}

// scanDue 每轮：查启用且到点的任务 → Forbid 判定 → 分发并推进 next_run_at。
func (s *Service) scanDue(ctx context.Context) error {
	now := time.Now()
	var jobs []CronJob
	// 启用且（无 next_run_at 或已到点）：新建任务保存时已写入 next_run_at，
	// next_run_at 为空只可能是旧数据/异常，同样纳入扫描由推进逻辑修正
	if err := s.db.Where("enabled = ? AND (next_run_at IS NULL OR next_run_at <= ?)", true, now).
		Find(&jobs).Error; err != nil {
		return err
	}
	for i := range jobs {
		job := &jobs[i]
		next, err := parseSchedule(job.Schedule, now)
		if err != nil {
			// 表达式被改坏（不经过 API 的极端情况）：停用任务并留痕
			s.db.Model(job).Updates(map[string]any{"enabled": false, "next_run_at": nil})
			logger.Warnf("[cron] 任务 %d 调度表达式非法，已停用: %v", job.ID, err)
			continue
		}
		// 错失判定：落后超过宽限 → 记 missed（skipped），不执行
		if job.NextRunAt != nil && now.Sub(*job.NextRunAt) > missedGrace {
			s.db.Create(&CronRun{
				JobID: job.ID, Trigger: TriggerSchedule, Status: RunSkipped, StartedAt: now,
				Output: fmt.Sprintf("错失触发点 %s（平台不可用），按策略跳过不补跑", job.NextRunAt.Format(time.RFC3339)),
			})
			s.db.Model(job).Update("next_run_at", next)
			continue
		}
		// 先推进 next_run_at 再分发（单实例调度权威，无并发扫描）
		s.db.Model(job).Update("next_run_at", next)
		run, ok := s.startRun(job, TriggerSchedule)
		if !ok { // Forbid：上次未结束
			s.db.Create(&CronRun{
				JobID: job.ID, Trigger: TriggerSchedule, Status: RunSkipped, StartedAt: now,
				Output: "上一次执行仍在进行中（Forbid），本轮跳过",
			})
			s.db.Model(&CronJob{}).Where("id = ?", job.ID).Update("last_status", RunSkipped)
			continue
		}
		go s.execute(context.WithoutCancel(ctx), job, run)
	}
	return nil
}
