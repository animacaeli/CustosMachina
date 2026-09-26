package projects

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// View 项目的只读投影：ci/release/canary/slots 等模块一律通过本结构取
// 项目数据（消费方声明小接口、wire 绑定 *Service），不再 Table("projects")
// 直读——列改名从"运行期才炸"变为编译期可见。
type View struct {
	ID                  uint
	Name                string
	RepoPath            string
	ComposePath         string
	DefaultBranch       string
	TestSlotCount       int
	SlotGraceDays       int
	TrafficCap          int
	NotifyOnSuccess     bool
	NotifyProdGroupID   *uint
	NotifyCanaryGroupID *uint
	NotifyTestGroupID   *uint
}

var ErrViewNotFound = errors.New("项目不存在")

// viewCols 显式列出投影列（而非 SELECT *）：消费方与测试只需保证这些列存在。
const viewCols = "id, name, repo_path, compose_path, default_branch, test_slot_count, slot_grace_days, traffic_cap, notify_on_success, notify_prod_group_id, notify_canary_group_id, notify_test_group_id"

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
func (s *Service) ViewByRepoPath(ctx context.Context, repoPath string) (*View, error) {
	var v View
	err := s.db.WithContext(ctx).Table("projects").
		Select(viewCols).Where("repo_path = ?", repoPath).First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = s.db.WithContext(ctx).Table("projects").
			Select(viewCols).Where("lower(repo_path) = ?", strings.ToLower(repoPath)).First(&v).Error
	}
	if err != nil {
		return nil, ErrViewNotFound
	}
	return &v, nil
}

// EncryptedCIToken 项目级 token 密文（仅 ci 模块使用；解密归调用方）。
func (s *Service) EncryptedCIToken(ctx context.Context, repoPath string) string {
	var row struct{ CIToken string }
	if err := s.db.WithContext(ctx).Model(&Project{}).
		Where("repo_path = ?", repoPath).First(&row).Error; err != nil {
		return ""
	}
	return row.CIToken
}

// Reader 消费方（ci/release/canary/slots）依赖的最小只读接口；
// wire 将 *Service 绑定到它，替代跨模块直读表。
type Reader interface {
	ViewByID(ctx context.Context, id uint) (*View, error)
	ViewByRepoPath(ctx context.Context, repoPath string) (*View, error)
	EncryptedCIToken(ctx context.Context, repoPath string) string
}
