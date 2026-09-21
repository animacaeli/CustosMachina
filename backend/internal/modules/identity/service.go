package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gorm.io/gorm"
)

var (
	ErrNotFound      = errors.New("用户不存在")
	ErrAlreadyExists = errors.New("用户已存在")
)

type UserService struct {
	repo UserRepository
}

func NewUserService(repo UserRepository) *UserService { return &UserService{repo: repo} }

type CreateUserInput struct {
	DisplayName string `json:"displayName" binding:"required"`
	Username    string `json:"username"`
	Roles       string `json:"roles"`
}

func (s *UserService) List(ctx context.Context) ([]User, error) { return s.repo.List(ctx) }

// ValidateRoles 校验角色串：每项须为内置角色，且不允许把 admin 分配给普通用户。
// 未知角色在 casbin 默认拒绝下虽无权限，但仍拒绝写入以保持数据干净。
func ValidateRoles(s string) error {
	for _, r := range ParseRoleList(s) {
		known := false
		for _, b := range BuiltinRoles {
			if r == b {
				known = true
				break
			}
		}
		if !known {
			return fmt.Errorf("未知角色: %s（可选：%s）", r, strings.Join(BuiltinRoles, "/"))
		}
		if r == "admin" {
			return errors.New("admin 为本地超管专属角色，不可分配")
		}
	}
	return nil
}

func (s *UserService) Create(ctx context.Context, in CreateUserInput) (*User, error) {
	if err := ValidateRoles(in.Roles); err != nil {
		return nil, err
	}
	if in.Username != "" {
		if _, err := s.repo.GetByUsername(ctx, in.Username); err == nil {
			return nil, ErrAlreadyExists
		}
	}
	u := &User{DisplayName: in.DisplayName, Username: in.Username, Roles: in.Roles}
	if u.Roles == "" {
		u.Roles = "guest"
	}
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}
	return u, nil
}

func (s *UserService) UpdateRoles(ctx context.Context, id uint, roles string) (*User, error) {
	if err := ValidateRoles(roles); err != nil {
		return nil, err
	}
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Roles = roles
	if err := s.repo.Update(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// CountUsers 供 setup 模块判断是否首次启动（FR1.1）。
func (s *UserService) CountUsers(ctx context.Context) (int64, error) { return s.repo.Count(ctx) }

// EnsureLocalAdmin 由 setup 向导调用：创建 break-glass 本地超管（FR1.3）。
func (s *UserService) EnsureLocalAdmin(ctx context.Context, username, displayName, passwordHash string) (*User, error) {
	if existing, err := s.repo.GetByUsername(ctx, username); err == nil {
		return existing, nil
	}
	u := &User{
		Username:     username,
		DisplayName:  displayName,
		PasswordHash: passwordHash,
		IsLocalAdmin: true,
		Roles:        "admin",
	}
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}
