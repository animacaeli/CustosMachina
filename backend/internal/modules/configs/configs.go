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
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/projects"
	"github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/shellx"
	"github.com/custos-machina/backend/internal/pkg/strx"
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
	// R2 文件管理器：项目归属 + 层级路径。环境语义（2026-11-09 返工对齐，
	// 迁移 0011）：首段 = prod 或槽位名 dev1..devN（N=项目 TestSlotCount），
	// 深层子目录自由；canary 不再有独立目录——全面引用 prod（读侧映射）。
	ProjectID uint   `gorm:"index;not null;default:0" json:"projectId"`
	RelPath   string `gorm:"size:512" json:"relPath"`
	Path      string `gorm:"size:512;not null" json:"path"` // 远端绝对路径
	Format    string `gorm:"size:8;not null;default:yaml" json:"format"`
	// Sensitive 已废弃（2026-11-09）：脱敏改字段级自动识别（键名含
	// token/secret/pwd/password），列保留防 schema 迁移，恒 false。
	Sensitive   bool      `gorm:"not null;default:false" json:"-"`
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

func Models() []any { return []any{&File{}, &Version{}, &PullToken{}, &AgileApp{}} }

// Executor resources.Service 的最小投影（避免反向依赖）。
type Executor interface {
	SftpWrite(serverID uint, name string, content []byte) error
	RunCommandOn(ctx context.Context, serverID uint, cmd, stdin string, timeout time.Duration) (string, error)
	RecordEvent(ctx context.Context, serverID uint, typ, msg string)
}

// Service 配置中心（proj：槽位首段校验联动项目 TestSlotCount）。
type Service struct {
	db     *gorm.DB
	exec   Executor
	cipher *crypto.Cipher
	proj   projects.Reader
}

func NewService(db *gorm.DB, exec Executor, cipher *crypto.Cipher, proj projects.Reader) *Service {
	return &Service{db: db, exec: exec, cipher: cipher, proj: proj}
}

// ---- CRUD ----

type SaveFileInput struct {
	Name      string `json:"name" binding:"required,max=64"`
	ServerID  uint   `json:"serverId" binding:"required"`
	ProjectID uint   `json:"projectId"`
	RelPath   string `json:"relPath" binding:"omitempty,max=512"`
	Path      string `json:"path" binding:"required,max=512"`
	Format    string `json:"format" binding:"required,oneof=yaml json toml env ini"`
	Content   string `json:"content" binding:"max=1048576"` // 1MB（与 SFTP 上限一致）
	// Sensitive 已移除（2026-11-09）：脱敏按字段名自动识别；旧载荷多传的字段被忽略
	ApplyAction string `json:"applyAction" binding:"required,oneof=none sighup http restart"`
	ApplyTarget string `json:"applyTarget" binding:"omitempty,max=512"`
	Remark      string `json:"remark" binding:"max=255"`
}

// 生效目标白名单（v0.12.0 审计严重项 3：此前只查非空，target 会拼进远端 shell）。
// SIGHUP 目标是 pkill -f 的匹配串（可为绝对路径）；restart 目标是容器名。
var (
	sighupTargetRe  = regexp.MustCompile(`^/?[a-zA-Z0-9][a-zA-Z0-9_.:/+-]*$`)
	restartTargetRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)
)

func validateTarget(action, target string) error {
	switch action {
	case ApplySighup:
		if target == "" {
			return fmt.Errorf("SIGHUP 动作须填写目标进程名（pkill -f 匹配）")
		}
		if !sighupTargetRe.MatchString(target) {
			return fmt.Errorf("SIGHUP 目标进程名含非法字符（仅允许字母数字与 . : / + - _）")
		}
	case ApplyHTTP:
		if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
			return fmt.Errorf("refresh 动作须填写 http(s):// 端点 URL")
		}
	case ApplyRestart:
		if target == "" {
			return fmt.Errorf("重启动作须填写容器名")
		}
		if !restartTargetRe.MatchString(target) {
			return fmt.Errorf("重启目标容器名含非法字符（仅允许字母数字与 . _ -）")
		}
	}
	return nil
}

// ---- 环境语义（2026-11-09 返工对齐，迁移 0011） ----
// rel_path 首段 = prod 或槽位名 dev1..devN（N=项目 TestSlotCount，管理员可改）；
// canary 不再有独立配置目录——全面引用 prod；旧 test/devN/... 前缀已迁移重写。

// slotSegRe 槽位名形态：dev1、dev2…（dev0 不合法，两位数起允许 dev10）。
var slotSegRe = regexp.MustCompile(`^dev[1-9][0-9]*$`)

func isSlotSeg(seg string) bool { return slotSegRe.MatchString(seg) }

func slotNum(seg string) int {
	n, _ := strconv.Atoi(strings.TrimPrefix(seg, "dev"))
	return n
}

// EnvTypeOf 环境首段 → 部署目标环境类型（project_env_targets.env_type）：
// prod→prod；devN（测试槽位）→test。canary 传入时映射 prod（引用语义）。
func EnvTypeOf(env string) string {
	switch {
	case env == "prod" || env == "canary":
		return "prod"
	case isSlotSeg(env):
		return "test"
	default:
		return ""
	}
}

// envPlaceholder 远端路径环境占位符：{env} 按文件 rel_path 首段渲染
// （prod→prod、devN→devN；canary 引用 prod 不落独立路径）。多环境文件
// 用同一路径模板（如 /opt/x/config/{env}/app.yaml），下发时各归其位；
// 路径不含占位符则原样（存量路径零影响）。
const envPlaceholder = "{env}"

// renderPath 渲染远端路径的 {env} 占位符。无层级旧形态裸文件按 prod
// （fail-safe 取最严环境，与 IsTestPath 的 fail-closed 对齐）。
func renderPath(path, relPath string) string {
	if !strings.Contains(path, envPlaceholder) {
		return path
	}
	env := firstSeg(relPath)
	if env == "" || env == "canary" {
		env = "prod"
	}
	return strings.ReplaceAll(path, envPlaceholder, env)
}

// validEnvSeg 拉取/同步/合并视图的环境入参：prod、canary（引用 prod）或槽位名。
func validEnvSeg(env string) bool { return EnvTypeOf(env) != "" }

// IsTestPath 测试族文件（首段=槽位名）——敏感字段明文查看免权限（2026-11-09
// 用户定档：测试环境任何人可看，正式环境需 config.reveal）。
// 无层级的旧形态裸文件按正式环境最严处理（fail-closed）。
func IsTestPath(relPath string) bool { return isSlotSeg(firstSeg(relPath)) }

// checkSlotCap 槽位首段上限校验（v0.14.2 起按历史峰值放宽——目录树环境层固化，
// 峰值内的 devN 存量目录可继续新建/编辑；扩容照常跟随）——validateRelPath
// 与 EnvSync 建档两路共用（防同步路径绕过上限）。
func (s *Service) checkSlotCap(ctx context.Context, projectID uint, env string) error {
	if !isSlotSeg(env) || s.proj == nil {
		return nil
	}
	v, err := s.proj.ViewByID(ctx, projectID)
	if err != nil {
		return fmt.Errorf("查询项目槽位失败: %w", err)
	}
	cap := v.TestSlotCount
	if v.TestSlotPeak > cap {
		cap = v.TestSlotPeak
	}
	if slotNum(env) > cap {
		return fmt.Errorf("槽位 %s 超出项目槽位数（当前 %d 个，历史峰值 %d）", env, v.TestSlotCount, v.TestSlotPeak)
	}
	return nil
}

// validateRelPath 层级路径强语义：首段 = prod 或槽位名 dev1..devN（联动项目
// TestSlotCount），且项目已配置对应环境部署目标（prod→prod；devN→test）；
// 深层子目录自由。
func (s *Service) validateRelPath(ctx context.Context, projectID uint, relPath string) error {
	relPath = strings.Trim(strings.TrimSpace(relPath), "/")
	if relPath == "" {
		return nil // 旧形态兼容（不挂层级的裸文件）
	}
	segs := strings.Split(relPath, "/")
	env := segs[0]
	if env != "prod" && !isSlotSeg(env) {
		return fmt.Errorf("层级路径首段须为 prod 或槽位名（dev1、dev2…），got %q", env)
	}
	if projectID == 0 {
		return fmt.Errorf("层级路径需要先选择归属项目")
	}
	if err := s.checkSlotCap(ctx, projectID, env); err != nil {
		return err
	}
	envType := EnvTypeOf(env)
	var n int64
	if err := s.db.WithContext(ctx).Table("project_env_targets").
		Where("project_id = ? AND env_type = ?", projectID, envType).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("项目未配置 %s 环境的部署目标（先在环境管理绑定）", envType)
	}
	for _, seg := range segs {
		if seg == "" || seg == "." || seg == ".." || strings.ContainsAny(seg, " \\") {
			return fmt.Errorf("路径段不合法: %q", seg)
		}
	}
	return nil
}

// validateEnvPlaceholder {env} 占位符依赖环境层级：无层级（旧形态裸文件）
// 时首段恒渲染为 prod，多环境同路径会互相覆盖——直接拒绝，早暴露。
func validateEnvPlaceholder(path, relPath string) error {
	if strings.Contains(path, envPlaceholder) && strings.Trim(strings.TrimSpace(relPath), "/") == "" {
		return fmt.Errorf("远端路径含 {env} 占位符，需要层级路径（首段 prod 或槽位名）")
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
	if err := validateEnvPlaceholder(in.Path, in.RelPath); err != nil {
		return nil, err
	}
	f := File{Name: in.Name, ServerID: in.ServerID, Path: in.Path, Format: in.Format,
		Content: in.Content, ApplyAction: in.ApplyAction,
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
	if err := validateEnvPlaceholder(in.Path, in.RelPath); err != nil {
		return err
	}
	updates := map[string]any{
		"name": in.Name, "server_id": in.ServerID, "path": in.Path, "format": in.Format,
		"apply_action": in.ApplyAction,
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

// DeleteByPrefix 按层级前缀批量删除（目录树右键「删除」，v0.14.2）：
// 删 project_id 下 rel_path 以 prefix/ 开头的全部文件及版本链。
// 前缀归一化后必须匹配至少一个文件（空删报错防手滑）；调用方已 confirm。
func (s *Service) DeleteByPrefix(ctx context.Context, projectID uint, relPath string) (int, error) {
	prefix := strings.Trim(strings.TrimSpace(relPath), "/")
	if prefix == "" {
		return 0, fmt.Errorf("删除前缀不能为空（根目录不可整体删除）")
	}
	var ids []uint
	if err := s.db.WithContext(ctx).Model(&File{}).
		Where("project_id = ? AND rel_path LIKE ?", projectID, prefix+"/%").
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, fmt.Errorf("前缀 %s 下没有配置文件", prefix)
	}
	if err := s.db.WithContext(ctx).Where("file_id IN ?", ids).Delete(&Version{}).Error; err != nil {
		return 0, err
	}
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Delete(&File{}).Error; err != nil {
		return 0, err
	}
	return len(ids), nil
}

// FileOut 列表瘦身视图。
type FileOut struct {
	File
	HasContent bool `json:"hasContent"`
	// RenderedPath {env} 占位符渲染后的实际下发路径（无占位符=原样；
	// 后端统一下发防前后端渲染漂移，前端预览直接展示）。
	RenderedPath string `json:"renderedPath"`
}

func toOut(f File) FileOut {
	return FileOut{File: f, HasContent: f.Content != "", RenderedPath: renderPath(f.Path, f.RelPath)}
}

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

// ---- 内容（字段级脱敏/明文） ----

// sensitiveWords 敏感键字表（2026-11-09 用户定档）：键名含任一子串
// （大小写不敏感）即脱敏该值——不是所有字段，仅凭据类字段。
var sensitiveWords = []string{"token", "secret", "pwd", "password"}

func isSensitiveKey(key string) bool {
	k := strings.ToLower(strings.Trim(strings.TrimSpace(key), `"'`))
	for _, w := range sensitiveWords {
		if strings.Contains(k, w) {
			return true
		}
	}
	return false
}

// sensitiveLineRe 匹配带标量值的键值行：yaml/json/toml `k: v` 与 env/ini `k=v`。
// group(1)=缩进 group(2)=键 group(3)=分隔符 group(4)=值（原分隔符保留，env 的
// = 不被改写成 yaml 的 :）。
var sensitiveLineRe = regexp.MustCompile(`^(\s*)([^\s#=:]+)(\s*[:=]\s*)(.*\S.*)$`)

// structuralValueRe 值是结构指示（对象/数组开闭、yaml block 竖线）而非标量——
// 整行替换会破坏结构，跳过（其下的缩进行键未知，同样不打码，与旧实现一致保守）。
var structuralValueRe = regexp.MustCompile(`^[{}\[\]|>][{}\[\]|>&*\s,]*$`)

// inlinePairRe 单行紧凑 JSON 等多对形态的行内键值对（key/sep/value）——
// 主正则把首对之后的内容都当值，行内第二对起会漏；此兜底逐对判定
// （sep 含两侧空格重建不丢格式；值含扁平对象/数组整段——否则对象值
// 替换后会残留闭合 } 破坏 JSON）。
var inlinePairRe = regexp.MustCompile(`("[^"]+"|'[^']+'|[^\s:={},]+)(\s*:\s*)("[^"]*"|'[^']*'|\{[^{}]*\}|\[[^\[\]]*\]|[^,}\s][^,}]*)`)

// maskSensitiveValue 值替换为 ***，保留成对引号形态（JSON 合法性）。
func maskSensitiveValue(val string) string {
	if len(val) >= 2 && (val[0] == '"' && val[len(val)-1] == '"' || val[0] == '\'' && val[len(val)-1] == '\'') {
		return string(val[0]) + "***" + string(val[len(val)-1])
	}
	return "***"
}

// jsonPairBoundary 值内出现 `"…":`/`'…':` 对边界（引号紧邻冒号）——
// 单行紧凑 JSON 多对形态的判定：整值替换会吞掉首对之后的对，须走逐对。
var jsonPairBoundary = regexp.MustCompile(`["'][^"']*:\s*["']|"\s*:`)

// MaskSensitive 字段级脱敏：仅敏感键（含 token/secret/pwd/password）的值替换
// 为 ***，其余行原样保留；无敏感键的文件输出与输入相同（幂等，兼作判定）。
// 值带成对引号（JSON 行 `"k": "v",`）时保留引号与行尾分隔（,/}）形态，
// 输出仍是合法 JSON；单行紧凑 JSON 多对行逐对兜底替换（主正则贪婪到行尾，
// 整值替换会吞后续对——2026-11-09 审核实测坐实）。
func MaskSensitive(content string) string {
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		m := sensitiveLineRe.FindStringSubmatch(l)
		multiPair := jsonPairBoundary.MatchString(l) ||
			len(inlinePairRe.FindAllStringSubmatch(l, -1)) > 1
		if m != nil && isSensitiveKey(m[2]) && !multiPair &&
			!structuralValueRe.MatchString(strings.TrimSpace(m[4])) {
			val := strings.TrimSpace(m[4])
			tail := ""
			// JSON 行尾分隔符不属于值
			if strings.HasSuffix(val, ",") || strings.HasSuffix(val, "}") {
				tail = val[len(val)-1:]
				val = val[:len(val)-1]
			}
			lines[i] = m[1] + m[2] + m[3] + maskSensitiveValue(val) + tail
			continue
		}
		if !multiPair {
			continue // 单对且键非敏感/结构值：原样
		}
		// 多对行：逐对替换敏感键（非敏感对原样；纯结构值不打码防破坏；
		// 对象/数组值整体降为字符串 "***"——保持 JSON 合法且内部值不泄漏）
		lines[i] = inlinePairRe.ReplaceAllStringFunc(l, func(pair string) string {
			pm := inlinePairRe.FindStringSubmatch(pair)
			if pm == nil || !isSensitiveKey(pm[1]) {
				return pair
			}
			v := strings.TrimSpace(pm[3])
			if structuralValueRe.MatchString(v) {
				return pair
			}
			if strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[") {
				return pm[1] + pm[2] + `"***"`
			}
			return pm[1] + pm[2] + maskSensitiveValue(v)
		})
	}
	return strings.Join(lines, "\n")
}

// hasSensitiveFields 内容是否含敏感键（与 MaskSensitive 同一判定——
// 对比实现防两套行逻辑漂移：多对行首对非敏感时曾在此误判 false 泄漏明文）。
func hasSensitiveFields(content string) bool {
	return MaskSensitive(content) != content
}

// IsSensitive 文件当前内容是否含敏感键（handler 输出 masked 标记用）。
func (s *Service) IsSensitive(ctx context.Context, id uint) (bool, error) {
	var f File
	if err := s.db.WithContext(ctx).Select("content").First(&f, id).Error; err != nil {
		return false, ErrNotFound
	}
	return hasSensitiveFields(f.Content), nil
}

// IsTestFile 文件是否测试族（槽位首段）——reveal 免权限分流判定。
func (s *Service) IsTestFile(ctx context.Context, id uint) (bool, error) {
	var f File
	if err := s.db.WithContext(ctx).Select("rel_path").First(&f, id).Error; err != nil {
		return false, ErrNotFound
	}
	return IsTestPath(f.RelPath), nil
}

// GetContent 取内容：默认敏感字段脱敏；reveal=true 返回明文并落审计
// （权限分流在 handler：devN 免 config.reveal，prod 需要）。
func (s *Service) GetContent(ctx context.Context, id uint, reveal bool, by string) (string, *File, error) {
	var f File
	if err := s.db.WithContext(ctx).First(&f, id).Error; err != nil {
		return "", nil, ErrNotFound
	}
	sensitive := hasSensitiveFields(f.Content)
	if sensitive && !reveal {
		return MaskSensitive(f.Content), &f, nil
	}
	if sensitive && reveal && s.exec != nil {
		s.exec.RecordEvent(ctx, f.ServerID, "config_reveal",
			fmt.Sprintf("配置 %q 敏感字段明文查看 by %s", f.Name, by))
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
	// deploy 来源版本默认隐藏（v0.14.2）：下发内容必然等于某编辑版本，
	// deploy 快照只制造"无内容变更的版本"污染对比链；库保留供审计直查
	if err := s.db.WithContext(ctx).Where("file_id = ? AND source != ?", fileID, SourceDeploy).
		Order("id DESC").Limit(50).Find(&vs).Error; err != nil {
		return nil, err
	}
	out := make([]VersionOut, len(vs))
	for i, v := range vs {
		out[i] = VersionOut{Version: v, HasContent: v.Content != ""}
	}
	return out, nil
}

// GetVersionContent 版本内容（含敏感键时同样默认字段级脱敏）。
func (s *Service) GetVersionContent(ctx context.Context, versionID uint, reveal bool) (string, *Version, error) {
	var v Version
	if err := s.db.WithContext(ctx).First(&v, versionID).Error; err != nil {
		return "", nil, gorm.ErrRecordNotFound
	}
	var f File
	if err := s.db.WithContext(ctx).First(&f, v.FileID).Error; err != nil {
		return "", nil, ErrNotFound
	}
	if hasSensitiveFields(v.Content) && !reveal {
		return MaskSensitive(v.Content), &v, nil
	}
	return v.Content, &v, nil
}

// VersionIsTest 版本所属文件是否测试族（reveal 免权限分流判定）。
func (s *Service) VersionIsTest(ctx context.Context, versionID uint) (bool, error) {
	var v Version
	if err := s.db.WithContext(ctx).Select("file_id").First(&v, versionID).Error; err != nil {
		return false, gorm.ErrRecordNotFound
	}
	return s.IsTestFile(ctx, v.FileID)
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
	dstPath := renderPath(f.Path, f.RelPath)
	if err := s.exec.SftpWrite(f.ServerID, dstPath, []byte(f.Content)); err != nil {
		s.exec.RecordEvent(ctx, f.ServerID, "config_deploy", fmt.Sprintf("配置 %q 下发失败: %v", f.Name, err))
		return fmt.Errorf("SFTP 写入失败: %w", err)
	}
	// 下发不落版本（v0.14.2）：版本=内容变更历史，下发内容必然等于某编辑版本，
	// deploy 快照只制造无变更版本污染对比链；动作审计走 server_events

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
		fmt.Sprintf("配置 %q 下发 %s by %s（%s）", f.Name, dstPath, by, actionNote))
	// P6-M7：env/ini 文件下发后自动同步 AgileConfig（已配置才触发，异步不阻塞）
	s.syncAgileAfterDeploy(ctx, &f, by)
	return nil
}

func (s *Service) applyAction(ctx context.Context, f *File) error {
	switch f.ApplyAction {
	case ApplySighup:
		// shellx.Quote 而非 %q：Go 字面量不转义 $()/反引号，双引号内照常展开
		out, err := s.exec.RunCommandOn(ctx, f.ServerID,
			"pkill -HUP -f "+shellx.Quote(f.ApplyTarget), "", 30*time.Second)
		if err != nil {
			return fmt.Errorf("SIGHUP %q: %v（%s）", f.ApplyTarget, err, truncate(out, 200))
		}
		return nil
	case ApplyHTTP:
		return postRefresh(ctx, f.ApplyTarget)
	case ApplyRestart:
		out, err := s.exec.RunCommandOn(ctx, f.ServerID,
			"docker restart "+shellx.Quote(f.ApplyTarget), "", 5*time.Minute)
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
	return strx.Truncate(s, n) + "..." // rune 安全（防切碎中文，v0.12.0 审计）
}

// EnvSyncInput 环境同步：把源环境（可选子前缀）下的配置文件内容完整同步到
// 目标环境（prod ↔ devN 或 devN ↔ devN'；canary 引用 prod 无独立目录，不参与）——
// 目标已存在则生成新版本，不存在则建档（目标主机自动取目标环境族的部署目标，
// 远端路径沿用源文件）。
type EnvSyncInput struct {
	ProjectID uint   `json:"projectId" binding:"required"`
	SourceEnv string `json:"sourceEnv" binding:"required"`
	TargetEnv string `json:"targetEnv" binding:"required"`
	SubPath   string `json:"subPath" binding:"omitempty,max=512"`
}

// EnvSyncResult 同步结果：计数 + 风险提示（路径不含 {env} 占位符时目标与源
// 落盘同路径——同主机会互相覆盖，前端黄条展示，不阻断）。
type EnvSyncResult struct {
	Created  int      `json:"created"`
	Updated  int      `json:"updated"`
	Warnings []string `json:"warnings"`
}

// EnvSync 执行环境同步；内容只进版本链，不自动下发。
func (s *Service) EnvSync(ctx context.Context, in EnvSyncInput, by string) (EnvSyncResult, error) {
	res := EnvSyncResult{Warnings: []string{}}
	if in.SourceEnv == in.TargetEnv {
		return res, fmt.Errorf("源与目标环境相同")
	}
	// 环境入参：prod 或槽位名（canary 无独立配置目录，拒绝——引用 prod 即是）
	for _, env := range []string{in.SourceEnv, in.TargetEnv} {
		if env == "canary" || !isSlotSeg(env) && env != "prod" {
			return res, fmt.Errorf("环境 %q 不合法（prod 或槽位名 devN；canary 引用 prod 无独立目录）", env)
		}
	}
	sub := strings.Trim(strings.TrimSpace(in.SubPath), "/")
	for _, seg := range strings.Split(sub, "/") {
		if seg == "" || seg == "." || seg == ".." {
			if sub == "" {
				break
			}
			return res, fmt.Errorf("子路径不合法: %q", seg)
		}
	}
	// 目标槽位同样受项目槽位数约束（建档路径与手工创建一致）
	if err := s.checkSlotCap(ctx, in.ProjectID, in.TargetEnv); err != nil {
		return res, err
	}
	// 目标环境族须已绑定部署目标（新建文件的主机来源：prod→prod、devN→test）
	var targetServer uint
	if err := s.db.WithContext(ctx).Table("project_env_targets").
		Select("server_id").Where("project_id = ? AND env_type = ?", in.ProjectID, EnvTypeOf(in.TargetEnv)).
		Scan(&targetServer).Error; err != nil {
		return res, err
	}
	if targetServer == 0 {
		return res, fmt.Errorf("项目未配置 %s 环境的部署目标", EnvTypeOf(in.TargetEnv))
	}
	prefix := in.SourceEnv
	if sub != "" {
		prefix = in.SourceEnv + "/" + sub
	}
	var sources []File
	if err := s.db.WithContext(ctx).
		Where("project_id = ? AND rel_path LIKE ?", in.ProjectID, prefix+"/%").
		Find(&sources).Error; err != nil {
		return res, err
	}
	if len(sources) == 0 {
		return res, fmt.Errorf("源路径 %s 下没有配置文件", prefix)
	}
	for _, src := range sources {
		rest := strings.TrimPrefix(src.RelPath, in.SourceEnv) // /xxx/yyy.yaml
		dstRel := in.TargetEnv + rest
		// 覆盖风险提示：源路径不含 {env} 占位符 → 目标文件落盘路径与源完全相同，
		// 同主机下发互相覆盖（.bak 兜底但会干扰）。提示而非阻断（用户可有意为之）。
		if src.ServerID == targetServer && !strings.Contains(src.Path, envPlaceholder) {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"%s：远端路径 %s 不含 {env} 占位符，目标环境落盘路径与源相同，同主机下发会互相覆盖（建议把路径改为含 {env} 的模板）", src.Name, src.Path))
		}
		var dst File
		e := s.db.WithContext(ctx).Where("project_id = ? AND rel_path = ?", in.ProjectID, dstRel).First(&dst).Error
		if e == nil {
			dst.Content = src.Content
			if err := s.db.WithContext(ctx).Model(&File{}).Where("id = ?", dst.ID).
				Update("content", src.Content).Error; err != nil {
				return res, err
			}
			s.snapVersion(ctx, dst.ID, src.Content, SourceEnvSync, by)
			res.Updated++
		} else {
			nf := src
			nf.ID = 0
			nf.RelPath = dstRel
			nf.ServerID = targetServer
			if err := s.db.WithContext(ctx).Create(&nf).Error; err != nil {
				return res, err
			}
			s.snapVersion(ctx, nf.ID, src.Content, SourceEnvSync, by)
			res.Created++
		}
	}
	return res, nil
}

// RecentChanges P7-M2：最近的配置版本记录（告警 AI 分析工具 get_config_changes
// 数据源）——文件名/环境(路径首段)/操作人/时间/来源，不含内容（DLP 默认收敛）。
func (s *Service) RecentChanges(ctx context.Context, limit int) (string, error) {
	if limit <= 0 || limit > 50 {
		limit = 15
	}
	var rows []Version
	if err := s.db.WithContext(ctx).
		Order("id DESC").Limit(limit).Find(&rows).Error; err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "（暂无配置变更记录）", nil
	}
	fileIDs := make([]uint, 0, len(rows))
	for _, v := range rows {
		fileIDs = append(fileIDs, v.FileID)
	}
	var files []File
	if err := s.db.WithContext(ctx).Where("id IN ?", fileIDs).Find(&files).Error; err != nil {
		return "", err
	}
	fmap := map[uint]File{}
	for _, f := range files {
		fmap[f.ID] = f
	}
	var sb strings.Builder
	for _, v := range rows {
		f := fmap[v.FileID]
		fmt.Fprintf(&sb, "%s %s[%s] by %s（%s）\n",
			v.CreatedAt.Format("01-02 15:04"), f.Name, envOfRel(f.RelPath), orDash(v.CreatedBy), v.Source)
	}
	return strings.TrimSpace(sb.String()), nil
}

// envOfRel 环境=层级路径首段（prod/devN）；旧形态裸文件显 '-'。
// （此前误取远端绝对路径 Path 首段——恒为 etc/opt 等，2026-11-09 顺手修正）
func envOfRel(relPath string) string {
	if seg := firstSeg(relPath); seg != "" {
		return seg
	}
	return "-"
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
