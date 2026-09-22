package identity

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

// mockUserRepository 手写桩实现，验证 service 层只依赖仓储接口、
// 不依赖数据库即可单测（配合 wire 注入，生产实现由 wire 替换）。
type mockUserRepository struct {
	UserRepository
	users map[string]*User
}

func newMockRepo() *mockUserRepository {
	return &mockUserRepository{users: map[string]*User{}}
}

func (m *mockUserRepository) Create(_ context.Context, u *User) error {
	m.users[u.UsernameOf()] = u
	return nil
}

func (m *mockUserRepository) GetByUsername(_ context.Context, username string) (*User, error) {
	if u, ok := m.users[username]; ok {
		return u, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockUserRepository) Count(_ context.Context) (int64, error) {
	return int64(len(m.users)), nil
}

func TestCreateUser_DefaultRoles(t *testing.T) {
	svc := NewUserService(newMockRepo())
	u, err := svc.Create(context.Background(), CreateUserInput{DisplayName: "张三"}, true)
	if err != nil {
		t.Fatalf("创建用户失败: %v", err)
	}
	if u.Roles != "guest" {
		t.Errorf("默认角色应为 guest，实际 %q", u.Roles)
	}
}

func TestCreateUser_DuplicateUsername(t *testing.T) {
	svc := NewUserService(newMockRepo())
	ctx := context.Background()
	if _, err := svc.Create(ctx, CreateUserInput{DisplayName: "a", Username: "admin"}, true); err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}
	if _, err := svc.Create(ctx, CreateUserInput{DisplayName: "b", Username: "admin"}, true); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("重复用户名应返回 ErrAlreadyExists，实际 %v", err)
	}
}

func TestCountUsers_SetupNeeded(t *testing.T) {
	svc := NewUserService(newMockRepo())
	if n, _ := svc.CountUsers(context.Background()); n != 0 {
		t.Fatalf("空仓储应返回 0，实际 %d", n)
	}
}

func TestValidateRoles(t *testing.T) {
	if err := ValidateRoles("ops, dev"); err != nil {
		t.Errorf("合法角色不应报错: %v", err)
	}
	if err := ValidateRoles("guest,root"); err == nil {
		t.Error("未知角色应被拒绝")
	}
	if err := ValidateRoles("admin"); err != nil {
		t.Errorf("admin 应为合法角色: %v", err)
	}
	if err := ValidateRoles("superadmin"); err == nil {
		t.Error("superadmin 不可分配")
	}
	if err := ValidateRoles(""); err != nil {
		t.Errorf("空角色串应放行: %v", err)
	}
}

func TestAdminAssignPermission(t *testing.T) {
	svc := NewUserService(newMockRepo())
	ctx := context.Background()
	// 普通管理员（actorSuper=false）不能任命 admin
	if _, err := svc.Create(ctx, CreateUserInput{DisplayName: "x", Roles: "admin"}, false); err == nil {
		t.Error("非超管不能任命 admin")
	}
	// 超管可以
	if _, err := svc.Create(ctx, CreateUserInput{DisplayName: "y", Roles: "admin"}, true); err != nil {
		t.Errorf("超管应可任命 admin: %v", err)
	}
}
