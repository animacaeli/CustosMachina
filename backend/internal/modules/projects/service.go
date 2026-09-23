package projects

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/crypto"
)

var ErrNotFound = errors.New("项目不存在")

// ProjectOut 对外视图：不含 CI token 密文。
type ProjectOut struct {
	Project
	HasCIToken bool `json:"hasCiToken"`
}

func toOut(p Project) ProjectOut {
	out := ProjectOut{Project: p, HasCIToken: p.CIToken != ""}
	out.CIToken = ""
	return out
}

type Service struct {
	db     *gorm.DB
	cipher *crypto.Cipher
}

func NewService(db *gorm.DB, cipher *crypto.Cipher) *Service {
	return &Service{db: db, cipher: cipher}
}

type SaveProjectInput struct {
	Name                string `json:"name" binding:"required,max=64"`
	RepoURL             string `json:"repoUrl" binding:"required,url,max=255"`
	RepoPath            string `json:"repoPath" binding:"required,max=255"`
	CIToken             string `json:"ciToken" binding:"omitempty,max=512"` // 留空保留
	ComposePath         string `json:"composePath" binding:"omitempty,max=255"`
	DefaultBranch       string `json:"defaultBranch" binding:"omitempty,max=128"`
	NotifyProdGroupID   *uint  `json:"notifyProdGroupId"`
	NotifyCanaryGroupID *uint  `json:"notifyCanaryGroupId"`
	NotifyTestGroupID   *uint  `json:"notifyTestGroupId"`
	NotifyOnSuccess     bool   `json:"notifyOnSuccess"`
	TestSlotCount       int    `json:"testSlotCount" binding:"omitempty,min=0,max=64"`
	TrafficCap          int    `json:"trafficCap" binding:"omitempty,min=1,max=100"`
	SlotGraceDays       int    `json:"slotGraceDays" binding:"omitempty,min=1,max=30"`
}

// validateNotifyGroups 校验环境→群用途匹配：prod/canary 只能绑【P】群，test 只能绑【dev】群。
func (s *Service) validateNotifyGroups(prod, canary, test *uint) error {
	check := func(id *uint, wantScope string) error {
		if id == nil {
			return nil
		}
		var g struct{ Scope string }
		err := s.db.Table("notify_groups").Select("scope").Where("id = ?", *id).First(&g).Error
		if err != nil {
			return fmt.Errorf("通知群 %d 不存在", *id)
		}
		if g.Scope != wantScope {
			return fmt.Errorf("通知群 %d 用途不符（需要 %s 类群）", *id, wantScope)
		}
		return nil
	}
	for _, e := range []struct {
		id    *uint
		scope string
	}{{prod, "prod"}, {canary, "prod"}, {test, "dev"}} {
		if err := check(e.id, e.scope); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) Create(ctx context.Context, in SaveProjectInput) (*ProjectOut, error) {
	if err := s.validateNotifyGroups(in.NotifyProdGroupID, in.NotifyCanaryGroupID, in.NotifyTestGroupID); err != nil {
		return nil, err
	}
	p := Project{
		Name: in.Name, RepoURL: in.RepoURL, RepoPath: in.RepoPath,
		ComposePath: in.ComposePath, DefaultBranch: defaultStr(in.DefaultBranch, "main"),
		NotifyProdGroupID: in.NotifyProdGroupID, NotifyCanaryGroupID: in.NotifyCanaryGroupID,
		NotifyTestGroupID: in.NotifyTestGroupID, NotifyOnSuccess: in.NotifyOnSuccess,
		TestSlotCount: defaultInt(in.TestSlotCount, 3), TrafficCap: defaultInt(in.TrafficCap, 50),
		SlotGraceDays: defaultInt(in.SlotGraceDays, 3),
	}
	if in.CIToken != "" {
		enc, err := s.encryptToken(in.CIToken)
		if err != nil {
			return nil, err
		}
		p.CIToken = enc
	}
	if err := s.db.WithContext(ctx).Create(&p).Error; err != nil {
		return nil, err
	}
	out := toOut(p)
	return &out, nil
}

func (s *Service) Update(ctx context.Context, id uint, in SaveProjectInput) (*ProjectOut, error) {
	var p Project
	if err := s.db.WithContext(ctx).First(&p, id).Error; err != nil {
		return nil, ErrNotFound
	}
	if err := s.validateNotifyGroups(in.NotifyProdGroupID, in.NotifyCanaryGroupID, in.NotifyTestGroupID); err != nil {
		return nil, err
	}
	p.Name, p.RepoURL, p.RepoPath = in.Name, in.RepoURL, in.RepoPath
	p.ComposePath = in.ComposePath
	if in.DefaultBranch != "" {
		p.DefaultBranch = in.DefaultBranch
	}
	p.NotifyProdGroupID, p.NotifyCanaryGroupID = in.NotifyProdGroupID, in.NotifyCanaryGroupID
	p.NotifyTestGroupID, p.NotifyOnSuccess = in.NotifyTestGroupID, in.NotifyOnSuccess
	if in.TestSlotCount > 0 {
		p.TestSlotCount = in.TestSlotCount
	}
	if in.TrafficCap > 0 {
		p.TrafficCap = in.TrafficCap
	}
	if in.SlotGraceDays > 0 {
		p.SlotGraceDays = in.SlotGraceDays
	}
	if in.CIToken != "" {
		enc, err := s.encryptToken(in.CIToken)
		if err != nil {
			return nil, err
		}
		p.CIToken = enc
	}
	if err := s.db.WithContext(ctx).Save(&p).Error; err != nil {
		return nil, err
	}
	out := toOut(p)
	return &out, nil
}

func (s *Service) Delete(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&Project{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return s.db.WithContext(ctx).Where("project_id = ?", id).Delete(&EnvTarget{}).Error
}

func (s *Service) List(ctx context.Context) ([]ProjectOut, error) {
	var ps []Project
	if err := s.db.WithContext(ctx).Order("id").Find(&ps).Error; err != nil {
		return nil, err
	}
	out := make([]ProjectOut, len(ps))
	for i, p := range ps {
		out[i] = toOut(p)
	}
	return out, nil
}

func (s *Service) Get(ctx context.Context, id uint) (*ProjectOut, []EnvTarget, error) {
	var p Project
	if err := s.db.WithContext(ctx).First(&p, id).Error; err != nil {
		return nil, nil, ErrNotFound
	}
	var targets []EnvTarget
	if err := s.db.WithContext(ctx).Where("project_id = ?", id).Order("env_type").Find(&targets).Error; err != nil {
		return nil, nil, err
	}
	out := toOut(p)
	return &out, targets, nil
}

// ---- 环境部署目标 ----

type SaveTargetsInput struct {
	Targets []TargetInput `json:"targets" binding:"required,min=1,dive"`
}

type TargetInput struct {
	EnvType  string `json:"envType" binding:"required,oneof=prod canary test"`
	ServerID uint   `json:"serverId" binding:"required,min=1"`
	Runtime  string `json:"runtime" binding:"omitempty,oneof=compose k3s"`
}

// SaveTargets 整体替换某项目的部署目标（前端表格一次提交）。
func (s *Service) SaveTargets(ctx context.Context, projectID uint, in SaveTargetsInput) error {
	var p Project
	if err := s.db.WithContext(ctx).First(&p, projectID).Error; err != nil {
		return ErrNotFound
	}
	seen := map[string]bool{}
	for _, t := range in.Targets {
		if seen[t.EnvType] {
			return fmt.Errorf("环境 %s 重复配置", t.EnvType)
		}
		seen[t.EnvType] = true
		// 校验服务器存在（资源管理表）
		var cnt int64
		if err := s.db.Table("servers").Where("id = ?", t.ServerID).Count(&cnt).Error; err != nil {
			return err
		}
		if cnt == 0 {
			return fmt.Errorf("服务器 %d 不存在", t.ServerID)
		}
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("project_id = ?", projectID).Delete(&EnvTarget{}).Error; err != nil {
			return err
		}
		for _, t := range in.Targets {
			runtime := t.Runtime
			if runtime == "" {
				runtime = RuntimeCompose
			}
			if err := tx.Create(&EnvTarget{
				ProjectID: projectID, EnvType: t.EnvType, ServerID: t.ServerID, Runtime: runtime,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// DecryptedCIToken 项目级 token 解密（M2 gitea 客户端用；空串 = 用全局）。
func (s *Service) DecryptedCIToken(ctx context.Context, projectID uint) (string, error) {
	var p Project
	if err := s.db.WithContext(ctx).First(&p, projectID).Error; err != nil {
		return "", ErrNotFound
	}
	if p.CIToken == "" {
		return "", nil
	}
	if s.cipher == nil {
		return "", errors.New("平台主密钥未配置")
	}
	return s.cipher.Decrypt(p.CIToken)
}

func (s *Service) encryptToken(token string) (string, error) {
	if s.cipher == nil {
		return "", errors.New("平台主密钥未配置，无法加密 CI token")
	}
	return s.cipher.Encrypt(token)
}

func defaultStr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func defaultInt(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
