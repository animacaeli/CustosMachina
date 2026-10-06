// Package k3s P8-M3.2 k3s 最小承载：集群凭证管理 + compose→K8s 翻译 + 部署/回滚。
// 双轨载体的 k3s 侧（docs/research-k3s-spike.md 判定落地）：
// 平台只调 K8s API（零 SSH），kubeconfig AES 落库复用 IM 凭证加密通道。
package k3s

import (
	"time"
)

// Cluster k3s 集群凭证（资源层一等实体，多项目共享）。
type Cluster struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Name      string    `gorm:"size:64;uniqueIndex;not null" json:"name"`
	Server    string    `gorm:"size:255" json:"server"` // 展示用 API 地址（真实连接以 kubeconfig 为准）
	Kubeenc   string    `gorm:"type:text" json:"-"`     // kubeconfig AES 加密落库
	Domain    string    `gorm:"size:255" json:"domain"` // Ingress 域名后缀（如 k3s.internal），host = <project>-<env>.<domain>
	Remark    string    `gorm:"size:255" json:"remark"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (Cluster) TableName() string { return "k3s_clusters" }

// Models 返回本模块需要自动迁移的模型。
func Models() []any {
	return []any{&Cluster{}}
}
