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
	result := []RoleWithPolicies{{
		Name: "superadmin", Builtin: true,
		Policies: []Policy{{Role: "superadmin", Path: "/*", Act: ".*"}},
	}}
	for _, role := range identity.BuiltinRoles {
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
	if role == "superadmin" {
		return fmt.Errorf("superadmin 为超管专属角色，权限不可编辑")
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

// terminalACLPath 主机终端的用户级授权资源点（与角色级通配
// /servers/*/terminal 并存：通配覆盖角色，精确路径覆盖被授权的登录名）。
func terminalACLPath(serverID uint) string {
	return fmt.Sprintf("/servers/%d/terminal", serverID)
}

func builtinRoleSet() map[string]bool {
	set := map[string]bool{"superadmin": true}
	for _, r := range identity.BuiltinRoles {
		set[r] = true
	}
	return set
}

// ServerTerminalACLs 某主机终端的登录名级授权列表（角色策略不在其列）。
func (s *Service) ServerTerminalACLs(serverID uint) []string {
	ps, _ := s.enforcer.GetFilteredPolicy(1, terminalACLPath(serverID), "GET")
	builtin := builtinRoleSet()
	users := []string{}
	for _, p := range ps {
		if len(p) == 3 && !builtin[p[0]] {
			users = append(users, p[0])
		}
	}
	return users
}

// SetServerTerminalACLs 整体替换某主机的登录名级终端授权（堡垒机细粒度，
// P6-M6）。RemoveFilteredPolicy 按字段精确匹配，不影响角色通配策略。
func (s *Service) SetServerTerminalACLs(serverID uint, usernames []string) error {
	path := terminalACLPath(serverID)
	if _, err := s.enforcer.RemoveFilteredPolicy(1, path, "GET"); err != nil {
		return fmt.Errorf("清除旧授权失败: %w", err)
	}
	for _, u := range usernames {
		if u == "" {
			continue
		}
		if _, err := s.enforcer.AddPolicy(u, path, "GET"); err != nil {
			return fmt.Errorf("写入授权 %s 失败: %w", u, err)
		}
	}
	return nil
}
