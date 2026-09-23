package resources

import "time"

// MetricSample 服务器指标采样点（agentless SSH 采集）。
// 写入量最大的表：联合索引 (server_id, ts)，保留策略见 collector 的 retention 任务。
type MetricSample struct {
	ID       uint      `gorm:"primarykey" json:"-"`
	ServerID uint      `gorm:"not null;index:idx_server_ts,priority:1" json:"serverId"`
	TS       time.Time `gorm:"not null;index:idx_server_ts,priority:2" json:"ts"`
	CPUPct   float64   `gorm:"not null" json:"cpuPct"`   // 0~100，与上一次采样的差值比
	MemUsed  uint64    `gorm:"not null" json:"memUsed"`  // 字节
	MemTotal uint64    `gorm:"not null" json:"memTotal"` // 字节
}

func (MetricSample) TableName() string { return "metric_samples" }

// ServerEvent 服务器状态事件（不可达/恢复等），告警桥接 notify 模块的原始数据。
type ServerEvent struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	ServerID  uint      `gorm:"not null;index" json:"serverId"`
	Type      string    `gorm:"size:32;not null" json:"type"` // unreachable | recovered | credential_changed
	Message   string    `gorm:"size:255" json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

func (ServerEvent) TableName() string { return "server_events" }
