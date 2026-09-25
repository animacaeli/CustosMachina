package resources

import (
	"time"

	"gorm.io/gorm"
)

// ServerStatus 由采集调度器维护（M2 起更新；M1 仅连通性测试时短暂回写）。
type ServerStatus string

const (
	StatusUnknown     ServerStatus = "unknown"
	StatusReachable   ServerStatus = "reachable"
	StatusUnreachable ServerStatus = "unreachable"
	DefaultMetricSecs              = 30
)

// AuthType 凭据类型。
const (
	AuthPassword = "password" // 用户名+密码
	AuthKey      = "key"      // 用户名+私钥（可选口令）
)

// Server 受管服务器。agent_version 为空即 agentless 模式（集群管理预留字段）。
type Server struct {
	ID           uint           `gorm:"primarykey" json:"id"`
	Name         string         `gorm:"size:64;not null" json:"name"`
	Host         string         `gorm:"size:255;not null" json:"host"`
	Port         int            `gorm:"not null;default:22" json:"port"`
	GroupID      *uint          `gorm:"index" json:"groupId"`
	AuthType     string         `gorm:"size:16;not null" json:"authType"` // password | key
	Credential   string         `gorm:"type:text" json:"-"`               // 加密后的凭据 JSON，绝不外发
	AgentVersion string         `gorm:"size:32" json:"agentVersion"`
	Status       ServerStatus   `gorm:"size:16;default:unknown" json:"status"`
	LastSeen     *time.Time     `json:"lastSeen"`
	MetricSecs   int            `gorm:"not null;default:30" json:"metricSecs"` // 采集间隔，15/30/60
	HostInfo     string         `gorm:"type:text" json:"hostInfo"`             // 主机配置探测结果（JSON，环境探测时刷新）
	Remark       string         `gorm:"size:255" json:"remark"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Server) TableName() string { return "servers" }

// ServerGroup 服务器分组：一层平铺，不做树形。
type ServerGroup struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	Name      string         `gorm:"size:64;uniqueIndex;not null" json:"name"`
	Remark    string         `gorm:"size:255" json:"remark"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

func (ServerGroup) TableName() string { return "server_groups" }

// Models 返回本模块需要自动迁移的模型（须为指针，GORM 值类型迁移会报 unsupported data type）。
func Models() []any {
	return []any{&Server{}, &ServerGroup{}, &MetricSample{}, &ServerEvent{}}
}

// credential 是 Credential 字段加密前的明文结构。
type credential struct {
	Username   string `json:"username"`
	Password   string `json:"password,omitempty"`   // AuthPassword 用
	PrivateKey string `json:"privateKey,omitempty"` // AuthKey 用（OpenSSH PEM）
	Passphrase string `json:"passphrase,omitempty"` // 私钥口令，可选
}
