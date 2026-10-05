// pull.go P6-M5 配置拉取 API（方案 B）：复用文件形态真相源，不引入新组件。
// GET /api/config/{app}/{env}——按"应用（项目）×环境"归组 config_files 深合并
// 输出；应用级 token（只读限定 app×env，服务间凭证与人凭证分离）；
// 60s 短缓存 + ETag/304；合并冲突 key 明确报错；仅同格式族可合并。
package configs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
	"gopkg.in/yaml.v3"
)

// PullToken 配置拉取凭证：应用级只读（限定 app×env 范围）。
// 明文只出现一次（签发响应），库里存 sha256；吊销=Enabled false 即时生效。
type PullToken struct {
	ID         uint      `gorm:"primarykey" json:"id"`
	Name       string    `gorm:"size:64;not null" json:"name"`
	App        string    `gorm:"size:64;index;not null" json:"app"` // 应用名=项目名
	Envs       string    `gorm:"size:128;not null" json:"envs"`     // 允许环境，逗号分隔；* = 全环境
	TokenHash  string    `gorm:"size:64;uniqueIndex;not null" json:"-"`
	Enabled    bool      `gorm:"not null;default:true" json:"enabled"`
	LastUsedAt time.Time `json:"lastUsedAt"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (PullToken) TableName() string { return "config_pull_tokens" }

// PullTokenOut 签发结果（Plaintext 仅此一次）。
type PullTokenOut struct {
	PullToken
	Plaintext string `json:"plaintext,omitempty"`
}

func envAllowed(envs, env string) bool {
	for _, e := range strings.Split(envs, ",") {
		if strings.TrimSpace(e) == "*" || strings.TrimSpace(e) == env {
			return true
		}
	}
	return false
}

// IssuePullToken 签发拉取凭证（admin）。
func (s *Service) IssuePullToken(ctx context.Context, name, app, envs string) (*PullTokenOut, error) {
	app = strings.TrimSpace(app)
	if app == "" || strings.TrimSpace(envs) == "" {
		return nil, fmt.Errorf("app 与 envs 均必填")
	}
	// 应用名必须对应真实项目（防幻觉范围）
	var cnt int64
	s.db.WithContext(ctx).Table("projects").Where("name = ?", app).Count(&cnt)
	if cnt == 0 {
		return nil, fmt.Errorf("应用 %q 不存在（须为平台项目名）", app)
	}
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	plaintext := "pull_" + hex.EncodeToString(raw)
	sum := sha256.Sum256([]byte(plaintext))
	t := PullToken{Name: name, App: app, Envs: envs, TokenHash: hex.EncodeToString(sum[:]), Enabled: true}
	if err := s.db.WithContext(ctx).Create(&t).Error; err != nil {
		return nil, err
	}
	return &PullTokenOut{PullToken: t, Plaintext: plaintext}, nil
}

func (s *Service) ListPullTokens(ctx context.Context) ([]PullToken, error) {
	var ts []PullToken
	if err := s.db.WithContext(ctx).Order("id DESC").Find(&ts).Error; err != nil {
		return nil, err
	}
	return ts, nil
}

func (s *Service) SetPullTokenEnabled(ctx context.Context, id uint, enabled bool) error {
	res := s.db.WithContext(ctx).Model(&PullToken{}).Where("id = ?", id).Update("enabled", enabled)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("拉取凭证不存在")
	}
	return nil
}

// VerifyPullToken 公开接口鉴权：hash 查 token → 启用 → app×env 范围。
// LastUsedAt best-effort 更新（不阻塞请求）。
func (s *Service) VerifyPullToken(ctx context.Context, plaintext, app, env string) (*PullToken, error) {
	sum := sha256.Sum256([]byte(plaintext))
	var t PullToken
	if err := s.db.WithContext(ctx).Where("token_hash = ?", hex.EncodeToString(sum[:])).First(&t).Error; err != nil {
		return nil, fmt.Errorf("凭证无效")
	}
	if !t.Enabled {
		return nil, fmt.Errorf("凭证已吊销")
	}
	if t.App != app {
		return nil, fmt.Errorf("凭证不适用于应用 %q", app)
	}
	if !envAllowed(t.Envs, env) {
		return nil, fmt.Errorf("凭证不适用于环境 %q", env)
	}
	s.db.WithContext(context.WithoutCancel(ctx)).Model(&PullToken{}).Where("id = ?", t.ID).
		Update("last_used_at", time.Now())
	return &t, nil
}

// PullOut 拉取结果：合并后内容 + 聚合指纹（各文件内容 hash 的再 hash）。
type PullOut struct {
	Body    []byte
	Version string // 聚合指纹（X-Config-Version / ETag）
	Format  string
	Files   int
}

// fetchEnvFiles 项目×环境（rel_path 首段）有内容的配置文件（拉取与合并视图共用）。
func (s *Service) fetchEnvFiles(ctx context.Context, projectID uint, env string) ([]File, error) {
	var files []File
	if err := s.db.WithContext(ctx).Where("project_id = ?", projectID).Order("rel_path").Find(&files).Error; err != nil {
		return nil, err
	}
	var envs []File
	for _, f := range files {
		if firstSeg(f.RelPath) == env && f.Content != "" {
			envs = append(envs, f)
		}
	}
	return envs, nil
}

// PullConfig 应用级聚合拉取：项目 × 环境（rel_path 首段）→ 同格式族深合并。
func (s *Service) PullConfig(ctx context.Context, app, env string) (*PullOut, error) {
	var projectID uint
	if err := s.db.WithContext(ctx).Table("projects").Select("id").
		Where("name = ?", app).Scan(&projectID).Error; err != nil || projectID == 0 {
		return nil, fmt.Errorf("应用 %q 不存在", app)
	}
	envs, err := s.fetchEnvFiles(ctx, projectID, env)
	if err != nil {
		return nil, err
	}
	if len(envs) == 0 {
		return nil, fmt.Errorf("应用 %q 环境 %q 无可用配置", app, env)
	}
	// 格式族检查：结构化（yaml/json/toml）与平铺（env/ini）不可混
	family := formatFamily(envs[0].Format)
	for _, f := range envs[1:] {
		if formatFamily(f.Format) != family {
			return nil, fmt.Errorf("配置格式不一致（%s 与 %s），不可合并", envs[0].RelPath, f.RelPath)
		}
	}
	// 聚合指纹：排序遍历各文件内容 hash 再 hash
	hashes := make([]string, 0, len(envs))
	for _, f := range envs {
		hashes = append(hashes, contentHash(f.Content))
	}
	sort.Strings(hashes)
	vsum := sha256.Sum256([]byte(strings.Join(hashes, ",")))
	version := hex.EncodeToString(vsum[:12])

	var body []byte
	var merr error
	if family == "struct" {
		body, merr = mergeStructFiles(envs)
	} else {
		body, merr = mergeFlatFiles(envs)
	}
	err = merr
	if err != nil {
		return nil, err
	}
	return &PullOut{Body: body, Version: version, Format: envs[0].Format, Files: len(envs)}, nil
}

func firstSeg(relPath string) string {
	relPath = strings.TrimPrefix(relPath, "/")
	if i := strings.Index(relPath, "/"); i >= 0 {
		return relPath[:i]
	}
	return relPath
}

func formatFamily(format string) string {
	switch format {
	case FormatYAML, "json", FormatTOML:
		return "struct"
	default: // env / ini
		return "flat"
	}
}

// mergeStructFiles yaml/json/toml → map 深合并（冲突 key 报错）→ 按首文件格式序列化。
func mergeStructFiles(files []File) ([]byte, error) {
	merged := map[string]any{}
	for _, f := range files {
		var m map[string]any
		var err error
		switch f.Format {
		case FormatYAML:
			err = yaml.Unmarshal([]byte(f.Content), &m)
		case "json":
			err = json.Unmarshal([]byte(f.Content), &m)
		case FormatTOML:
			err = toml.Unmarshal([]byte(f.Content), &m)
		}
		if err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %v", f.RelPath, err)
		}
		if err := deepMerge(merged, m, ""); err != nil {
			return nil, fmt.Errorf("%s: %v", f.RelPath, err)
		}
	}
	switch files[0].Format {
	case FormatYAML:
		return yaml.Marshal(merged)
	case "json":
		return json.MarshalIndent(merged, "", "  ")
	case FormatTOML:
		return toml.Marshal(merged)
	}
	return nil, fmt.Errorf("不支持的格式 %s", files[0].Format)
}

// deepMerge 深合并：双方都是 map 则递归；同 key 非双 map = 冲突（明确报错，不静默覆盖）。
func deepMerge(dst, src map[string]any, path string) error {
	for k, v := range src {
		full := k
		if path != "" {
			full = path + "." + k
		}
		if dv, ok := dst[k]; ok {
			dm, dok := dv.(map[string]any)
			sm, sok := v.(map[string]any)
			if dok && sok {
				if err := deepMerge(dm, sm, full); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("合并冲突 key %q：多个文件定义了不同类型的值", full)
		}
		dst[k] = v
	}
	return nil
}

// mergeFlatFiles env/ini → key 平铺合并（冲突 key 报错）→ 按 key 排序输出。
func mergeFlatFiles(files []File) ([]byte, error) {
	merged := map[string]string{}
	src := map[string]string{} // key → 定义文件（冲突提示用）
	for _, f := range files {
		var kv map[string]string
		var err error
		if f.Format == FormatINI {
			kv, err = parseINI(f.Content)
		} else {
			kv, err = parseENV(f.Content)
		}
		if err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %v", f.RelPath, err)
		}
		for k, v := range kv {
			if _, dup := merged[k]; dup {
				return nil, fmt.Errorf("%s: 合并冲突 key %q（与 %s 重复）", f.RelPath, k, src[k])
			}
			merged[k] = v
			src[k] = f.RelPath
		}
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&sb, "%s=%s\n", k, merged[k])
	}
	return []byte(sb.String()), nil
}

// parseENV KEY=VAL（# 注释、空行跳过、去引号）。
func parseENV(content string) (map[string]string, error) {
	out := map[string]string{}
	for i, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			return nil, fmt.Errorf("第 %d 行不是 KEY=VAL", i+1)
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.TrimSpace(line[eq+1:])
		v = strings.Trim(v, `"'`)
		out[k] = v
	}
	return out, nil
}

// parseINI [section] 段 + key=val；段内 key 平铺为 section.key（无段为裸 key）。
func parseINI(content string) (map[string]string, error) {
	out := map[string]string{}
	section := ""
	for i, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSpace(line[1 : len(line)-1])
			continue
		}
		eq := strings.Index(line, "=")
		if eq <= 0 {
			return nil, fmt.Errorf("第 %d 行不是 key=val", i+1)
		}
		k := strings.TrimSpace(line[:eq])
		v := strings.Trim(strings.TrimSpace(line[eq+1:]), `"'`)
		if section != "" {
			k = section + "." + k
		}
		out[k] = v
	}
	return out, nil
}

// MergedOut 合并视图输出（AgileConfig 式 UI 适配：项目×环境最终生效配置）。
type MergedOut struct {
	Body    string `json:"body"`
	Version string `json:"version"`
	Files   int    `json:"files"`
	Format  string `json:"format"`
}

// MergedPreview 合并视图（管理端，只读）：项目×环境全部文件按 M5 同一合并
// 语义聚合成一棵树，输出 JSON/YAML——即拉取 API 返回内容的可视化预览。
func (s *Service) MergedPreview(ctx context.Context, projectID uint, env, format string) (*MergedOut, error) {
	if format != "json" && format != "yaml" {
		format = "json"
	}
	files, err := s.fetchEnvFiles(ctx, projectID, env)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("该应用×环境无配置文件")
	}
	// 聚合指纹（与 PullConfig 同算法）
	hashes := make([]string, 0, len(files))
	for _, f := range files {
		hashes = append(hashes, contentHash(f.Content))
	}
	sort.Strings(hashes)
	vsum := sha256.Sum256([]byte(strings.Join(hashes, ",")))
	version := hex.EncodeToString(vsum[:12])

	merged := map[string]any{}
	for _, f := range files {
		var part map[string]any
		switch formatFamily(f.Format) {
		case "struct":
			var m map[string]any
			var perr error
			switch f.Format {
			case FormatYAML:
				perr = yaml.Unmarshal([]byte(f.Content), &m)
			case "json":
				perr = json.Unmarshal([]byte(f.Content), &m)
			case FormatTOML:
				perr = toml.Unmarshal([]byte(f.Content), &m)
			}
			if perr != nil {
				return nil, fmt.Errorf("解析 %s 失败: %v", f.RelPath, perr)
			}
			part = m
		default: // env/ini → 点号嵌套树（与前端转换视图同规则）
			var kv map[string]string
			if f.Format == FormatINI {
				kv, err = parseINI(f.Content)
			} else {
				kv, err = parseENV(f.Content)
			}
			if err != nil {
				return nil, fmt.Errorf("解析 %s 失败: %v", f.RelPath, err)
			}
			// 同文件内 key 冲突不可能（map）；跨文件冲突交给 deepMerge 统一报错
			part = map[string]any{}
			for k, v := range kv {
				insertDotted(part, strings.Split(k, "."), v)
			}
		}
		if err := deepMerge(merged, part, ""); err != nil {
			return nil, fmt.Errorf("%s: %v", f.RelPath, err)
		}
	}
	var body []byte
	var serr error
	if format == "json" {
		body, serr = json.MarshalIndent(merged, "", "  ")
	} else {
		body, serr = yaml.Marshal(merged)
	}
	if serr != nil {
		return nil, serr
	}
	return &MergedOut{Body: string(body), Version: version, Files: len(files), Format: format}, nil
}

// insertDotted 点号路径嵌套插入（env/ini 键 → 树）。
func insertDotted(m map[string]any, path []string, v string) {
	if len(path) == 1 {
		m[path[0]] = v
		return
	}
	next, ok := m[path[0]].(map[string]any)
	if !ok {
		next = map[string]any{}
		m[path[0]] = next
	}
	insertDotted(next, path[1:], v)
}
