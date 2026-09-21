package identity

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// IMBindingRepository IM 绑定与提供商配置的仓储接口（单测可 mock）。
type IMBindingRepository interface {
	GetBinding(ctx context.Context, provider, imUserID string) (*UserIMBinding, error)
	SaveBinding(ctx context.Context, b *UserIMBinding) error
	GetProviderConfig(ctx context.Context, provider string) (*IMProviderConfig, error)
	SaveProviderConfig(ctx context.Context, cfg *IMProviderConfig) error
}

type imBindingRepository struct{ db *gorm.DB }

func NewIMBindingRepository(db *gorm.DB) IMBindingRepository {
	return &imBindingRepository{db: db}
}

func (r *imBindingRepository) GetBinding(ctx context.Context, provider, imUserID string) (*UserIMBinding, error) {
	var b UserIMBinding
	if err := r.db.WithContext(ctx).
		Where("provider = ? AND im_user_id = ?", provider, imUserID).
		First(&b).Error; err != nil {
		return nil, err
	}
	return &b, nil
}

func (r *imBindingRepository) SaveBinding(ctx context.Context, b *UserIMBinding) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "provider"}, {Name: "im_user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"im_name", "user_id", "updated_at"}),
	}).Create(b).Error
}

func (r *imBindingRepository) GetProviderConfig(ctx context.Context, provider string) (*IMProviderConfig, error) {
	var c IMProviderConfig
	if err := r.db.WithContext(ctx).Where("provider = ?", provider).First(&c).Error; err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *imBindingRepository) SaveProviderConfig(ctx context.Context, cfg *IMProviderConfig) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "provider"}},
		DoUpdates: clause.AssignmentColumns([]string{"credentials_encrypted", "enabled", "updated_at"}),
	}).Create(cfg).Error
}
