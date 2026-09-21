package identity

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PlatformSetting 平台级 KV 设置（Redis 地址、token 有效期等，运行时可改）。
type PlatformSetting struct {
	Key   string `gorm:"primarykey;size:64" json:"key"`
	Value string `gorm:"type:text" json:"value"`
}

func (PlatformSetting) TableName() string { return "platform_settings" }

// SettingsRepository KV 设置仓储。
type SettingsRepository interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string) error
}

type settingsRepository struct{ repo *gorm.DB }

func NewSettingsRepository(db *gorm.DB) SettingsRepository {
	return &settingsRepository{repo: db}
}

func (r *settingsRepository) Get(ctx context.Context, key string) (string, bool, error) {
	var s PlatformSetting
	if err := r.repo.WithContext(ctx).First(&s, "key = ?", key).Error; err != nil {
		return "", false, nil // 不存在不视为错误
	}
	return s.Value, true, nil
}

func (r *settingsRepository) Set(ctx context.Context, key, value string) error {
	return r.repo.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&PlatformSetting{Key: key, Value: value}).Error
}
