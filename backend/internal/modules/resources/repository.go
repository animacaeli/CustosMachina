package resources

import (
	"context"

	"gorm.io/gorm"
)

type ServerRepository struct{ db *gorm.DB }

func NewServerRepository(db *gorm.DB) *ServerRepository { return &ServerRepository{db: db} }

func (r *ServerRepository) List(ctx context.Context) ([]Server, error) {
	var out []Server
	err := r.db.WithContext(ctx).Order("id").Find(&out).Error
	return out, err
}

func (r *ServerRepository) GetByID(ctx context.Context, id uint) (*Server, error) {
	var s Server
	if err := r.db.WithContext(ctx).First(&s, id).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *ServerRepository) Create(ctx context.Context, s *Server) error {
	return r.db.WithContext(ctx).Create(s).Error
}

func (r *ServerRepository) Update(ctx context.Context, s *Server) error {
	return r.db.WithContext(ctx).Save(s).Error
}

func (r *ServerRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&Server{}, id).Error
}

type GroupRepository struct{ db *gorm.DB }

func NewGroupRepository(db *gorm.DB) *GroupRepository { return &GroupRepository{db: db} }

func (r *GroupRepository) List(ctx context.Context) ([]ServerGroup, error) {
	var out []ServerGroup
	err := r.db.WithContext(ctx).Order("id").Find(&out).Error
	return out, err
}

func (r *GroupRepository) GetByID(ctx context.Context, id uint) (*ServerGroup, error) {
	var g ServerGroup
	if err := r.db.WithContext(ctx).First(&g, id).Error; err != nil {
		return nil, err
	}
	return &g, nil
}

func (r *GroupRepository) Create(ctx context.Context, g *ServerGroup) error {
	return r.db.WithContext(ctx).Create(g).Error
}

func (r *GroupRepository) Update(ctx context.Context, g *ServerGroup) error {
	return r.db.WithContext(ctx).Save(g).Error
}

func (r *GroupRepository) Delete(ctx context.Context, id uint) error {
	return r.db.WithContext(ctx).Delete(&ServerGroup{}, id).Error
}

// CountByGroup 统计各分组下服务器数（删除分组前校验用）。
func (r *GroupRepository) CountByGroup(ctx context.Context, groupID uint) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&Server{}).Where("group_id = ?", groupID).Count(&n).Error
	return n, err
}
