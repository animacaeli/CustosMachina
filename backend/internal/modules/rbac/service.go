package rbac

import (
	"fmt"

	"github.com/casbin/casbin/v2"

	"github.com/custos-machina/backend/internal/modules/identity"
)

// Policy 一条权限矩阵项：角色 → 资源路径模板 → HTTP 方法（支持 "GET|POST" 正则形式）。
type Policy struct {
	Role string `json:"role"`
	Path string `json:"path"`
	Act  string `json:"act"`
}

type Service struct {
	enforcer *casbin.SyncedEnforcer
}

func NewService(enforcer *casbin.SyncedEnforcer) *Service { return &Service{enforcer: enforcer} }

// RoleWithPolicies 角色及其权限矩阵（前端权限矩阵页数据源）。
type RoleWithPolicies struct {
	Name     string   `json:"name"`
	Builtin  bool     `json:"builtin"`
	Policies []Policy `json:"policies"`
}

func (s *Service) ListRoles() []RoleWithPolicies {
	result := []RoleWithPolicies{{Name: "admin", Builtin: true, Policies: []Policy{{Role: "admin", Path: "/*", Act: ".*"}}}}
	for _, role := range identity.BuiltinRoles {
		if role == "admin" {
			continue
		}
		result = append(result, RoleWithPolicies{Name: role, Builtin: true, Policies: s.listPolicies(role)})
	}
	return result
}

func (s *Service) listPolicies(role string) []Policy {
	ps, _ := s.enforcer.GetFilteredPolicy(0, role)
	out := make([]Policy, 0, len(ps))
	for _, p := range ps {
		if len(p) == 3 {
			out = append(out, Policy{Role: p[0], Path: p[1], Act: p[2]})
		}
	}
	return out
}

// ReplaceRolePolicies 整体替换某角色的权限矩阵（权限矩阵页保存语义）。
func (s *Service) ReplaceRolePolicies(role string, policies []Policy) error {
	if role == "admin" {
		return fmt.Errorf("admin 为本地超管专属角色，权限不可编辑")
	}
	if _, err := s.enforcer.RemoveFilteredPolicy(0, role); err != nil {
		return fmt.Errorf("清除旧策略失败: %w", err)
	}
	for _, p := range policies {
		if _, err := s.enforcer.AddPolicy(role, p.Path, p.Act); err != nil {
			return fmt.Errorf("写入策略 %v 失败: %w", p, err)
		}
	}
	return nil
}

// UserPermissions 当前用户的权限下发（FR3.3）：角色 + 扁平化权限码。
// 前端用 role 控制路由，用 permissions 控制按钮粒度。
func (s *Service) UserPermissions(roles []string, isAdmin bool) map[string]any {
	if isAdmin {
		return map[string]any{"roles": append(roles, "admin"), "permissions": []string{"*"}}
	}
	permSet := map[string]struct{}{}
	for _, role := range roles {
		for _, p := range s.listPolicies(role) {
			permSet[p.Path+":"+p.Act] = struct{}{}
		}
	}
	perms := make([]string, 0, len(permSet))
	for k := range permSet {
		perms = append(perms, k)
	}
	return map[string]any{"roles": roles, "permissions": perms}
}
