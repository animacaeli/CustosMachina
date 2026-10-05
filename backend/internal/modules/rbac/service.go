package rbac

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/casbin/casbin/v2"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/auth"
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
	db       *gorm.DB
	users    identity.UserRepository
}

func NewService(db *gorm.DB, enforcer *casbin.SyncedEnforcer, users identity.UserRepository) (*Service, error) {
	s := &Service{enforcer: enforcer, db: db, users: users}
	if err := s.seedRoles(); err != nil {
		return nil, err
	}
	// identity 侧钩子：角色串校验接受自定义角色；用户名不得撞角色名（两者都是 casbin sub）
	identity.ExtraRoleValidator = s.knownRole
	identity.UsernameReserved = s.isRoleName
	gateService = s
	return s, nil
}

// seedRoles 角色表种子：表空时全量种入；升级部署只补缺失的内置行，
// 不覆盖已有行（人为调整过的动作集保持原样）。
func (s *Service) seedRoles() error {
	for _, name := range identity.BuiltinRoles {
		var cnt int64
		if err := s.db.Model(&Role{}).Where("name = ?", name).Count(&cnt).Error; err != nil {
			return fmt.Errorf("检查角色种子失败: %w", err)
		}
		if cnt > 0 {
			continue
		}
		if err := s.db.Create(&Role{Name: name, Builtin: true}).Error; err != nil {
			return fmt.Errorf("种入内置角色 %s 失败: %w", name, err)
		}
		for _, a := range builtinDefaultActions[name] {
			if err := s.db.Create(&RoleAction{RoleName: name, Action: a}).Error; err != nil {
				return fmt.Errorf("种入角色动作失败: %w", err)
			}
		}
	}
	return nil
}

// RoleWithPolicies 角色及其权限矩阵（前端角色管理页数据源）。
type RoleWithPolicies struct {
	Name        string   `json:"name"`
	Builtin     bool     `json:"builtin"`
	Description string   `json:"description"`
	Policies    []Policy `json:"policies"`
	Actions     []string `json:"actions"`
	ProjectIDs  []uint   `json:"projectIds"`
	UserCount   int      `json:"userCount"`
}

func (s *Service) ListRoles() []RoleWithPolicies {
	result := []RoleWithPolicies{{
		Name: "superadmin", Builtin: true,
		Policies: []Policy{{Role: "superadmin", Path: "/*", Act: ".*"}},
		Actions:  AllActionKeys(),
	}}
	var rows []Role
	s.db.Order("builtin DESC, id ASC").Find(&rows)
	userRoles := s.userRoleCounts()
	for _, r := range rows {
		item := RoleWithPolicies{
			Name: r.Name, Builtin: r.Builtin, Description: r.Description,
			Policies:   s.listPolicies(r.Name),
			Actions:    s.actionsOf([]string{r.Name}),
			ProjectIDs: s.projectIDsOf(r.Name),
			UserCount:  userRoles[r.Name],
		}
		if item.Actions == nil {
			item.Actions = []string{}
		}
		if item.ProjectIDs == nil {
			item.ProjectIDs = []uint{}
		}
		result = append(result, item)
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

// userRoleCounts 每个角色绑定的用户数（users.Roles 是逗号串，量小内存聚合）。
func (s *Service) userRoleCounts() map[string]int {
	counts := map[string]int{}
	ctx := context.Background()
	users, err := s.users.List(ctx)
	if err != nil {
		return counts
	}
	for _, u := range users {
		for _, r := range identity.ParseRoleList(u.Roles) {
			counts[r]++
		}
	}
	return counts
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

// UserPermissions 当前用户的权限下发（FR3.3）：角色 + 扁平化权限码 + 业务动作码（P7-M1）。
// 前端用 role 控制路由，用 permissions 控制按钮粒度，用 actions 控制动作级 UI。
func (s *Service) UserPermissions(roles []string, isAdmin bool) map[string]any {
	if isAdmin {
		return map[string]any{"roles": append(roles, "admin"), "permissions": []string{"*"}, "actions": AllActionKeys()}
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
	return map[string]any{"roles": roles, "permissions": perms, "actions": s.actionsOf(roles)}
}

// ---- 动作裁决（P7-M1 细粒度层） ----

// actionsOf 角色列表的动作并集。
func (s *Service) actionsOf(roles []string) []string {
	if len(roles) == 0 {
		return []string{}
	}
	var rows []RoleAction
	s.db.Where("role_name IN ?", roles).Find(&rows)
	set := map[string]struct{}{}
	for _, r := range rows {
		set[r.Action] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for _, a := range AllActionKeys() { // 按目录顺序输出
		if _, ok := set[a]; ok {
			out = append(out, a)
		}
	}
	return out
}

func (s *Service) projectIDsOf(role string) []uint {
	var rows []RoleProject
	s.db.Where("role_name = ?", role).Find(&rows)
	ids := make([]uint, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ProjectID)
	}
	return ids
}

// canAction 细粒度裁决：超管全量；未知动作一律拒绝；其余查 role_actions。
func (s *Service) canAction(isAdmin bool, roles []string, action string) bool {
	if isAdmin {
		return true
	}
	if !validActionKey(action) {
		return false
	}
	var cnt int64
	s.db.Model(&RoleAction{}).Where("role_name IN ? AND action = ?", roles, action).Count(&cnt)
	return cnt > 0
}

// projectScopeOf 项目范围：任一内置可分配角色 = 全局；仅自定义角色 = 授权项目并集，
// 且自定义角色未配任何项目行时视为全局。
func (s *Service) projectScopeOf(roles []string) (all bool, ids []uint) {
	builtin := builtinRoleSet()
	for _, r := range roles {
		if builtin[r] {
			return true, nil
		}
	}
	if len(roles) == 0 {
		return true, nil
	}
	var rows []RoleProject
	s.db.Where("role_name IN ?", roles).Find(&rows)
	if len(rows) == 0 {
		return true, nil
	}
	seen := map[uint]struct{}{}
	for _, r := range rows {
		seen[r.ProjectID] = struct{}{}
	}
	ids = make([]uint, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	return false, ids
}

// ---- 包级 gate：handler 层一行调用（单实例装配，NewService 设置） ----

var gateService *Service

const subjectCtxKey = "rbac.subject"

// subject 请求主体（中间件查库后塞入，gate 免二次查询）。
type subject struct {
	IsAdmin bool
	Roles   []string
}

// Can 业务动作裁决（P7-M1）：handler gate 入口。
// 例：if !rbac.Can(c, "config.reveal") { httpx.Fail(...403...) }
func Can(c *gin.Context, action string) bool {
	claims := auth.ClaimsFromContext(c)
	if claims == nil {
		return false
	}
	if claims.IsAdmin {
		return true
	}
	if v, ok := c.Get(subjectCtxKey); ok {
		if s, ok := v.(subject); ok {
			return gateService.canAction(false, s.Roles, action)
		}
	}
	return false
}

// ProjectScope 当前请求的项目范围（all=true 不限）。
func ProjectScope(c *gin.Context) (all bool, ids []uint) {
	claims := auth.ClaimsFromContext(c)
	if claims == nil {
		return false, nil
	}
	if claims.IsAdmin {
		return true, nil
	}
	if v, ok := c.Get(subjectCtxKey); ok {
		if s, ok := v.(subject); ok {
			return gateService.projectScopeOf(s.Roles)
		}
	}
	return false, nil
}

// InProjectScope 项目 ID 是否在当前请求范围内。
func InProjectScope(c *gin.Context, projectID uint) bool {
	all, ids := ProjectScope(c)
	if all {
		return true
	}
	for _, id := range ids {
		if id == projectID {
			return true
		}
	}
	return false
}

// ---- 自定义角色 CRUD ----

var roleNameRe = regexp.MustCompile(`^[\p{Han}A-Za-z0-9_-]{2,32}$`)

type SaveRoleInput struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Actions     []string `json:"actions"`
	ProjectIDs  []uint   `json:"projectIds"`
}

// operatorActions 操作者（admin）自身动作集：创建/编辑时动作集不得超出（D3 授予面校验）。
func (s *Service) operatorActions(isAdmin bool) []string {
	return AllActionKeys() // /roles 管理路由仅 admin（超管放行）可达，恒为全集；留作扩展点
}

func (s *Service) knownRole(name string) bool {
	var cnt int64
	s.db.Model(&Role{}).Where("name = ?", name).Count(&cnt)
	return cnt > 0
}

func (s *Service) isRoleName(name string) bool {
	return s.knownRole(name)
}

// CreateRole 创建自定义角色：写元数据/动作集/项目范围，并按动作目录生成 casbin 策略。
func (s *Service) CreateRole(in SaveRoleInput, operatorIsAdmin bool) (*Role, error) {
	name := strings.TrimSpace(in.Name)
	if !roleNameRe.MatchString(name) || strings.Contains(name, ",") {
		return nil, fmt.Errorf("角色名须为 2~32 位中英文/数字/下划线/连字符，且不含逗号")
	}
	if builtinRoleSet()[name] {
		return nil, fmt.Errorf("%s 为内置角色名，不可占用", name)
	}
	if s.knownRole(name) {
		return nil, fmt.Errorf("角色 %s 已存在", name)
	}
	// 名字空间防撞：用户名也是 casbin sub（M6 用户级策略），同名会劫持授权
	if u, err := s.users.GetByUsername(context.Background(), name); err == nil && u != nil {
		return nil, fmt.Errorf("角色名与用户名 %s 冲突", name)
	}
	if err := s.validateActions(in.Actions, operatorIsAdmin); err != nil {
		return nil, err
	}
	role := &Role{Name: name, Description: in.Description}
	if err := s.db.Create(role).Error; err != nil {
		return nil, fmt.Errorf("创建角色失败: %w", err)
	}
	if err := s.writeRoleExtras(name, in, false); err != nil {
		return nil, err
	}
	if err := s.syncCustomRolePolicies(name, in.Actions); err != nil {
		return nil, err
	}
	return role, nil
}

// UpdateRole 更新角色：自定义角色可改动作/项目范围/描述；内置角色仅可改动作集
// （其 casbin 策略是种子+人为矩阵调整，不动）。
func (s *Service) UpdateRole(name string, in SaveRoleInput, operatorIsAdmin bool) error {
	var role Role
	if err := s.db.Where("name = ?", name).First(&role).Error; err != nil {
		return fmt.Errorf("角色不存在")
	}
	if err := s.validateActions(in.Actions, operatorIsAdmin); err != nil {
		return err
	}
	if err := s.db.Model(&role).Update("description", in.Description).Error; err != nil {
		return fmt.Errorf("更新角色失败: %w", err)
	}
	if err := s.writeRoleExtras(name, in, role.Builtin); err != nil {
		return err
	}
	if !role.Builtin {
		if err := s.syncCustomRolePolicies(name, in.Actions); err != nil {
			return err
		}
	}
	return nil
}

// DeleteRole 删除自定义角色；有用户绑定或内置角色拒绝。
func (s *Service) DeleteRole(name string) error {
	var role Role
	if err := s.db.Where("name = ?", name).First(&role).Error; err != nil {
		return fmt.Errorf("角色不存在")
	}
	if role.Builtin {
		return fmt.Errorf("内置角色不可删除")
	}
	if counts := s.userRoleCounts(); counts[name] > 0 {
		return fmt.Errorf("仍有 %d 个用户绑定该角色，请先解绑", counts[name])
	}
	if err := s.db.Where("name = ?", name).Delete(&Role{}).Error; err != nil {
		return err
	}
	s.db.Where("role_name = ?", name).Delete(&RoleAction{})
	s.db.Where("role_name = ?", name).Delete(&RoleProject{})
	if _, err := s.enforcer.RemoveFilteredPolicy(0, name); err != nil {
		return fmt.Errorf("清除角色策略失败: %w", err)
	}
	return nil
}

// validateActions 动作集合法性 + 授予面（⊆ 操作者动作集）。
func (s *Service) validateActions(actions []string, operatorIsAdmin bool) error {
	allowed := s.operatorActions(operatorIsAdmin)
	allowedSet := map[string]struct{}{}
	for _, a := range allowed {
		allowedSet[a] = struct{}{}
	}
	seen := map[string]struct{}{}
	for _, a := range actions {
		if !validActionKey(a) {
			return fmt.Errorf("未知动作: %s", a)
		}
		if _, ok := allowedSet[a]; !ok {
			return fmt.Errorf("动作 %s 超出你自身的权限范围", a)
		}
		if _, dup := seen[a]; dup {
			return fmt.Errorf("动作 %s 重复", a)
		}
		seen[a] = struct{}{}
	}
	return nil
}

// writeRoleExtras 整体替换动作集与项目范围（内置角色忽略项目范围=恒全局）。
func (s *Service) writeRoleExtras(name string, in SaveRoleInput, builtin bool) error {
	if err := s.db.Where("role_name = ?", name).Delete(&RoleAction{}).Error; err != nil {
		return err
	}
	for _, a := range in.Actions {
		if err := s.db.Create(&RoleAction{RoleName: name, Action: a}).Error; err != nil {
			return err
		}
	}
	if !builtin {
		if err := s.db.Where("role_name = ?", name).Delete(&RoleProject{}).Error; err != nil {
			return err
		}
		for _, id := range in.ProjectIDs {
			if id == 0 {
				continue
			}
			if err := s.db.Create(&RoleProject{RoleName: name, ProjectID: id}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// syncCustomRolePolicies 按动作目录整体重写自定义角色的 casbin 策略：
// 基础读集 + 所选动作的路由面。
func (s *Service) syncCustomRolePolicies(name string, actions []string) error {
	if _, err := s.enforcer.RemoveFilteredPolicy(0, name); err != nil {
		return fmt.Errorf("清除角色策略失败: %w", err)
	}
	want := map[Policy]struct{}{}
	add := func(ps []Policy) {
		for _, p := range ps {
			p.Role = name
			want[p] = struct{}{}
		}
	}
	add(customBaseRoutes)
	for _, a := range actions {
		if def, ok := actionIndex[a]; ok {
			add(def.Routes)
		}
	}
	for p := range want {
		if has, _ := s.enforcer.HasPolicy(p.Role, p.Path, p.Act); !has {
			if _, err := s.enforcer.AddPolicy(p.Role, p.Path, p.Act); err != nil {
				return fmt.Errorf("写入角色策略失败: %w", err)
			}
		}
	}
	return nil
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
