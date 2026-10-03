// Package configs 配置文件管理（P5 M4，docs/plan-phase5-services.md M4）。
// 文件级配置的真相源：存储/版本/审计/脱敏/下发（复用 resources 的 SSH+SFTP 通道）。
// 热更边界（UI 明示）：平台只负责文件落盘与可选生效动作（SIGHUP/refresh 端点/
// 容器重启），不承诺配置中心 SDK 级热更体验。K/V 拉取式归 AgileConfig（P6）。
package configs

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/pkg/crypto"
)

var ErrNotFound = errors.New("配置文件不存在")

// 文件格式（决定 Monaco 语言与脱敏行匹配）。
const (
	FormatYAML = "yaml"
	FormatJSON = "json"
	FormatTOML = "toml"
	FormatEnv  = "env"
	FormatINI  = "ini"
)

var validFormats = map[string]bool{
	FormatYAML: true, FormatJSON: true, FormatTOML: true, FormatEnv: true, FormatINI: true,
}

// 生效动作（下发后可选执行）。
const (
	ApplyNone    = "none"    // 仅落盘
	ApplySighup  = "sighup"  // pkill -HUP -f <target>
	ApplyHTTP    = "http"    // POST <target>（refresh 端点）
	ApplyRestart = "restart" // docker restart <target>
)

var validActions = map[string]bool{
	ApplyNone: true, ApplySighup: true, ApplyHTTP: true, ApplyRestart: true,
}

// 版本来源。
const (
	SourceEdit     = "edit"   // 手动编辑保存
	SourceDeploy   = "deploy" // 下发时快照
	SourceRollback = "rollback"
	SourceEnvSync  = "env_sync" // 环境同步覆盖/建档
)

// File 配置文件定义。
type File struct {
	ID uint `gorm:"primarykey" json:"id"`
	// 定义
	Name     string `gorm:"size:64;not null" json:"name"`
	ServerID uint   `gorm:"index;not null" json:"serverId"`
	// R2 文件管理器：项目归属 + 层级路径（首段=环境 prod/canary/test，
	// 更深层自由；旧数据 project_id=0 树根展示待迁移）
	ProjectID   uint      `gorm:"index;not null;default:0" json:"projectId"`
	RelPath     string    `gorm:"size:512" json:"relPath"`
	Path        string    `gorm:"size:512;not null" json:"path"` // 远端绝对路径
	Format      string    `gorm:"size:8;not null;default:yaml" json:"format"`
	Sensitive   bool      `gorm:"not null;default:false" json:"sensitive"`
	Content     string    `gorm:"type:text" json:"-"` // 当前内容（内容经专用接口取，列表瘦身）
	ApplyAction string    `gorm:"size:8;not null;default:none" json:"applyAction"`
	ApplyTarget string    `gorm:"size:512" json:"applyTarget"` // 进程名 / refresh URL / 容器名
	Remark      string    `gorm:"size:255" json:"remark"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (File) TableName() string { return "config_files" }

// Version 全量快照（编辑/下发/回滚各存一份；回滚 = 内容回到历史版本）。
type Version struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	FileID    uint      `gorm:"index;not null" json:"fileId"`
	Content   string    `gorm:"type:text" json:"-"`
	Hash      string    `gorm:"size:16;index" json:"hash"` // 内容指纹（前 16 hex）
	Source    string    `gorm:"size:16" json:"source"`
	CreatedBy string    `gorm:"size:128" json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

func (Version) TableName() string { return "config_versions" }

func Models() []any { return []any{&File{}, &Version{}} }

// Executor resources.Service 的最小投影（避免反向依赖）。
type Executor interface {
	SftpWrite(serverID uint, name string, content []byte) error
	RunCommandOn(ctx context.Context, serverID uint, cmd, stdin string, timeout time.Duration) (string, error)
	RecordEvent(ctx context.Context, serverID uint, typ, msg string)
}

// Service 配置中心。
type Service struct {
	db     *gorm.DB
	exec   Executor
	cipher *crypto.Cipher
}

func NewService(db *gorm.DB, exec Executor, cipher *crypto.Cipher) *Service {
	return &Service{db: db, exec: exec, cipher: cipher}
}

// ---- CRUD ----

type SaveFileInput struct {
	Name        string `json:"name" binding:"required,max=64"`
	ServerID    uint   `json:"serverId" binding:"required"`
	ProjectID   uint   `json:"projectId"`
	RelPath     string `json:"relPath" binding:"omitempty,max=512"`
	Path        string `json:"path" binding:"required,max=512"`
	Format      string `json:"format" binding:"required,oneof=yaml json toml env ini"`
	Sensitive   bool   `json:"sensitive"`
	Content     string `json:"content" binding:"max=1048576"` // 1MB（与 SFTP 上限一致）
	ApplyAction string `json:"applyAction" binding:"required,oneof=none sighup http restart"`
	ApplyTarget string `json:"applyTarget" binding:"omitempty,max=512"`
	Remark      string `json:"remark" binding:"max=255"`
}

func validateTarget(action, target string) error {
	switch action {
	case ApplySighup:
		if target == "" {
			return fmt.Errorf("SIGHUP 动作须填写目标进程名（pkill -f 匹配）")
		}
	case ApplyHTTP:
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			return fmt.Errorf("refresh 动作须填写 http(s):// 端点 URL")
		}
	case ApplyRestart:
		if target == "" {
			return fmt.Errorf("重启动作须填写容器名")
		}
	}
	return nil
}

// validateRelPath 层级路径强语义：首段=环境（prod/canary/test）且项目已配置
// 该环境部署目标（project_env_targets）；深层子目录自由（test/dev1/... 对应槽位目录）。
func (s *Service) validateRelPath(ctx context.Context, projectID uint, relPath string) error {
	relPath = strings.Trim(strings.TrimSpace(relPath), "/")
	if relPath == "" {
		return nil // 旧形态兼容（不挂层级的裸文件）
	}
	segs := strings.Split(relPath, "/")
	env := segs[0]
	if env != "prod" && env != "canary" && env != "test" {
		return fmt.Errorf("层级路径首段须为环境名（prod/canary/test），got %q", env)
	}
	if projectID == 0 {
		return fmt.Errorf("层级路径需要先选择归属项目")
	}
	var n int64
	if err := s.db.WithContext(ctx).Table("project_env_targets").
		Where("project_id = ? AND env_type = ?", projectID, env).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("项目未配置 %s 环境的部署目标（先在环境管理绑定）", env)
	}
	for _, seg := range segs {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, " \\") {
			return fmt.Errorf("路径段不合法: %q", seg)
		}
	}
	return nil
}

func (s *Service) Create(ctx context.Context, in SaveFileInput, by string) (*File, error) {
	if err := validateTarget(in.ApplyAction, in.ApplyTarget); err != nil {
		return nil, err
	}
	if err := s.validateRelPath(ctx, in.ProjectID, in.RelPath); err != nil {
		return nil, err
	}
	f := File{Name: in.Name, ServerID: in.ServerID, Path: in.Path, Format: in.Format,
		Sensitive: in.Sensitive, Content: in.Content, ApplyAction: in.ApplyAction,
		ApplyTarget: in.ApplyTarget, Remark: in.Remark,
		ProjectID: in.ProjectID, RelPath: strings.Trim(strings.TrimSpace(in.RelPath), "/")}
	if err := s.db.WithContext(ctx).Create(&f).Error; err != nil {
		return nil, err
	}
	if in.Content != "" {
		s.snapVersion(ctx, f.ID, in.Content, SourceEdit, by)
	}
	return &f, nil
}

func (s *Service) Update(ctx context.Context, id uint, in SaveFileInput) error {
	var f File
	if err := s.db.WithContext(ctx).First(&f, id).Error; err != nil {
		return ErrNotFound
	}
	if err := validateTarget(in.ApplyAction, in.ApplyTarget); err != nil {
		return err
	}
	if err := s.validateRelPath(ctx, in.ProjectID, in.RelPath); err != nil {
		return err
	}
	updates := map[string]any{
		"name": in.Name, "server_id": in.ServerID, "path": in.Path, "format": in.Format,
		"sensitive": in.Sensitive, "apply_action": in.ApplyAction,
		"apply_target": in.ApplyTarget, "remark": in.Remark,
		"project_id": in.ProjectID, "rel_path": strings.Trim(strings.TrimSpace(in.RelPath), "/"),
	}
	if err := s.db.WithContext(ctx).Model(&File{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return err
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&File{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	s.db.WithContext(ctx).Where("file_id = ?", id).Delete(&Version{})
	return nil
}

// FileOut 列表瘦身视图。
type FileOut struct {
	File
	HasContent bool `json:"hasContent"`
}

func toOut(f File) FileOut { return FileOut{File: f, HasContent: f.Content != ""} }

func (s *Service) List(ctx context.Context) ([]FileOut, error) {
	var fs []File
	if err := s.db.WithContext(ctx).Order("id").Find(&fs).Error; err != nil {
		return nil, err
	}
	out := make([]FileOut, len(fs))
	for i, f := range fs {
		f.Content = ""
		out[i] = toOut(f)
	}
	return out, nil
}

// ---- 内容（脱敏/明文） ----

// sensitiveLineRe 匹配带值行（yaml `k: v` / env/ini `k=v`），值整体替换。
var sensitiveLineRe = regexp.MustCompile(`^(\s*[^\s#=:]+(\s*[:=]\s*))(.*\S.*)$`)

// MaskSensitive 按行脱敏：保留键与结构，值替换为 ***。
func MaskSensitive(content string) string {
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		if m := sensitiveLineRe.FindStringSubmatch(l); m != nil {
			lines[i] = m[1] + "***"
		}
	}
	return strings.Join(lines, "\n")
}

// GetContent 取内容：敏感文件默认脱敏；reveal=true 返回明文并落审计。
func (s *Service) GetContent(ctx context.Context, id uint, reveal bool, by string) (string, *File, error) {
	var f File
	if err := s.db.WithContext(ctx).First(&f, id).Error; err != nil {
		return "", nil, ErrNotFound
	}
	if f.Sensitive && !reveal {
		return MaskSensitive(f.Content), &f, nil
	}
	if f.Sensitive && reveal && s.exec != nil {
		s.exec.RecordEvent(ctx, f.ServerID, "config_reveal", fmt.Sprintf("配置 %q 明文查看 by %s", f.Name, by))
	}
	return f.Content, &f, nil
}

// SaveContent 编辑保存（新版本快照）。
func (s *Service) SaveContent(ctx context.Context, id uint, content string, by string) error {
	var f File
	if err := s.db.WithContext(ctx).First(&f, id).Error; err != nil {
		return ErrNotFound
	}
	if err := s.db.WithContext(ctx).Model(&File{}).Where("id = ?", id).
		Update("content", content).Error; err != nil {
		return err
	}
	s.snapVersion(ctx, id, content, SourceEdit, by)
	return nil
}

func (s *Service) snapVersion(ctx context.Context, fileID uint, content, source, by string) {
	v := Version{FileID: fileID, Content: content, Hash: contentHash(content), Source: source, CreatedBy: by}
	s.db.WithContext(ctx).Create(&v)
	// 版本保留上限：每文件 50 个，超限删最旧
	var ids []uint
	s.db.WithContext(ctx).Model(&Version{}).Where("file_id = ?", fileID).
		Order("id DESC").Offset(50).Limit(100).Pluck("id", &ids)
	if len(ids) > 0 {
		s.db.WithContext(ctx).Where("id IN ?", ids).Delete(&Version{})
	}
}

func contentHash(s string) string {
	return fmt.Sprintf("%x", fastSum([]byte(s)))[:16]
}

// ---- 版本 ----

type VersionOut struct {
	Version
	HasContent bool `json:"hasContent"`
}

func (s *Service) ListVersions(ctx context.Context, fileID uint) ([]VersionOut, error) {
	var vs []Version
	if err := s.db.WithContext(ctx).Where("file_id = ?", fileID).
		Order("id DESC").Limit(50).Find(&vs).Error; err != nil {
		return nil, err
	}
	out := make([]VersionOut, len(vs))
	for i, v := range vs {
		out[i] = VersionOut{Version: v, HasContent: v.Content != ""}
	}
	return out, nil
}

// GetVersionContent 版本内容（敏感文件同样默认脱敏）。
func (s *Service) GetVersionContent(ctx context.Context, versionID uint, reveal bool) (string, *Version, error) {
	var v Version
	if err := s.db.WithContext(ctx).First(&v, versionID).Error; err != nil {
		return "", nil, gorm.ErrRecordNotFound
	}
	var f File
	if err := s.db.WithContext(ctx).First(&f, v.FileID).Error; err != nil {
		return "", nil, ErrNotFound
	}
	if f.Sensitive && !reveal {
		return MaskSensitive(v.Content), &v, nil
	}
	return v.Content, &v, nil
}

// Rollback 回滚：内容回到指定版本（记 rollback 快照，不自动下发——下发独立按钮）。
func (s *Service) Rollback(ctx context.Context, fileID, versionID uint, by string) error {
	var v Version
	if err := s.db.WithContext(ctx).Where("id = ? AND file_id = ?", versionID, fileID).
		First(&v).Error; err != nil {
		return gorm.ErrRecordNotFound
	}
	if err := s.db.WithContext(ctx).Model(&File{}).Where("id = ?", fileID).
		Update("content", v.Content).Error; err != nil {
		return err
	}
	s.snapVersion(ctx, fileID, v.Content, SourceRollback, by)
	return nil
}

// ---- 下发 ----

// Deploy 下发当前内容到目标机：SFTP 写入（自带 .bak 备份）→ 可选生效动作 → 审计。
func (s *Service) Deploy(ctx context.Context, id uint, by string) error {
	var f File
	if err := s.db.WithContext(ctx).First(&f, id).Error; err != nil {
		return ErrNotFound
	}
	if f.Content == "" {
		return fmt.Errorf("内容为空，先保存再下发")
	}
	if s.exec == nil {
		return fmt.Errorf("SSH 执行器未注入")
	}
	if err := s.exec.SftpWrite(f.ServerID, f.Path, []byte(f.Content)); err != nil {
		s.exec.RecordEvent(ctx, f.ServerID, "config_deploy", fmt.Sprintf("配置 %q 下发失败: %v", f.Name, err))
		return fmt.Errorf("SFTP 写入失败: %w", err)
	}
	s.snapVersion(ctx, id, f.Content, SourceDeploy, by)

	actionNote := "仅落盘"
	if f.ApplyAction != ApplyNone {
		// 生效动作脱离请求 ctx：重启容器等动作最长数分钟，
		// HTTP 断开不得把容器重启拦在半途
		if err := s.applyAction(context.WithoutCancel(ctx), &f); err != nil {
			s.exec.RecordEvent(ctx, f.ServerID, "config_deploy",
				fmt.Sprintf("配置 %q 已落盘但生效动作失败: %v", f.Name, err))
			return fmt.Errorf("文件已落盘，但生效动作失败: %w", err)
		}
		actionNote = "生效动作 " + f.ApplyAction + " 完成"
	}
	s.exec.RecordEvent(ctx, f.ServerID, "config_deploy",
		fmt.Sprintf("配置 %q 下发 %s by %s（%s）", f.Name, f.Path, by, actionNote))
	return nil
}

func (s *Service) applyAction(ctx context.Context, f *File) error {
	switch f.ApplyAction {
	case ApplySighup:
		out, err := s.exec.RunCommandOn(ctx, f.ServerID,
			fmt.Sprintf("pkill -HUP -f %q", f.ApplyTarget), "", 30*time.Second)
		if err != nil {
			return fmt.Errorf("SIGHUP %q: %v（%s）", f.ApplyTarget, err, truncate(out, 200))
		}
		return nil
	case ApplyHTTP:
		return postRefresh(ctx, f.ApplyTarget)
	case ApplyRestart:
		out, err := s.exec.RunCommandOn(ctx, f.ServerID,
			"docker restart "+f.ApplyTarget, "", 5*time.Minute)
		if err != nil {
			return fmt.Errorf("docker restart %s: %v（%s）", f.ApplyTarget, err, truncate(out, 200))
		}
		return nil
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// EnvSyncInput 环境同步：把源环境（可选子前缀）下的配置文件内容完整同步到
// 目标环境——目标已存在则生成新版本，不存在则建档（目标主机自动取目标环境
// 的部署目标，远端路径沿用源文件）。
type EnvSyncInput struct {
	ProjectID uint   `json:"projectId" binding:"required"`
	SourceEnv string `json:"sourceEnv" binding:"required,oneof=prod canary test"`
	TargetEnv string `json:"targetEnv" binding:"required,oneof=prod canary test"`
	SubPath   string `json:"subPath" binding:"omitempty,max=512"`
}

// EnvSync 执行环境同步；返回新建/覆盖计数。内容只进版本链，不自动下发。
func (s *Service) EnvSync(ctx context.Context, in EnvSyncInput, by string) (created, updated int, err error) {
	if in.SourceEnv == in.TargetEnv {
		return 0, 0, fmt.Errorf("源与目标环境相同")
	}
	sub := strings.Trim(strings.TrimSpace(in.SubPath), "/")
	for _, seg := range strings.Split(sub, "/") {
		if seg == "" || seg == "." || seg == ".." {
			if sub == "" {
				break
			}
			return 0, 0, fmt.Errorf("子路径不合法: %q", seg)
		}
	}
	// 目标环境须已绑定部署目标（新建文件的主机来源）
	var targetServer uint
	if err := s.db.WithContext(ctx).Table("project_env_targets").
		Select("server_id").Where("project_id = ? AND env_type = ?", in.ProjectID, in.TargetEnv).
		Scan(&targetServer).Error; err != nil {
		return 0, 0, err
	}
	if targetServer == 0 {
		return 0, 0, fmt.Errorf("项目未配置 %s 环境的部署目标", in.TargetEnv)
	}
	prefix := in.SourceEnv
	if sub != "" {
		prefix = in.SourceEnv + "/" + sub
	}
	var sources []File
	if err := s.db.WithContext(ctx).
		Where("project_id = ? AND rel_path LIKE ?", in.ProjectID, prefix+"/%").
		Find(&sources).Error; err != nil {
		return 0, 0, err
	}
	if len(sources) == 0 {
		return 0, 0, fmt.Errorf("源路径 %s 下没有配置文件", prefix)
	}
	for _, src := range sources {
		rest := strings.TrimPrefix(src.RelPath, in.SourceEnv) // /xxx/yyy.yaml
		dstRel := in.TargetEnv + rest
		var dst File
		e := s.db.WithContext(ctx).Where("project_id = ? AND rel_path = ?", in.ProjectID, dstRel).First(&dst).Error
		if e == nil {
			dst.Content = src.Content
			if err := s.db.WithContext(ctx).Model(&File{}).Where("id = ?", dst.ID).
				Update("content", src.Content).Error; err != nil {
				return created, updated, err
			}
			s.snapVersion(ctx, dst.ID, src.Content, SourceEnvSync, by)
			updated++
		} else {
			nf := src
			nf.ID = 0
			nf.RelPath = dstRel
			nf.ServerID = targetServer
			if err := s.db.WithContext(ctx).Create(&nf).Error; err != nil {
				return created, updated, err
			}
			s.snapVersion(ctx, nf.ID, src.Content, SourceEnvSync, by)
			created++
		}
	}
	return created, updated, nil
}
