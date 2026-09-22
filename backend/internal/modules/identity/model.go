// Package identity 管理平台用户（含本地超管与 IM JIT 注册用户）。
// 用户表不存 IM 凭证；IM 绑定关系（user_im_bindings，FR2.4）在 IM 提供商选型落地后加入。
package identity

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

type UserStatus string

const (
	StatusActive   UserStatus = "active"
	StatusDisabled UserStatus = "disabled"
)

// BuiltinRoles 内置可分配角色（FR3.2）。superadmin 为本地超管专属（仅 setup
// 创建、全库唯一），不在此列；admin 为普通管理员，仅超管可任命。
var BuiltinRoles = []string{"admin", "ops", "dev", "guest"}

type User struct {
	ID           uint           `gorm:"primarykey" json:"id"`
	DisplayName  string         `gorm:"size:64;uniqueIndex;not null" json:"displayName"`
	Username     *string        `gorm:"size:64;uniqueIndex" json:"username"` // 本地登录账号；IM 用户为 NULL（空串会撞唯一索引）
	PasswordHash string         `gorm:"size:255" json:"-"`                   // 仅本地超管使用，bcrypt，绝不外发
	IsLocalAdmin bool           `gorm:"not null;default:false" json:"isLocalAdmin"`
	Roles        string         `gorm:"size:255;default:guest" json:"roles"` // 逗号分隔，casbin 角色名
	Status       UserStatus     `gorm:"size:16;default:active" json:"status"`
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (User) TableName() string { return "users" }

// ParseRoleList 解析 Roles 字段（逗号分隔），去空白、丢弃空项。
// UsernameOf 便捷读取（NULL 返回空串）。
func (u *User) UsernameOf() string {
	if u.Username == nil {
		return ""
	}
	return *u.Username
}

// SetUsername 写入（空串转 NULL）。
func (u *User) SetUsername(s string) {
	if s == "" {
		u.Username = nil
	} else {
		u.Username = &s
	}
}

func ParseRoleList(s string) []string {
	var roles []string
	for _, r := range strings.Split(s, ",") {
		if r = strings.TrimSpace(r); r != "" {
			roles = append(roles, r)
		}
	}
	return roles
}

// Models 返回本模块需要自动迁移的模型。
func Models() []any {
	return []any{User{}, UserIMBinding{}, IMProviderConfig{}, PlatformSetting{}}
}
