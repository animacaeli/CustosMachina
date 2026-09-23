package release

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/ci"
	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/modules/resources"
)

var ErrNotFound = errors.New("发布记录不存在")

type Service struct {
	db     *gorm.DB
	ci     *ci.Service
	res    *resources.Service
	notify *notify.Service
}

func NewService(db *gorm.DB, ciSvc *ci.Service, resSvc *resources.Service, ntfy *notify.Service) *Service {
	return &Service{db: db, ci: ciSvc, res: resSvc, notify: ntfy}
}

// projectRow 只读 projects 所需列（避免跨模块循环依赖）。
type projectRow struct {
	ID                  uint
	Name                string
	RepoPath            string
	ComposePath         string
	NotifyProdGroupID   *uint
	NotifyCanaryGroupID *uint
}

func (s *Service) project(ctx context.Context, id uint) (*projectRow, error) {
	var p projectRow
	if err := s.db.WithContext(ctx).
		Table("projects").
		Select("id, name, repo_path, compose_path, notify_prod_group_id, notify_canary_group_id").
		Where("id = ?", id).First(&p).Error; err != nil {
		return nil, errors.New("项目不存在")
	}
	return &p, nil
}

// EnvTarget 项目某环境的部署目标。
type EnvTargetRow struct {
	ServerID uint
	Runtime  string
}

func (s *Service) envTarget(ctx context.Context, projectID uint, env string) (*EnvTargetRow, error) {
	var t EnvTargetRow
	if err := s.db.WithContext(ctx).
		Table("project_env_targets").
		Select("server_id, runtime").
		Where("project_id = ? AND env_type = ?", projectID, env).
		First(&t).Error; err != nil {
		return nil, fmt.Errorf("项目未配置 %s 环境的部署目标", env)
	}
	return &t, nil
}

// ---- 发布 ----

type ReleaseInput struct {
	ProjectID uint   `json:"projectId" binding:"required"`
	EnvType   string `json:"envType" binding:"required,oneof=prod canary"`
	Tag       string `json:"tag" binding:"required,max=128"`
}

// Execute 校验"已通过 CI"→ 取 compose 文件 → 部署 → 落发布历史 + 通知。
func (s *Service) Execute(ctx context.Context, in ReleaseInput, operator string) (*Release, error) {
	p, err := s.project(ctx, in.ProjectID)
	if err != nil {
		return nil, err
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
	if target.Runtime != "" && target.Runtime != "compose" {
		return nil, fmt.Errorf("部署目标运行时 %s 尚未支持（MVP 仅 compose）", target.Runtime)
	}

	yamlContent, err := s.fetchCompose(ctx, p, in.Tag)
	if err != nil {
		return nil, err
	}

	rel := Release{
		ProjectID: in.ProjectID, EnvType: in.EnvType, Tag: in.Tag,
		ServerID: target.ServerID, Runtime: "compose",
		ReleaseBy: operator, Status: ReleaseFailed,
	}
	deployName := fmt.Sprintf("%s-%s", normalizeName(p.Name), in.EnvType)
	out, _, err := s.res.DeployComposeTo(ctx, target.ServerID, deployName, yamlContent)
	rel.Output = truncate(out, 8000)
	if err == nil {
		rel.Status = ReleaseSuccess
	} else {
		rel.Output = rel.Output + "\n" + err.Error()
	}
	if err := s.db.WithContext(ctx).Create(&rel).Error; err != nil {
		return nil, err
	}
	s.notifyRelease(ctx, p, &rel)
	return &rel, nil
}

// fetchCompose 按标签从 gitea raw 接口取部署描述文件。
func (s *Service) fetchCompose(ctx context.Context, p *projectRow, tag string) (string, error) {
	if p.ComposePath == "" {
		return "", errors.New("项目未配置部署描述文件路径（项目管理 → 配置）")
	}
	client, base, err := s.ci.RawClient(ctx, p.RepoPath)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s/api/v1/repos/%s/raw/%s?ref=%s", base, p.RepoPath, strings.TrimPrefix(p.ComposePath, "/"), tag)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.HTTPDo(req)
	if err != nil {
		return "", fmt.Errorf("取部署描述失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("取部署描述失败：gitea %d（检查文件路径 %s 与标签 %s）", resp.StatusCode, p.ComposePath, tag)
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
	rel, err := s.Execute(ctx, ReleaseInput{
		ProjectID: old.ProjectID, EnvType: old.EnvType, Tag: old.Tag,
	}, operator)
	if err != nil {
		return nil, err
	}
	rel.RollbackOf = &old.ID
	if err := s.db.WithContext(ctx).Model(rel).Update("rollback_of", old.ID).Error; err != nil {
		return nil, err
	}
	return rel, nil
}

// List 分页发布历史。
func (s *Service) List(ctx context.Context, projectID uint, env string, page, size int) ([]Release, int64, error) {
	tx := s.db.WithContext(ctx).Model(&Release{})
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
	var groupID *uint
	switch rel.EnvType {
	case "prod":
		groupID = p.NotifyProdGroupID
	case "canary":
		groupID = p.NotifyCanaryGroupID
	}
	if groupID == nil || s.notify == nil {
		return
	}
	g, err := s.notify.Get(ctx, *groupID)
	if err != nil {
		return
	}
	title := fmt.Sprintf("发布%s：%s %s", map[string]string{ReleaseSuccess: "成功", ReleaseFailed: "失败"}[rel.Status], p.Name, rel.Tag)
	content := fmt.Sprintf("环境：%s\n操作人：%s\n标签：%s", envLabel(rel.EnvType), rel.ReleaseBy, rel.Tag)
	go func() {
		_ = s.notify.Send(context.WithoutCancel(ctx), g, title, content)
	}()
}

func normalizeName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, " ", "-")
	// 白名单外字符替换掉，确保可作 compose project 名
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

func envLabel(env string) string {
	if env == "prod" {
		return "正式"
	}
	return "灰度"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
