package release

import (
	"context"
	"errors"
	"fmt"
	k3smod "github.com/custos-machina/backend/internal/modules/k3s"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/ci"
	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/modules/projects"
	"github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/strx"
)

var ErrNotFound = errors.New("发布记录不存在")

type Service struct {
	db     *gorm.DB
	ci     *ci.Service
	res    Deployer // *resources.Service（接口化便于测试，见 bluegreen.go）
	notify *notify.Service
	proj   projects.Reader // 只读投影，替代 Table("projects") 直读
	cipher *crypto.Cipher  // registry 凭据解密
	canary ConfRenderer    // 整份 nginx conf 单写者（蓝绿切换时取整份配置；依赖单向：release→canary）
	k3s    K3sDeployer     // P8-M3.2：k3s 目标部署器（nil = 未装配，目标仍拒）
}

// registryCred 项目绑定的 registry 凭据（namespace 隔离：镜像路径里的
// namespace 在仓库 compose 中声明，平台只负责按项目绑定的 registry 登录）。
type registryCred struct {
	Address  string
	Username string
	Password string
	HasCred  bool
}

// registryFor 取项目绑定的 registry 凭据（解密；未绑定或无凭据返回 HasCred=false）。

// 密文字段绑定（GCM AAD）
// registries.credential 经 crypto.AADRegistryCredential 共享（ci 写 release 读）

// PromotedContainer 当前稳态容器名（空=未转正；UI 转正按钮显隐依据）。
func (s *Service) PromotedContainer(ctx context.Context, projectID uint) string {
	var st BGState
	if err := s.db.WithContext(ctx).First(&st, projectID).Error; err != nil {
		return ""
	}
	return st.PromotedContainer
}

// Promote 转正（2026-11-08 命名统一）：活跃色容器 rename 为稳态名 <proj>-prod-1
// → 重渲染 conf（upstream 恒定名）→ reload → 状态落库。不进发布关键路径：
// 失败最坏只影响名字（rename 后 conf 未 reload 时 nginx 按缓存 IP 继续转发，流量无损）。
func (s *Service) Promote(ctx context.Context, projectID uint, operator string) error {
	var st BGState
	if err := s.db.WithContext(ctx).First(&st, projectID).Error; err != nil {
		return errors.New("项目未启用蓝绿（无活跃色记录）")
	}
	p, err := s.project(ctx, projectID)
	if err != nil {
		return err
	}
	target, err := s.envTarget(ctx, projectID, "prod")
	if err != nil {
		return err
	}
	norm := strx.NormalizeName(p.Name)
	steady := norm + "-prod-1"
	if st.PromotedContainer == "" {
		// 首次转正：rename + 落库（幂等锚点=落库后再写 conf——中途失败重试走下方补 conf 路径）
		if st.ActiveContainer == "" {
			return errors.New("活跃容器名未记录（旧版本部署的项目，请重新发布一次后再转正）")
		}
		if err := s.res.RenameContainer(ctx, target.ServerID, st.ActiveContainer, steady); err != nil {
			return err
		}
		if err := s.db.WithContext(ctx).Model(&BGState{}).Where("project_id = ?", projectID).
			Updates(map[string]any{"promoted_container": steady, "active_container": steady, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
	}
	// conf 重渲染（PromotedContainer 落库后 FullConf 自动用稳态名）+ 写入 reload。
	// 已转正时重入此函数 = 上次 conf 失败的修复重试（rename 已完成，只补 conf）
	conf, err := s.canary.FullConf(ctx, projectID, st.ActiveColor)
	if err != nil {
		return fmt.Errorf("conf 渲染失败（rename 已完成，可重试转正修复）: %w", err)
	}
	if _, err := s.res.DeployNginxConf(ctx, target.ServerID, p.Name, conf); err != nil {
		return fmt.Errorf("conf 写入失败（rename 已完成，可重试转正修复）: %w", err)
	}
	s.notifyRelease(ctx, p, &Release{Status: ReleaseSuccess, EnvType: "prod", Tag: "promote:" + steady, ReleaseBy: operator})
	return nil
}

// recordActiveContainer 记录活跃域入口容器名（占位符形态=渲染返回值；
// 存量 compose 渲染返回空 → 按旧规则 <域>-server-1 推断，两代命名可共存）。
func (s *Service) recordActiveContainer(ctx context.Context, projectID uint, domain, container string) {
	if container == "" {
		container = domain + "-server-1"
	}
	// 新版本部署 = 回到未转正态：必须同时清 promoted_container——
	// 否则后续 conf 渲染优先读旧稳态名（即将销毁的上一版容器），切流指向死容器
	s.db.WithContext(ctx).Exec(
		"UPDATE blue_green_states SET active_container = ?, promoted_container = '', updated_at = ? WHERE project_id = ?",
		container, time.Now(), projectID)
}

// recordCanaryDomain 记录/更新当前 canary 域（返回旧域供清理；首次部署旧值为空）。
func (s *Service) recordCanaryDomain(ctx context.Context, projectID uint, domain, container string) (oldDomain string) {
	var st CanaryState
	if err := s.db.WithContext(ctx).First(&st, projectID).Error; err == nil {
		oldDomain = st.Domain
	}
	if container == "" {
		container = domain + "-server-1"
	}
	s.db.WithContext(ctx).Save(&CanaryState{
		ProjectID: projectID, Domain: domain, Container: container, UpdatedAt: time.Now(),
	})
	return oldDomain
}

// imageFor 正式/灰度镜像全名：registry/<proj>:<tag>（2026-11-08 命名统一，
// 占位符 compose 的 image 由平台统一填充；未绑 registry 或 tag 空返回空——
// 无占位符的存量 compose 原文部署不受影响）。
func (s *Service) imageFor(ctx context.Context, projectID uint, tag string) string {
	if tag == "" {
		return ""
	}
	rc, err := s.registryFor(ctx, projectID)
	if err != nil || rc == nil || rc.Address == "" {
		return ""
	}
	var name string
	s.db.WithContext(ctx).Table("projects").Select("name").Where("id = ?", projectID).Scan(&name)
	if name == "" {
		return ""
	}
	// address 允许 host/namespace 形态（TCR 个人版 namespace 层）：
	// 镜像前缀=完整 address；docker login 用 host 段（registryFor 内已拆）
	return strings.TrimRight(rc.Address, "/") + "/" + strx.NormalizeName(name) + ":" + tag
}

func (s *Service) registryFor(ctx context.Context, projectID uint) (*registryCred, error) {
	var row struct {
		RAddress    string
		RCredential string
	}
	if err := s.db.WithContext(ctx).Table("projects").
		Select("registries.address, registries.credential").
		Joins("LEFT JOIN registries ON registries.id = projects.registry_id").
		Where("projects.id = ?", projectID).
		First(&row).Error; err != nil {
		return &registryCred{}, nil // 未绑定
	}
	if row.RAddress == "" || row.RCredential == "" {
		return &registryCred{}, nil
	}
	// 解密 "username:password"
	dec, err := s.decryptRegistryCred(row.RCredential)
	if err != nil {
		return nil, fmt.Errorf("registry 凭据解密失败: %w", err)
	}
	return &registryCred{Address: row.RAddress, Username: dec[0], Password: dec[1], HasCred: true}, nil
}

func (s *Service) decryptRegistryCred(enc string) ([]string, error) {
	c := s.cipher
	if c == nil {
		return nil, fmt.Errorf("平台主密钥未配置")
	}
	dec, err := c.Decrypt(enc, crypto.AADRegistryCredential)
	if err != nil {
		return nil, err
	}
	parts := strings.SplitN(dec, ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("凭据格式须为 username:password")
	}
	return parts, nil
}

func NewService(db *gorm.DB, ciSvc *ci.Service, resSvc Deployer, ntfy *notify.Service, proj projects.Reader, cipher *crypto.Cipher, canarySvc ConfRenderer) *Service {
	return &Service{db: db, ci: ciSvc, res: resSvc, notify: ntfy, proj: proj, cipher: cipher, canary: canarySvc}
}

// projectRow 只读 projects 所需列（避免跨模块循环依赖）。
type projectRow struct {
	ID                  uint
	Name                string
	RepoPath            string
	Provider            string
	CIJob               string
	ComposePath         string
	NotifyProdGroupID   *uint
	NotifyCanaryGroupID *uint
	NotifyTestGroupID   *uint
}

func (s *Service) project(ctx context.Context, id uint) (*projectRow, error) {
	v, err := s.proj.ViewByID(ctx, id)
	if err != nil {
		return nil, errors.New("项目不存在")
	}
	return &projectRow{
		ID: v.ID, Name: v.Name, RepoPath: v.RepoPath, Provider: v.Provider, CIJob: v.CIJob,
		ComposePath:       v.ComposePath,
		NotifyProdGroupID: v.NotifyProdGroupID, NotifyCanaryGroupID: v.NotifyCanaryGroupID,
		NotifyTestGroupID: v.NotifyTestGroupID,
	}, nil
}

// EnvTarget 项目某环境的部署目标。
type EnvTargetRow struct {
	ServerID  uint
	Runtime   string
	ClusterID uint
}

func (s *Service) envTarget(ctx context.Context, projectID uint, env string) (*EnvTargetRow, error) {
	var t EnvTargetRow
	if err := s.db.WithContext(ctx).
		Table("project_env_targets").
		Select("server_id, runtime, cluster_id").
		Where("project_id = ? AND env_type = ?", projectID, env).
		First(&t).Error; err != nil {
		return nil, fmt.Errorf("项目未配置 %s 环境的部署目标", env)
	}
	return &t, nil
}

// ---- 发布 ----

type ReleaseInput struct {
	ProjectID uint   `json:"projectId" binding:"required"`
	EnvType   string `json:"envType" binding:"required,oneof=prod canary test"`
	Tag       string `json:"tag" binding:"required,max=128"`
	// drainSecs 蓝绿 drain 窗口（默认 30s，5~600；长连接多的业务调大）
	DrainSecs int `json:"drainSecs" binding:"omitempty,min=5,max=600"`
}

// Execute 校验"已通过 CI"→ 取 compose 文件 → 部署 → 落发布历史 + 通知。
func (s *Service) Execute(ctx context.Context, in ReleaseInput, operator string, rollbackOf ...uint) (*Release, error) {
	p, err := s.project(ctx, in.ProjectID)
	if err != nil {
		return nil, err
	}
	// tag 白名单（tag 拼进 compose 镜像名/域名，收敛注入面；canary 域名含 tag）
	for _, r := range in.Tag {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' || r == '@') {
			return nil, fmt.Errorf("非法标签字符 %q", string(r))
		}
	}
	// 校验该标签在本项目该环境已通过 CI
	var cnt int64
	if err := s.db.WithContext(ctx).
		Table("builds").
		Where("project_id = ? AND env_type = ? AND tag = ? AND status = ?", in.ProjectID, in.EnvType, in.Tag, ci.BuildSuccess).
		Count(&cnt).Error; err != nil {
		return nil, err
	}
	if cnt == 0 {
		return nil, fmt.Errorf("标签 %s 在%s环境没有已通过 CI 的构建记录", in.Tag, envLabel(in.EnvType))
	}
	target, err := s.envTarget(ctx, in.ProjectID, in.EnvType)
	if err != nil {
		return nil, err
	}
	// P8-M3.2 双轨载体：k3s 目标走 K8s API 分流（零 SSH），compose 原路径不变
	if target.Runtime == "k3s" {
		return s.executeK3s(ctx, in, p, target, operator, rollbackOf)
	}

	yamlContent, err := s.fetchCompose(ctx, p, in.Tag)
	if err != nil {
		return nil, err
	}

	// 私有 registry：部署前在目标机 docker login（namespace 隔离由镜像路径
	// 中的 namespace 段保证——平台只按项目绑定的 registry 地址认证）。
	// prod 走蓝绿后台执行，登录/登出必须由后台任务自己管理——本函数立即返回，
	// 这里的 defer logout 会在后台 pull 之前清掉目标机凭据（v0.12.0 审计严重项 6：
	// 私有 registry 的 prod 发布必 401）。
	reg, err := s.registryFor(ctx, in.ProjectID)
	if err != nil {
		return nil, err
	}

	// 正式环境走蓝绿链路（第四阶段 M2，任务化）：双隔离域 + 健康门禁 + 整份 conf
	// 切换；立即返回 running 记录，后台执行、阶段日志轮询可见
	if in.EnvType == "prod" {
		return s.startBlueGreen(ctx, p, target, in, operator, yamlContent, reg)
	}

	if reg != nil && reg.HasCred {
		if err := s.res.RegistryLogin(ctx, target.ServerID, strings.SplitN(reg.Address, "/", 2)[0], reg.Username, reg.Password); err != nil {
			return nil, fmt.Errorf("目标机 registry 登录失败: %w", err)
		}
		defer s.res.RegistryLogout(ctx, target.ServerID, reg.Address)
	}

	rel := Release{
		ProjectID: in.ProjectID, EnvType: in.EnvType, Tag: in.Tag,
		ServerID: target.ServerID, Runtime: "compose",
		ReleaseBy: operator, Status: ReleaseFailed,
		RollbackOf: rollbackOfRef(rollbackOf),
	}
	deployName := fmt.Sprintf("%s-%s", strx.NormalizeName(p.Name), in.EnvType)
	if in.EnvType == "canary" {
		// 2026-11-08 命名统一：灰度域版本化 <proj>-canary-<yyyymmdd-缩写>（域=tag），
		// 容器名随域携带版本；新灰度部署后自动销毁上一版本旧域（流量不冲突）
		deployName = strx.NormalizeName(p.Name) + "-" + strx.NormalizeName(in.Tag)
	}

	startedAt := time.Now()
	// {env} 挂载段（v0.14.1）：prod→prod；canary 引用 prod 配置同源
	envSeg := "prod"
	out, _, container, err := s.res.DeployComposeToImageEnv(ctx, target.ServerID, deployName, yamlContent,
		s.imageFor(ctx, in.ProjectID, in.Tag), envSeg)
	rel.DurationSecs = int(time.Since(startedAt).Seconds())
	rel.Output = truncate(out, 8000)
	if err == nil {
		rel.Status = ReleaseSuccess
		if in.EnvType == "canary" {
			if old := s.recordCanaryDomain(ctx, in.ProjectID, deployName, container); old != "" && old != deployName {
				if dOut, dErr := s.res.DestroyCompose(ctx, target.ServerID, old); dErr != nil {
					rel.Output += fmt.Sprintf("\n[warn] 旧灰度域 %s 清理失败（可手动清理）: %v\n%s", old, dErr, dOut)
				} else {
					rel.Output += fmt.Sprintf("\n已清理上一版本灰度域 %s", old)
				}
			}
			// 版本化灰度域：容器名随域变化，部署后必须重写 conf（upstream 指向新城容器），
			// 失败告警不阻断（容器已健康，conf 旧值指向旧域会被上面的清理移除——残留断流风险，提示手动修复）
			conf, cErr := s.canary.FullConf(ctx, in.ProjectID, s.ActiveColor(ctx, in.ProjectID))
			if cErr != nil {
				rel.Output += fmt.Sprintf("\n[warn] 灰度 conf 渲染失败（分流暂不可用）: %v", cErr)
			} else if _, wErr := s.res.DeployNginxConf(ctx, target.ServerID, p.Name, conf); wErr != nil {
				rel.Output += fmt.Sprintf("\n[warn] 灰度 conf 写入失败（分流暂不可用，可重发灰度或手动修复）: %v", wErr)
			} else {
				rel.Output += "\n已更新灰度 conf（upstream 指向新版本域）"
			}
		}
	} else {
		rel.Output = rel.Output + "\n" + err.Error()
	}
	if err := s.db.WithContext(ctx).Create(&rel).Error; err != nil {
		return nil, err
	}
	s.notifyRelease(ctx, p, &rel)
	return &rel, nil
}

// fetchCompose 按标签从 git 托管 raw 接口取部署描述文件（gitea/gitee 按 provider 路由）。
func (s *Service) fetchCompose(ctx context.Context, p *projectRow, tag string) (string, error) {
	if p.ComposePath == "" {
		return "", errors.New("项目未配置部署描述文件路径（项目管理 → 配置）")
	}
	body, err := s.ci.RawFile(ctx, p.Provider, p.RepoPath, p.ComposePath, tag)
	if err != nil {
		return "", fmt.Errorf("取部署描述失败（检查文件路径 %s 与标签 %s）: %w", p.ComposePath, tag, err)
	}
	if len(body) == 0 {
		return "", fmt.Errorf("标签 %s 的部署描述文件为空", tag)
	}
	return string(body), nil
}

// Rollback 回滚 = 重新发布指定历史 release 的标签。
func (s *Service) Rollback(ctx context.Context, releaseID uint, operator string) (*Release, error) {
	var old Release
	if err := s.db.WithContext(ctx).First(&old, releaseID).Error; err != nil {
		return nil, ErrNotFound
	}
	// P8-M1：rollback_of 经 Execute 变参并入单条 INSERT（原两步写在
	// 补写失败时丢溯源关系；Execute 内含远程部署不可入事务，单条原子写是正解）
	return s.Execute(ctx, ReleaseInput{
		ProjectID: old.ProjectID, EnvType: old.EnvType, Tag: old.Tag,
	}, operator, old.ID)
}

// List 分页发布历史。
// DB 暴露只读查询入口（handler 单条查询用；写操作仍收在 service 内）。
func (s *Service) DB() *gorm.DB { return s.db }

// List 发布记录分页。scopeIDs（P7-M1 项目范围）非 nil 时仅返回授权项目，
// 与 projectID 过滤叠加（scoped 用户查未授权项目 → 空集）。
func (s *Service) List(ctx context.Context, projectID uint, env string, page, size int, scopeIDs []uint) ([]Release, int64, error) {
	tx := s.db.WithContext(ctx).Model(&Release{})
	if scopeIDs != nil {
		tx = tx.Where("project_id IN ?", scopeIDs)
	}
	if projectID > 0 {
		tx = tx.Where("project_id = ?", projectID)
	}
	if env != "" {
		tx = tx.Where("env_type = ?", env)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if page <= 0 {
		page = 1
	}
	if size <= 0 || size > 100 {
		size = 20
	}
	var list []Release
	if err := tx.Order("id DESC").Offset((page - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// PassedTags 已通过 CI 的标签（发布抽屉用）。
func (s *Service) PassedTags(ctx context.Context, projectID uint, env string) ([]string, error) {
	var tags []string
	if err := s.db.WithContext(ctx).
		Table("builds").
		Where("project_id = ? AND env_type = ? AND status = ?", projectID, env, ci.BuildSuccess).
		Order("id DESC").Limit(100).
		Pluck("tag", &tags).Error; err != nil {
		return nil, err
	}
	return tags, nil
}

func (s *Service) notifyRelease(ctx context.Context, p *projectRow, rel *Release) {
	title := fmt.Sprintf("发布%s：%s %s", map[string]string{ReleaseSuccess: "成功", ReleaseFailed: "失败"}[rel.Status], p.Name, rel.Tag)
	if s.notify != nil {
		key := fmt.Sprintf("release:%d", rel.ID)
		if rel.ID == 0 {
			key = fmt.Sprintf("release:promote:%d", time.Now().UnixNano())
		}
		s.notify.RecordProjectEvent(ctx, notify.ProjectEvent{ProjectID: p.ID,
			Key: key, Title: title, Message: envLabel(rel.EnvType)})
	}
	var groupID *uint
	switch rel.EnvType {
	case "prod":
		groupID = p.NotifyProdGroupID
	case "canary":
		groupID = p.NotifyCanaryGroupID
	case "test":
		groupID = p.NotifyTestGroupID
	}
	if groupID == nil || s.notify == nil {
		return
	}
	g, err := s.notify.Get(ctx, *groupID)
	if err != nil {
		return
	}
	content := fmt.Sprintf("环境：%s\n操作人：%s\n标签：%s", envLabel(rel.EnvType), rel.ReleaseBy, rel.Tag)
	go func() {
		_ = s.notify.Send(context.WithoutCancel(ctx), g, title, content)
	}()
}

func envLabel(env string) string {
	switch env {
	case "prod":
		return "正式"
	case "test":
		return "测试"
	default:
		return "灰度"
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strx.Truncate(s, n) // rune 安全（防切碎中文，v0.12.0 审计）
}

// rollbackOfRef 变参 → 可空指针（无参 = nil，普通发布）。
func rollbackOfRef(rollbackOf []uint) *uint {
	if len(rollbackOf) == 0 || rollbackOf[0] == 0 {
		return nil
	}
	return &rollbackOf[0]
}

// ---- P8-M3.2 k3s 分流 ----

// K3sDeployer k3s 部署能力投影（k3s.Service 实现；release→k3s 单向依赖无环）。
type K3sDeployer interface {
	Deploy(ctx context.Context, in k3smod.DeployInput) (*k3smod.DeployResult, error)
	Rollback(ctx context.Context, clusterID uint, namespace, name string) error
}

// SetK3sDeployer 注入 k3s 部署器（nil = k3s 目标仍报"未支持"）。
func (s *Service) SetK3sDeployer(d K3sDeployer) { s.k3s = d }

// K3sHostFor Ingress host（k3s 集群域名 + 项目-环境命名，由 k3s 模块约定）。
func k3sNamespace(projectID uint, env string) string {
	return fmt.Sprintf("custos-%d-%s", projectID, env)
}

func (s *Service) executeK3s(ctx context.Context, in ReleaseInput, p *projectRow, target *EnvTargetRow, operator string, rollbackOf []uint) (*Release, error) {
	if s.k3s == nil {
		return nil, fmt.Errorf("部署目标运行时 k3s 尚未支持（k3s 模块未装配）")
	}
	if target.ClusterID == 0 {
		return nil, fmt.Errorf("k3s 部署目标未绑定集群（环境管理 → 目标设置）")
	}
	yamlContent, err := s.fetchCompose(ctx, p, in.Tag)
	if err != nil {
		return nil, err
	}
	res, err := s.k3s.Deploy(ctx, k3smod.DeployInput{
		ClusterID:   target.ClusterID,
		Namespace:   k3sNamespace(in.ProjectID, in.EnvType),
		ComposeYAML: yamlContent,
		Tag:         in.Tag,
		ProjectName: p.Name,
		EnvType:     in.EnvType, // Host=<project>-<env>.<cluster.Domain>（k3s 模块组装）
	})
	if err != nil {
		return nil, fmt.Errorf("k3s 部署失败: %w", err)
	}
	rel := Release{
		ProjectID: in.ProjectID, EnvType: in.EnvType, Tag: in.Tag, ReleaseBy: operator,
		Runtime: "k3s", Status: ReleaseSuccess,
		Output:       fmt.Sprintf("k3s 部署 %s/%s 镜像 %s（rollout %ds）", res.Namespace, res.Name, res.Image, res.RolloutSecs),
		DurationSecs: res.RolloutSecs,
	}
	if len(rollbackOf) > 0 && rollbackOf[0] > 0 {
		rel.RollbackOf = &rollbackOf[0]
	}
	if err := s.db.WithContext(ctx).Create(&rel).Error; err != nil {
		return nil, err
	}
	s.notifyRelease(ctx, p, &rel)
	return &rel, nil
}
