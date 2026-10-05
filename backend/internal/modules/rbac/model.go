// model.go P7-M1 权限专项：自定义角色与动作集的存储模型。
// 绑定关系沿用 users.Roles 逗号串（角色名即 casbin sub），此处只存元数据与动作面。
package rbac

import "time"

// Role 角色元数据。Builtin 行随版本补种，不覆盖人为调整；自定义角色可删。
type Role struct {
	ID          uint      `gorm:"primarykey" json:"id"`
	Name        string    `gorm:"size:64;uniqueIndex;not null" json:"name"`
	Description string    `gorm:"size:255" json:"description"`
	Builtin     bool      `gorm:"not null;default:false" json:"builtin"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func (Role) TableName() string { return "perm_roles" }

// RoleAction 角色的业务动作集（动作 Key 见 actions.go 目录）。
type RoleAction struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	RoleName  string    `gorm:"size:64;uniqueIndex:idx_role_action;not null" json:"roleName"`
	Action    string    `gorm:"size:64;uniqueIndex:idx_role_action;not null" json:"action"`
	CreatedAt time.Time `json:"createdAt"`
}

func (RoleAction) TableName() string { return "perm_role_actions" }

// RoleProject 角色的项目范围；某角色无任何行 = 全局（不限制项目）。
type RoleProject struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	RoleName  string    `gorm:"size:64;uniqueIndex:idx_role_project;not null" json:"roleName"`
	ProjectID uint      `gorm:"uniqueIndex:idx_role_project;not null" json:"projectId"`
	CreatedAt time.Time `json:"createdAt"`
}

func (RoleProject) TableName() string { return "perm_role_projects" }

// Models 返回本模块需要自动迁移的模型。
func Models() []any {
	return []any{Role{}, RoleAction{}, RoleProject{}}
}
