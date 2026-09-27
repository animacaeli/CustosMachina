// Package cron 定时任务 + 脚本库（第四阶段 M1，docs/plan-phase4-runtime.md）。
// 模型语义对齐 K8s CronJob：错失策略 = 跳过不补跑；并发策略 = Forbid；
// 执行 = 经 SSH 在目标机起一次性容器（docker run --rm / docker compose run --rm），
// k3s 期只需替换执行器，模型不动。
package cron

import (
	"time"

	"gorm.io/gorm"
)

// 脚本类型
const (
	ScriptShell      = "shell"       // 跑在绑定镜像内（镜像须含 sh）
	ScriptPython     = "python"      // 镜像须含 python3，脚本内容写入容器执行
	ScriptComposeRun = "compose-run" // 即项目 compose 里定义好的命令型服务
)

// 执行载体
const (
	CarrierRun     = "run"         // docker run --rm <镜像>
	CarrierCompose = "compose-run" // docker compose -p <proj> run --rm <svc>
)

// 运行记录状态
const (
	RunRunning = "running"
	RunSuccess = "success"
	RunFailed  = "failed"
	RunTimeout = "timeout"
	RunSkipped = "skipped" // Forbid：上次未结束，本次跳过
	RunUnknown = "unknown" // 平台宕机前发起、结果未知
)

// 运行触发方式
const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
	TriggerRetry    = "retry"
)

// CronScript 脚本库：仅 admin 可建可改。
type CronScript struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	Name      string         `gorm:"size:128;not null" json:"name"`
	Type      string         `gorm:"size:16;not null" json:"type"` // shell | python | compose-run
	Content   string         `gorm:"type:text" json:"content"`     // 脚本内容 / 子命令 / 备注（compose-run 仅备注）
	Remark    string         `gorm:"size:255" json:"remark"`
	CreatedBy string         `gorm:"size:64" json:"createdBy"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (CronScript) TableName() string { return "cron_scripts" }

// CronJob 定时任务：绑定脚本 + 调度 + 目标主机 + 执行载体。
type CronJob struct {
	ID          uint           `gorm:"primarykey" json:"id"`
	Name        string         `gorm:"size:128;not null" json:"name"`
	ScriptID    uint           `gorm:"index;not null" json:"scriptId"`
	Schedule    string         `gorm:"size:64;not null" json:"schedule"` // 5 段式 cron 或 @every 1h
	ServerID    uint           `gorm:"not null" json:"serverId"`
	Carrier     string         `gorm:"size:16;not null" json:"carrier"` // run | compose-run
	Image       string         `gorm:"size:255" json:"image"`           // carrier=run 必填
	ProjectName string         `gorm:"size:64" json:"projectName"`      // carrier=compose-run 必填（部署目录名）
	Service     string         `gorm:"size:64" json:"service"`          // carrier=compose-run 必填
	Command     string         `gorm:"size:512" json:"command"`         // 附加参数（shell/python 追加到解释器后）
	Network     string         `gorm:"size:64" json:"network"`          // carrier=run 可选：docker --network（连业务网络查数据用）
	TimeoutSecs int            `gorm:"not null;default:600" json:"timeoutSecs"`
	Retry       int            `gorm:"not null;default:0" json:"retry"` // 失败后重试次数（0-3，间隔 5 分钟）
	Enabled     bool           `gorm:"not null;default:true" json:"enabled"`
	NextRunAt   *time.Time     `gorm:"index" json:"nextRunAt"`
	LastStatus  string         `gorm:"size:16" json:"lastStatus"`
	LastRunAt   *time.Time     `json:"lastRunAt"`
	CreatedBy   string         `gorm:"size:64" json:"createdBy"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (CronJob) TableName() string { return "cron_jobs" }

// CronRun 执行历史：输出回平台留痕，不依赖目标机日志。
type CronRun struct {
	ID           uint       `gorm:"primarykey" json:"id"`
	JobID        uint       `gorm:"index;not null" json:"jobId"`
	ServerID     uint       `gorm:"not null" json:"serverId"`        // 下载全量日志（SFTP ticket）用
	Trigger      string     `gorm:"size:16;not null" json:"trigger"` // schedule | manual | retry
	Status       string     `gorm:"size:16;index;not null" json:"status"`
	Output       string     `gorm:"type:text" json:"output"`
	OutputFile   string     `gorm:"size:255" json:"outputFile"` // 目标机全量日志路径（64KB 截断的兜底）
	DurationSecs int        `json:"durationSecs"`
	StartedAt    time.Time  `gorm:"not null" json:"startedAt"`
	FinishedAt   *time.Time `json:"finishedAt"`
	CreatedAt    time.Time  `json:"createdAt"`
}

func (CronRun) TableName() string { return "cron_runs" }

// Models 返回本模块需要自动迁移的模型。
func Models() []any {
	return []any{&CronScript{}, &CronJob{}, &CronRun{}}
}
