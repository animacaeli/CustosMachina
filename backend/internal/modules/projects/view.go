package projects

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// View 项目的只读投影：ci/release/canary/slots 等模块一律通过本结构取
// 项目数据（消费方声明小接口、wire 绑定 *Service），不再 Table("projects")
// 直读——列改名从"运行期才炸"变为编译期可见。
type View struct {
	ID                  uint
	Name                string
	RepoPath            string
	Provider            string
	CIJob               string
	ComposePath         string
	DefaultBranch       string
	TestSlotCount       int
	TestSlotPeak        int
	SlotGraceDays       int
	TrafficCap          int
	NotifyOnSuccess     bool
	NotifyProdGroupID   *uint
	NotifyCanaryGroupID *uint
	NotifyTestGroupID   *uint
}

var ErrViewNotFound = errors.New("项目不存在")

// viewCols 显式列出投影列（而非 SELECT *）：消费方与测试只需保证这些列存在。
const viewCols = "id, name, repo_path, provider, ci_job, compose_path, default_branch, test_slot_count, test_slot_peak, slot_grace_days, traffic_cap, notify_on_success, notify_prod_group_id, notify_canary_group_id, notify_test_group_id"

// ViewByID 按主键取投影。
func (s *Service) ViewByID(ctx context.Context, id uint) (*View, error) {
	var v View
	if err := s.db.WithContext(ctx).Table("projects").
		Select(viewCols).Where("id = ?", id).First(&v).Error; err != nil {
		return nil, ErrViewNotFound
	}
	return &v, nil
}

// ViewByRepoPath 供 CI webhook 匹配：精确优先（走索引），大小写兜底全扫。
// 找不到返回 ErrViewNotFound（调用方决定忽略或报错）。
func (s *Service) ViewByRepoPath(ctx context.Context, repoPath, provider string) (*View, error) {
	var v View
	if err := s.findByRepo(ctx, repoPath, provider, viewCols, &v); err != nil {
		return nil, ErrViewNotFound
	}
	return &v, nil
}

// ViewsByRepoPath returns every project bound to the provider and repository,
// including bindings that differ only in path casing.
func (s *Service) ViewsByRepoPath(ctx context.Context, repoPath, provider string) ([]View, error) {
	var views []View
	err := s.db.WithContext(ctx).Model(&Project{}).Select(viewCols).
		Where("provider = ? AND lower(repo_path) = ?", provider, strings.ToLower(repoPath)).
		Order("id").Find(&views).Error
	return views, err
}

func (s *Service) findByRepo(ctx context.Context, repoPath, provider, columns string, dest any) error {
	q := s.db.WithContext(ctx).Model(&Project{}).Select(columns).Where("provider = ?", provider)
	err := q.Session(&gorm.Session{}).Where("repo_path = ?", repoPath).First(dest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = q.Session(&gorm.Session{}).Where("lower(repo_path) = ?", strings.ToLower(repoPath)).First(dest).Error
	}
	return err
}

// EncryptedCIToken 项目级 token 密文（仅 ci 模块使用；解密归调用方）。
func (s *Service) EncryptedCIToken(ctx context.Context, repoPath, provider string) string {
	view, err := s.ViewByRepoPath(ctx, repoPath, provider)
	if err != nil {
		return ""
	}
	var token string
	if err := s.db.WithContext(ctx).Model(&Project{}).
		Where("id = ?", view.ID).Pluck("ci_token", &token).Error; err != nil {
		return ""
	}
	return token
}

// Reader 消费方（ci/release/canary/slots）依赖的最小只读接口；
// wire 将 *Service 绑定到它，替代跨模块直读表。
type Reader interface {
	ViewByID(ctx context.Context, id uint) (*View, error)
	ViewByRepoPath(ctx context.Context, repoPath, provider string) (*View, error)
	ViewsByRepoPath(ctx context.Context, repoPath, provider string) ([]View, error)
	EncryptedCIToken(ctx context.Context, repoPath, provider string) string
}

// ValidateTaskProject locks the selected project until the task save transaction ends.
func ValidateTaskProject(ctx context.Context, db *gorm.DB, id *uint) error {
	if id == nil || *id == 0 {
		return nil
	}
	var p Project
	err := db.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").First(&p, *id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("关联项目不存在")
	}
	return err
}
