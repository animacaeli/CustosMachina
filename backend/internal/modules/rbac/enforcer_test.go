package rbac

import (
	"testing"

	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTestEnforcer(t *testing.T) *Service {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	if err := db.AutoMigrate(&identity.PlatformSetting{}); err != nil {
		t.Fatalf("迁移 settings 表失败: %v", err)
	}
	e, _, err := NewEnforcer(db)
	if err != nil {
		t.Fatalf("初始化 enforcer 失败: %v", err)
	}
	return NewService(e)
}

func TestDefaultPolicies_Enforce(t *testing.T) {
	svc := newTestEnforcer(t)
	cases := []struct {
		role, obj, act string
		want           bool
	}{
		{"ops", "/services", "GET", true},
		{"ops", "/services", "POST", true},
		{"ops", "/services/api-gateway/logs", "GET", true},
		{"ops", "/users", "GET", false},     // 用户管理不在默认矩阵
		{"dev", "/services", "POST", false}, // dev 只读
		{"dev", "/alerts", "GET", true},
		{"guest", "/services/api-gateway", "GET", true},
		{"guest", "/users", "GET", false},
		{"", "/services", "GET", false},
	}
	for _, tc := range cases {
		e := svc.enforcer
		got, err := EnforceAny(e, identity.ParseRoleList(tc.role), tc.obj, tc.act)
		if err != nil {
			t.Fatalf("Enforce(%v) 报错: %v", tc, err)
		}
		if got != tc.want {
			t.Errorf("Enforce(role=%q obj=%q act=%q) = %v, 期望 %v", tc.role, tc.obj, tc.act, got, tc.want)
		}
	}
}

func TestReplaceRolePolicies(t *testing.T) {
	svc := newTestEnforcer(t)
	err := svc.ReplaceRolePolicies("guest", []Policy{{Path: "/services", Act: "GET"}})
	if err != nil {
		t.Fatalf("替换策略失败: %v", err)
	}
	ok, _ := EnforceAny(svc.enforcer, []string{"guest"}, "/services/api", "GET")
	if ok {
		t.Error("替换后 /services/* 已收窄，/services/api 不应再放行")
	}
	ok, _ = EnforceAny(svc.enforcer, []string{"guest"}, "/services", "GET")
	if !ok {
		t.Error("替换后 /services GET 应放行")
	}

	if err := svc.ReplaceRolePolicies("superadmin", nil); err == nil {
		t.Error("superadmin 角色策略不可编辑，应报错")
	}
	if err := svc.ReplaceRolePolicies("admin", nil); err != nil {
		t.Errorf("admin 角色策略应可编辑: %v", err)
	}
}

func TestParseRoleList(t *testing.T) {
	got := identity.ParseRoleList(" ops , dev ,,")
	if len(got) != 2 || got[0] != "ops" || got[1] != "dev" {
		t.Errorf("ParseRoles 结果不符: %v", got)
	}
}

func TestUserPermissions_Admin(t *testing.T) {
	svc := newTestEnforcer(t)
	out := svc.UserPermissions([]string{}, true)
	perms := out["permissions"].([]string)
	if len(perms) != 1 || perms[0] != "*" {
		t.Errorf("超管权限应为 [\"*\"], 实际 %v", perms)
	}
}
