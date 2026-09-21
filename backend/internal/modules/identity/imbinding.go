package identity

import "time"

// UserIMBinding 平台用户 ↔ IM userid 绑定（FR2.4）。
// 同一 IM 用户在一个 provider 内唯一；用户表不存任何 IM 凭证。
type UserIMBinding struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	UserID    uint      `gorm:"not null;index" json:"userId"`
	Provider  string    `gorm:"size:32;not null;uniqueIndex:idx_provider_imuser" json:"provider"`
	IMUserID  string    `gorm:"size:128;not null;uniqueIndex:idx_provider_imuser" json:"imUserId"`
	IMName    string    `gorm:"size:128" json:"imName"` // IM 侧昵称，仅展示用
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (UserIMBinding) TableName() string { return "user_im_bindings" }

// IMProviderConfig 已激活的 IM 提供商凭证（FR1.4，表 im_provider_configs）。
// CredentialsEncrypted 为 AES-256-GCM(hex) 的 JSON 配置，字段因 provider 而异。
type IMProviderConfig struct {
	ID                   uint      `gorm:"primarykey" json:"id"`
	Provider             string    `gorm:"size:32;not null;uniqueIndex" json:"provider"`
	CredentialsEncrypted string    `gorm:"type:text" json:"-"`
	Enabled              bool      `gorm:"not null;default:false" json:"enabled"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

func (IMProviderConfig) TableName() string { return "im_provider_configs" }
