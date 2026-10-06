package projects

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/strx"
)

var ErrNotFound = errors.New("项目不存在")

// ProjectOut 对外视图：不含 CI token 密文。
type ProjectOut struct {
	Project
	HasCIToken bool `json:"hasCiToken"`
	// Envs 每环境运行概览（环境页/项目列表的"一眼判断"列）：
	// 最近一次发布（tag/状态/时间）+ prod 蓝绿活跃色。两条批量查询，无 N+1。
	Envs []EnvStatus `json:"envs"`
}

// EnvStatus 单环境概览。
type EnvStatus struct {
	EnvType       string     `json:"envType"`
	LastTag       string     `json:"lastTag"`
	LastStatus    string     `json:"lastStatus"`
	LastReleaseAt *time.Time `json:"lastReleaseAt"`
	ActiveColor   string     `json:"activeColor"` // prod 蓝绿活跃色；未启用为空
}

// 密文字段绑定（GCM AAD）
// projects.ci_token 经 crypto.AADProjectCIToken 共享（projects 写 ci 读）

func toOut(p Project) ProjectOut {
	out := ProjectOut{Project: p, HasCIToken: p.CIToken != ""}
	out.CIToken = ""
	out.DeployPrefix = strx.NormalizeName(p.Name) + "-" // 容器视图过滤用（前端不再自行实现命名规则）
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
	Name          string `json:"name" binding:"required,max=64"`
	RepoURL       string `json:"repoUrl" binding:"required,url,max=255"`
	RepoPath      string `json:"repoPath" binding:"required,max=255"`
	Provider      string `json:"provider" binding:"omitempty,oneof=gitea gitee"` // 空 = gitea（存量默认）
	CIJob         string `json:"ciJob" binding:"omitempty,max=128"`              // provider=gitee 时的 Jenkins job 名
	CIToken       string `json:"ciToken" binding:"omitempty,max=512"`            // 留空保留
	ComposePath   string `json:"composePath" binding:"omitempty,max=255"`
	DefaultBranch string `json:"defaultBranch" binding:"omitempty,max=128"`
	// 指针语义：Create nil = 不设置（通知群）/默认 true（成功通知）；Update nil = 保留原值
	NotifyProdGroupID   *uint `json:"notifyProdGroupId"`
	NotifyCanaryGroupID *uint `json:"notifyCanaryGroupId"`
	NotifyTestGroupID   *uint `json:"notifyTestGroupId"`
	NotifyOnSuccess     *bool `json:"notifyOnSuccess"`
	TestSlotCount       int   `json:"testSlotCount" binding:"omitempty,min=0,max=64"`
	TrafficCap          int   `json:"trafficCap" binding:"omitempty,min=1,max=100"`
	SlotGraceDays       int   `json:"slotGraceDays" binding:"omitempty,min=1,max=30"`
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
	onSuccess := true // 默认成功也通知（2026-09-26 用户要求默认勾选）
	if in.NotifyOnSuccess != nil {
		onSuccess = *in.NotifyOnSuccess
	}
	p := Project{
		Name: in.Name, RepoURL: in.RepoURL, RepoPath: in.RepoPath,
		Provider: defaultStr(in.Provider, "gitea"), CIJob: in.CIJob,
		ComposePath: in.ComposePath, DefaultBranch: defaultStr(in.DefaultBranch, "main"),
		NotifyProdGroupID:   in.NotifyProdGroupID,
		NotifyCanaryGroupID: in.NotifyProdGroupID, // 灰度通知群恒等于正式（不分离）
		NotifyTestGroupID:   in.NotifyTestGroupID, NotifyOnSuccess: onSuccess,
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
	if in.Provider != "" {
		p.Provider = in.Provider
	}
	p.CIJob = in.CIJob
	p.ComposePath = in.ComposePath
	if in.DefaultBranch != "" {
		p.DefaultBranch = in.DefaultBranch
	}
	// nil = 保留原值（部分更新不清空；显式传 null 会被 JSON 解为 nil，如需清空用前端全量提交约定外的专门接口）
	if in.NotifyProdGroupID != nil {
		p.NotifyProdGroupID = in.NotifyProdGroupID
	}
	// 2026-09-26 用户定案：正式与灰度本质同一生产环境，通知群不分离——灰度恒等于正式
	p.NotifyCanaryGroupID = p.NotifyProdGroupID
	if in.NotifyTestGroupID != nil {
		p.NotifyTestGroupID = in.NotifyTestGroupID
	}
	if in.NotifyOnSuccess != nil {
		p.NotifyOnSuccess = *in.NotifyOnSuccess
	}
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
	// 硬删：软删行会占住 name 唯一索引导致"删了建不回"；构建/发布记录
	// 按 project_id 关联保留（项目没了列表不再展示，作为历史沉淀可接受）
	res := s.db.WithContext(ctx).Unscoped().Delete(&Project{}, id)
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
	envs := s.envStatusMap(ctx)
	out := make([]ProjectOut, len(ps))
	for i, p := range ps {
		out[i] = toOut(p)
		out[i].Envs = envs[p.ID]
	}
	return out, nil
}

// envStatusMap 项目 ID → 每环境概览。跨模块读模型（releases/blue_green_states），
// 与 release.ActiveDomainFor 读 projects 表同款约定：模块间只读投影、不引依赖。
func (s *Service) envStatusMap(ctx context.Context) map[uint][]EnvStatus {
	var rels []struct {
		ProjectID uint
		EnvType   string
		Tag       string
		Status    string
		CreatedAt time.Time
	}
	_ = s.db.WithContext(ctx).Table("releases").
		Select("releases.project_id, releases.env_type, releases.tag, releases.status, releases.created_at").
		Joins("JOIN (SELECT project_id, env_type, MAX(id) AS max_id FROM releases GROUP BY project_id, env_type) m ON releases.id = m.max_id").
		Scan(&rels).Error
	var bgs []struct {
		ProjectID   uint
		ActiveColor string
	}
	_ = s.db.WithContext(ctx).Table("blue_green_states").
		Select("project_id, active_color").Scan(&bgs).Error
	colorOf := map[uint]string{}
	for _, b := range bgs {
		colorOf[b.ProjectID] = b.ActiveColor
	}
	out := map[uint][]EnvStatus{}
	for _, r := range rels {
		es := EnvStatus{
			EnvType: r.EnvType, LastTag: r.Tag, LastStatus: r.Status,
			LastReleaseAt: &r.CreatedAt,
		}
		if r.EnvType == "prod" {
			es.ActiveColor = colorOf[r.ProjectID]
		}
		out[r.ProjectID] = append(out[r.ProjectID], es)
	}
	return out
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
	EnvType   string `json:"envType" binding:"required,oneof=prod canary test"`
	ServerID  uint   `json:"serverId"` // 0 且 ClusterID=0 = 清空该环境目标（保存整体替换语义）
	Runtime   string `json:"runtime" binding:"omitempty,oneof=compose k3s"`
	ClusterID uint   `json:"clusterId"` // runtime=k3s 时必填（服务层校验）
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
	}
	// 目标行校验（0/0 = 清空该环境，不落行；v0.12.1 复核 N1：校验循环跳过
	// 而事务循环照落 server_id=0 零行，击穿下游 First/Count 守卫并误导排障）
	for _, t := range in.Targets {
		if t.ServerID == 0 && t.ClusterID == 0 {
			continue
		}
		runtime := t.Runtime
		if runtime == "" {
			runtime = RuntimeCompose
		}
		switch runtime {
		case RuntimeK3s:
			// k3s 目标：校验集群存在（server_id 恒 0，不能走 servers 校验）
			var cnt int64
			if err := s.db.Table("k3s_clusters").Where("id = ?", t.ClusterID).Count(&cnt).Error; err != nil {
				return err
			}
			if cnt == 0 {
				return fmt.Errorf("k3s 集群 %d 不存在", t.ClusterID)
			}
		default:
			if t.ServerID == 0 {
				return fmt.Errorf("环境 %s 缺少目标主机", t.EnvType)
			}
			var cnt int64
			if err := s.db.Table("servers").Where("id = ?", t.ServerID).Count(&cnt).Error; err != nil {
				return err
			}
			if cnt == 0 {
				return fmt.Errorf("服务器 %d 不存在", t.ServerID)
			}
		}
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("project_id = ?", projectID).Delete(&EnvTarget{}).Error; err != nil {
			return err
		}
		for _, t := range in.Targets {
			if t.ServerID == 0 && t.ClusterID == 0 {
				continue // 清空：与校验循环对称，绝不落零行
			}
			runtime := t.Runtime
			if runtime == "" {
				runtime = RuntimeCompose
			}
			if err := tx.Create(&EnvTarget{
				ProjectID: projectID, EnvType: t.EnvType, ServerID: t.ServerID, Runtime: runtime, ClusterID: t.ClusterID,
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
	return s.cipher.Decrypt(p.CIToken, crypto.AADProjectCIToken)
}

func (s *Service) encryptToken(token string) (string, error) {
	if s.cipher == nil {
		return "", errors.New("平台主密钥未配置，无法加密 CI token")
	}
	return s.cipher.Encrypt(token, crypto.AADProjectCIToken)
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
