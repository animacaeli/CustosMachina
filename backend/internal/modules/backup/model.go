// Package backup 备份与恢复（P5 M2，docs/plan-phase5-services.md 第二节 M2）。
// 三类任务之一期实现 platform_self（平台 DB+数据目录+密钥份额）与
// remote_dir（SSH 远端目录打包回拉）；remote_db 留待有真实 MySQL 业务机再补。
// 存储双通道：本地目录轮转 / S3 兼容对象存储直连（minio-go）。
// 调度复用 cron 模块的"扫描型 Job"模式（30s 扫 next_run_at，单实例调度权威）。
package backup

import (
	"time"
)

// 任务类型。
const (
	TypePlatformSelf = "platform_self" // 平台自身：DB 文件 + 数据目录 + 口令加密的密钥份额
	TypeRemoteDir    = "remote_dir"    // 远端主机目录：tar 打包 → SFTP 拉回 → 存储
)

// 存储类型。
const (
	StorageLocal = "local"
	StorageS3    = "s3"
)

// 运行状态。
const (
	RunRunning = "running"
	RunSuccess = "success"
	RunFailed  = "failed"
	RunUnknown = "unknown" // 平台重启时结果未知的悬空记录
)

const (
	TriggerSchedule = "schedule"
	TriggerManual   = "manual"
)

// Job 备份任务。
type Job struct {
	ID uint `gorm:"primarykey" json:"id"`
	// 定义
	Name    string `gorm:"size:64;not null" json:"name"`
	Type    string `gorm:"size:16;not null" json:"type"` // platform_self | remote_dir
	Enabled bool   `gorm:"not null;default:true" json:"enabled"`
	// 调度（空表达式 = 仅手动）
	Schedule  string     `gorm:"size:32" json:"schedule"` // 标准 5 段 crontab
	NextRunAt *time.Time `gorm:"index" json:"nextRunAt"`
	// remote_dir 目标
	ServerID   uint   `json:"serverId"`                   // remote_dir：目标主机
	RemotePath string `gorm:"size:512" json:"remotePath"` // remote_dir：远端目录
	// 存储
	Storage        string    `gorm:"size:8;not null;default:local" json:"storage"` // local | s3
	S3Endpoint     string    `gorm:"size:255" json:"s3Endpoint"`
	S3Bucket       string    `gorm:"size:64" json:"s3Bucket"`
	S3Prefix       string    `gorm:"size:255" json:"s3Prefix"` // 对象前缀（按任务名默认化）
	S3AccessKey    string    `gorm:"size:128" json:"-"`
	S3SecretEnc    string    `gorm:"type:text" json:"-"` // 平台 cipher 加密
	LocalDir       string    `gorm:"size:255" json:"localDir"`
	PassphraseEnc  string    `gorm:"type:text" json:"-"` // 平台 cipher 加密的备份口令（platform_self 密钥份额用）
	RetentionCount int       `gorm:"not null;default:7" json:"retentionCount"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

func (Job) TableName() string { return "backup_jobs" }

// Run 备份执行历史（生命周期模式对齐 cron_runs：保留期清理 + 悬空恢复）。
type Run struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	JobID     uint      `gorm:"index" json:"jobId"`
	JobName   string    `gorm:"size:64" json:"jobName"` // 任务删除后仍可读
	Trigger   string    `gorm:"size:16" json:"trigger"` // schedule | manual
	Status    string    `gorm:"size:16;index" json:"status"`
	Artifact  string    `gorm:"size:255" json:"artifact"` // 存储侧对象名/文件名
	SizeBytes int64     `json:"sizeBytes"`
	Output    string    `gorm:"type:text" json:"output"` // 截断的执行输出/错误
	StartedAt time.Time `json:"startedAt"`
	CreatedAt time.Time `json:"createdAt"`
}

func (Run) TableName() string { return "backup_runs" }

// Models 自动迁移清单。
func Models() []any { return []any{&Job{}, &Run{}} }
