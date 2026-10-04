// skill.go P6-M3 声明式技能包：管理员配置的提示词模板 + 运维 runbook，
// 会话中经 / 命令触发（Claude Code 风格命令面板）。只做声明式——skill 不携带
// 可执行代码（安全边界）；工具组合部分待 M3 对话内工具调用落地后接入。
package ai

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Skill 声明式技能（管理员 CRUD；按角色 allowlist，空 = 全部角色可用）。
type Skill struct {
	ID          uint           `gorm:"primarykey" json:"id"`
	Name        string         `gorm:"size:64;uniqueIndex;not null" json:"name"` // 触发名：/name（小写字母数字-_）
	Title       string         `gorm:"size:128" json:"title"`
	Description string         `gorm:"size:255" json:"description"` // 命令面板里的一行说明
	Prompt      string         `gorm:"type:text" json:"prompt"`     // 提示词模板；{{q}} = 用户输入
	Runbook     string         `gorm:"type:text" json:"runbook"`    // 运维 runbook（markdown，随 prompt 注入）
	Roles       string         `gorm:"size:128" json:"roles"`       // allowlist 逗号分隔（如 admin,ops）；空=全部
	Enabled     bool           `gorm:"not null;default:true" json:"enabled"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	DeletedAt   gorm.DeletedAt `gorm:"index" json:"-"`
}

func (Skill) TableName() string { return "ai_skills" }

var ErrSkillNotFound = errors.New("技能不存在")

func validSkillName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, c := range name {
		ok := c == '-' || c == '_' ||
			(c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !ok {
			return false
		}
	}
	return true
}

type SkillService struct {
	db *gorm.DB
}

func NewSkillService(db *gorm.DB) *SkillService { return &SkillService{db: db} }

type SaveSkillInput struct {
	Name        string `json:"name" binding:"required,max=64"`
	Title       string `json:"title" binding:"required,max=128"`
	Description string `json:"description" binding:"max=255"`
	Prompt      string `json:"prompt" binding:"required,max=16000"`
	Runbook     string `json:"runbook" binding:"omitempty,max=32000"`
	Roles       string `json:"roles" binding:"omitempty,max=128"`
	Enabled     *bool  `json:"enabled"`
}

// List 按角色列出可用技能（管理端传空 roles 看全部）。
func (s *SkillService) List(ctx context.Context, viewerRoles []string) ([]Skill, error) {
	var all []Skill
	q := s.db.WithContext(ctx).Order("name")
	if len(viewerRoles) == 0 { // 管理端：全部（含禁用）
		if err := q.Find(&all).Error; err != nil {
			return nil, err
		}
		return all, nil
	}
	if err := q.Where("enabled = ?", true).Find(&all).Error; err != nil {
		return nil, err
	}
	out := make([]Skill, 0, len(all))
	for _, sk := range all {
		if sk.allows(viewerRoles) {
			out = append(out, sk)
		}
	}
	return out, nil
}

func (sk *Skill) allows(viewerRoles []string) bool {
	if strings.TrimSpace(sk.Roles) == "" {
		return true
	}
	allow := map[string]bool{}
	for _, r := range strings.Split(sk.Roles, ",") {
		if r = strings.TrimSpace(r); r != "" {
			allow[r] = true
		}
	}
	for _, r := range viewerRoles {
		if allow[r] {
			return true
		}
	}
	return false
}

func (s *SkillService) Save(ctx context.Context, id uint, in SaveSkillInput) (*Skill, error) {
	in.Name = strings.ToLower(strings.TrimSpace(in.Name))
	if !validSkillName(in.Name) {
		return nil, errors.New("技能名仅限小写字母/数字/-/_（作为 /命令 触发名）")
	}
	var sk Skill
	if id > 0 {
		if err := s.db.WithContext(ctx).First(&sk, id).Error; err != nil {
			return nil, ErrSkillNotFound
		}
	} else {
		sk = Skill{Name: in.Name}
	}
	sk.Title, sk.Description = in.Title, in.Description
	sk.Prompt, sk.Runbook, sk.Roles = in.Prompt, in.Runbook, strings.TrimSpace(in.Roles)
	if in.Enabled != nil {
		sk.Enabled = *in.Enabled
	} else if id == 0 {
		sk.Enabled = true
	}
	if err := s.db.WithContext(ctx).Save(&sk).Error; err != nil {
		return nil, err
	}
	return &sk, nil
}

func (s *SkillService) Delete(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Unscoped().Delete(&Skill{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrSkillNotFound
	}
	return nil
}

// LookupFor 按触发名取对角色可用的技能（对话注入用）。
func (s *SkillService) LookupFor(ctx context.Context, name string, viewerRoles []string) (*Skill, error) {
	var sk Skill
	if err := s.db.WithContext(ctx).
		Where("name = ? AND enabled = ?", strings.ToLower(strings.TrimSpace(name)), true).
		First(&sk).Error; err != nil {
		return nil, ErrSkillNotFound
	}
	if !sk.allows(viewerRoles) {
		return nil, ErrSkillNotFound // 越权触发与不存在同语义，不泄露技能存在性
	}
	return &sk, nil
}

// RenderSkill 展开技能为注入块：{{q}} 替换为用户输入；runbook 附后。
// 数据围栏语义与 pack 一致（skill 由管理员配置，但 runbook 中的示例数据
// 仍按不可信文本对待）。
func RenderSkill(sk *Skill, userQuery string) string {
	var b strings.Builder
	b.WriteString("以下是指令式技能「" + sk.Title + "」的执行要求（用户经 /" + sk.Name + " 显式触发）：\n")
	b.WriteString(strings.ReplaceAll(sk.Prompt, "{{q}}", userQuery))
	if strings.TrimSpace(sk.Runbook) != "" {
		b.WriteString("\n\n参考 runbook（运维手册，按需引用）：\n")
		b.WriteString(sk.Runbook)
	}
	return b.String()
}

// SkillModels skill 自动迁移。
func SkillModels() []any { return []any{&Skill{}} }
