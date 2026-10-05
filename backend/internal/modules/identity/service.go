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

var ErrAdminAssignForbidden = errors.New("admin 角色仅超管可任命")

// ExtraRoleValidator 自定义角色校验钩子（P7-M1：app 层装配时由 rbac 注入，
// 查角色表；nil = 仅内置角色）。identity 不反向依赖 rbac，避免环。
var ExtraRoleValidator func(role string) bool

// UsernameReserved 用户名保留字校验钩子（P7-M1：用户名与角色名同为 casbin sub，
// 同名会劫持 M6 用户级授权策略；由 rbac 注入，nil = 不校验）。
var UsernameReserved func(name string) bool

// ValidateRoles 校验角色串：每项须为内置可分配角色（superadmin 不可分配）
// 或角色表中存在的自定义角色（经 ExtraRoleValidator）。
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
		if !known && ExtraRoleValidator != nil && ExtraRoleValidator(r) {
			known = true
		}
		if !known {
			return fmt.Errorf("未知角色: %s（可选：%s 或自定义角色）", r, strings.Join(BuiltinRoles, "/"))
		}
	}
	return nil
}

// validateAssign 权限校验：admin 仅超管可任命（actorSuper = 操作者是本地超管）。
func validateAssign(s string, actorSuper bool) error {
	if err := ValidateRoles(s); err != nil {
		return err
	}
	if !actorSuper {
		for _, r := range ParseRoleList(s) {
			if r == "admin" {
				return ErrAdminAssignForbidden
			}
		}
	}
	return nil
}

func (s *UserService) Create(ctx context.Context, in CreateUserInput, actorSuper bool) (*User, error) {
	if err := validateAssign(in.Roles, actorSuper); err != nil {
		return nil, err
	}
	if in.Username != "" {
		if _, err := s.repo.GetByUsername(ctx, in.Username); err == nil {
			return nil, ErrAlreadyExists
		}
		// P7-M1：用户名与角色名同为 casbin sub，撞名会劫持用户级授权策略
		if UsernameReserved != nil && UsernameReserved(in.Username) {
			return nil, fmt.Errorf("该用户名与角色名冲突，不可用")
		}
	}
	u := &User{DisplayName: in.DisplayName, Roles: in.Roles}
	u.SetUsername(in.Username)
	if u.Roles == "" {
		u.Roles = "guest"
	}
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, fmt.Errorf("创建用户失败: %w", err)
	}
	return u, nil
}

func (s *UserService) UpdateRoles(ctx context.Context, id uint, roles string, actorSuper bool) (*User, error) {
	if err := validateAssign(roles, actorSuper); err != nil {
		return nil, err
	}
	u, err := s.repo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// 超管账号只有超管能改
	if u.IsLocalAdmin && !actorSuper {
		return nil, errors.New("超管账号仅超管可修改")
	}
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
		DisplayName:  displayName,
		PasswordHash: passwordHash,
		IsLocalAdmin: true,
		Roles:        "superadmin",
	}
	u.SetUsername(username)
	if err := s.repo.Create(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}
