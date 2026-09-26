package canary

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

var ErrNotFound = errors.New("策略不存在")

// SSHRunner 灰度承载层需要的最小 SSH 能力（resources.Service 提供实现）。
type SSHRunner interface {
	// DeployNginxConf 写灰度配置片段到目标机并 reload nginx：
	// 自动探测宿主 nginx 或 nginx 容器（conf.d 挂载 / docker exec）。
	DeployNginxConf(ctx context.Context, serverID uint, projName, content string) (string, error)
}

type Service struct {
	db     *gorm.DB
	ssh    SSHRunner
	notify *notify.Service

	// mu 串行化策略写操作：流量总和校验与版本推进都是读-改-写，
	// 并发会超 TrafficCap / 出现中间态（单实例部署下进程内锁足够）
	mu sync.Mutex
}

func NewService(db *gorm.DB, ssh SSHRunner, ntfy *notify.Service) *Service {
	return &Service{db: db, ssh: ssh, notify: ntfy}
}

// ---- 策略 CRUD ----

type SavePolicyInput struct {
	Type           string `json:"type" binding:"required,oneof=header traffic"`
	HeaderKey      string `json:"headerKey" binding:"omitempty,max=64"`
	HeaderValue    string `json:"headerValue" binding:"omitempty,max=128"`
	TrafficPercent int    `json:"trafficPercent" binding:"omitempty,min=1,max=100"`
	BoundTag       string `json:"boundTag" binding:"required,max=128"`
	Enabled        *bool  `json:"enabled"`
}

func (s *Service) validate(ctx context.Context, projectID uint, in SavePolicyInput, excludeID uint) error {
	var proj struct {
		TrafficCap          int
		NotifyCanaryGroupID *uint
		Name                string
	}
	if err := s.db.WithContext(ctx).
		Table("projects").
		Select("traffic_cap, notify_canary_group_id, name").
		Where("id = ?", projectID).First(&proj).Error; err != nil {
		return errors.New("项目不存在")
	}
	switch in.Type {
	case TypeHeader:
		if in.HeaderKey == "" || in.HeaderValue == "" {
			return errors.New("请求头策略需填写 header 键与值")
		}
		// 写入 nginx map 片段的值必须限定字符集（Go 转义 ≠ nginx 语法安全）
		for _, ch := range in.HeaderKey + in.HeaderValue {
			if !((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') ||
				ch == '-' || ch == '_' || ch == '.' || ch >= 0x80) { // 0x80+ 允许中文等 UTF-8 字节
				return errors.New("请求头键/值只允许字母、数字、-_. 与中文")
			}
		}
	default: // traffic
		if in.TrafficPercent <= 0 {
			return errors.New("流量策略需填写比例")
		}
		// 总和 ≤ 项目上限（未启用策略与自身不计入）
		var sum int64
		tx := s.db.WithContext(ctx).Model(&Policy{}).
			Where("project_id = ? AND type = ? AND enabled = ?", projectID, TypeTraffic, true)
		if excludeID > 0 {
			tx = tx.Where("id <> ?", excludeID)
		}
		if err := tx.Select("COALESCE(SUM(traffic_percent),0)").Scan(&sum).Error; err != nil {
			return err
		}
		if int(sum)+in.TrafficPercent > proj.TrafficCap {
			return fmt.Errorf("流量比例总和将达 %d%%，超过项目上限 %d%%", int(sum)+in.TrafficPercent, proj.TrafficCap)
		}
	}
	// 绑定标签须是本项目已通过 CI 的 canary 标签
	var cnt int64
	if err := s.db.WithContext(ctx).
		Table("builds").
		Where("project_id = ? AND env_type = ? AND tag = ? AND status = ?", projectID, "canary", in.BoundTag, "success").
		Count(&cnt).Error; err != nil {
		return err
	}
	if cnt == 0 {
		return fmt.Errorf("绑定标签 %s 不是本项目已通过 CI 的灰度标签", in.BoundTag)
	}
	return nil
}

func (s *Service) Create(ctx context.Context, projectID uint, in SavePolicyInput) (*Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validate(ctx, projectID, in, 0); err != nil {
		return nil, err
	}
	p := Policy{
		ProjectID: projectID, Type: in.Type,
		HeaderKey: in.HeaderKey, HeaderValue: in.HeaderValue,
		TrafficPercent: in.TrafficPercent, BoundTag: in.BoundTag,
		Enabled: in.Enabled == nil || *in.Enabled,
	}
	if err := s.db.WithContext(ctx).Create(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Service) Update(ctx context.Context, projectID, id uint, in SavePolicyInput) (*Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var p Policy
	if err := s.db.WithContext(ctx).Where("project_id = ?", projectID).First(&p, id).Error; err != nil {
		return nil, ErrNotFound
	}
	if err := s.validate(ctx, projectID, in, id); err != nil {
		return nil, err
	}
	p.Type, p.HeaderKey, p.HeaderValue = in.Type, in.HeaderKey, in.HeaderValue
	p.TrafficPercent, p.BoundTag = in.TrafficPercent, in.BoundTag
	if in.Enabled != nil {
		p.Enabled = *in.Enabled
	}
	// 任何修改都回到未发布
	p.PublishedVersion = 0
	if err := s.db.WithContext(ctx).Save(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

func (s *Service) Delete(ctx context.Context, projectID, id uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := s.db.WithContext(ctx).Where("project_id = ?", projectID).Delete(&Policy{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// List 项目的策略列表（附当前发布版本号）。
func (s *Service) List(ctx context.Context, projectID uint) ([]Policy, int, error) {
	var ps []Policy
	if err := s.db.WithContext(ctx).Where("project_id = ?", projectID).Order("id").Find(&ps).Error; err != nil {
		return nil, 0, err
	}
	current := 0
	for _, p := range ps {
		if p.PublishedVersion > current {
			current = p.PublishedVersion
		}
	}
	return ps, current, nil
}

// ---- 聚合发布 ----

// Publish 把当前全部启用策略版本化整体生效：渲染 nginx 配置写入灰度部署目标并 reload。
// 版本语义：新版本 = 已发布集合整体替换（旧版本策略全部退回未发布）。
func (s *Service) Publish(ctx context.Context, projectID uint, operator string) (int, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var ps []Policy
	if err := s.db.WithContext(ctx).
		Where("project_id = ? AND enabled = ?", projectID, true).
		Order("id").Find(&ps).Error; err != nil {
		return 0, "", err
	}
	if len(ps) == 0 {
		return 0, "", errors.New("没有启用的策略可发布")
	}
	// 部署模型只有一个 <proj>-canary 实例：所有策略必须绑定同一灰度标签
	for i := 1; i < len(ps); i++ {
		if ps[i].BoundTag != ps[0].BoundTag {
			return 0, "", fmt.Errorf("策略绑定标签不一致（%s / %s）：一个灰度实例同一时间只跑一个版本", ps[0].BoundTag, ps[i].BoundTag)
		}
	}

	var proj struct {
		Name string
	}
	if err := s.db.WithContext(ctx).Table("projects").Select("name").Where("id = ?", projectID).First(&proj).Error; err != nil {
		return 0, "", errors.New("项目不存在")
	}
	var target struct{ ServerID uint }
	if err := s.db.WithContext(ctx).
		Table("project_env_targets").
		Select("server_id").
		Where("project_id = ? AND env_type = ?", projectID, "canary").
		First(&target).Error; err != nil {
		return 0, "", errors.New("项目未配置灰度环境的部署目标")
	}

	sort.Slice(ps, func(i, j int) bool { return ps[i].ID < ps[j].ID })
	conf, err := renderNginx(normName(proj.Name), ps)
	if err != nil {
		return 0, "", err
	}
	out, err := s.ssh.DeployNginxConf(ctx, target.ServerID, normName(proj.Name), conf)
	if err != nil {
		return 0, out, err
	}

	// 版本推进（事务：全部退回 0 再标记新版本，避免中间态"无生效版本"）
	var maxV int64
	s.db.WithContext(ctx).Model(&Policy{}).
		Where("project_id = ?", projectID).
		Select("COALESCE(MAX(published_version),0)").Scan(&maxV)
	newV := int(maxV) + 1
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Policy{}).
			Where("project_id = ?", projectID).
			Update("published_version", 0).Error; err != nil {
			return err
		}
		return tx.Model(&Policy{}).
			Where("id IN ?", policyIDs(ps)).
			Update("published_version", newV).Error
	}); err != nil {
		return 0, "", err
	}
	logger.Infof("[canary] 项目 %d 策略发布 v%d（%d 条），操作人 %s", projectID, newV, len(ps), operator)
	return newV, out, nil
}

func policyIDs(ps []Policy) []uint {
	ids := make([]uint, len(ps))
	for i, p := range ps {
		ids[i] = p.ID
	}
	return ids
}

func normName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, ch := range n {
		switch {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9', ch == '_', ch == '.', ch == '-':
			b.WriteRune(ch)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if out == "" {
		out = "project"
	}
	return out
}
