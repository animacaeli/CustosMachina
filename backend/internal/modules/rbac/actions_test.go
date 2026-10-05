// actions_test.go P7-M1 权限专项：自定义角色/动作裁决/项目范围单测。
package rbac

import (
	"testing"

	"github.com/custos-machina/backend/internal/modules/identity"
)

func TestSeedRoles_BuiltinDefaults(t *testing.T) {
	svc := newTestEnforcer(t)
	var devActions []RoleAction
	svc.db.Where("role_name = ?", "dev").Find(&devActions)
	if len(devActions) != 1 || devActions[0].Action != "config.view" {
		t.Fatalf("dev 默认动作应为 [config.view]（reveal 收紧），实际 %v", devActions)
	}
	var adminActions []RoleAction
	svc.db.Where("role_name = ?", "admin").Find(&adminActions)
	if len(adminActions) != len(ActionCatalog) {
		t.Fatalf("admin 默认动作应为目录全集，实际 %d 项", len(adminActions))
	}
}

func TestCreateRole_CustomActionsAndPolicies(t *testing.T) {
	svc := newTestEnforcer(t)
	role, err := svc.CreateRole(SaveRoleInput{
		Name:        "值班运维",
		Description: "编辑+下发+终端，不可看密钥",
		Actions:     []string{"config.edit", "config.deploy", "terminal.access"},
	}, true)
	if err != nil {
		t.Fatalf("创建自定义角色失败: %v", err)
	}
	if role.Builtin {
		t.Fatal("自定义角色不应为内置")
	}
	// casbin 策略已生成：基础读集 + 动作路由
	ok, _ := EnforceAny(svc.enforcer, []string{"值班运维"}, "/config-files/3", "PUT")
	if !ok {
		t.Error("config.edit 动作应放行 PUT /config-files/3")
	}
	ok, _ = EnforceAny(svc.enforcer, []string{"值班运维"}, "/config-files/3", "GET")
	if !ok {
		t.Error("基础读集应放行 GET /config-files/3")
	}
	ok, _ = EnforceAny(svc.enforcer, []string{"值班运维"}, "/users", "GET")
	if ok {
		t.Error("管理面路由不在动作目录，不应放行")
	}
	// 动作裁决
	if !svc.canAction(false, []string{"值班运维"}, "config.deploy") {
		t.Error("持有 config.deploy 动作应裁决通过")
	}
	if svc.canAction(false, []string{"值班运维"}, "config.reveal") {
		t.Error("未持有 config.reveal 动作应拒绝（验收 1）")
	}
	if svc.canAction(false, []string{"值班运维"}, "不存在.action") {
		t.Error("未知动作应一律拒绝")
	}
}

func TestCreateRole_Guards(t *testing.T) {
	svc := newTestEnforcer(t)
	if _, err := svc.CreateRole(SaveRoleInput{Name: "admin", Actions: nil}, true); err == nil {
		t.Error("内置角色名不可占用")
	}
	if _, err := svc.CreateRole(SaveRoleInput{Name: "bad,comma", Actions: nil}, true); err == nil {
		t.Error("含逗号的角色名应拒绝（users.Roles 逗号串载体）")
	}
	if _, err := svc.CreateRole(SaveRoleInput{Name: "测试超管", Actions: nil}, true); err == nil {
		t.Error("角色名与用户名冲突应拒绝（同为 casbin sub）")
	}
	if _, err := svc.CreateRole(SaveRoleInput{Name: "越权角色", Actions: []string{"no.such"}}, true); err == nil {
		t.Error("未知动作应拒绝")
	}
}

func TestProjectScope(t *testing.T) {
	svc := newTestEnforcer(t)
	if _, err := svc.CreateRole(SaveRoleInput{Name: "项目A运维", Actions: []string{"config.view"}, ProjectIDs: []uint{1, 2}}, true); err != nil {
		t.Fatalf("创建角色失败: %v", err)
	}
	all, ids := svc.projectScopeOf([]string{"项目A运维"})
	if all || len(ids) != 2 {
		t.Fatalf("scoped 角色应返回授权项目并集，实际 all=%v ids=%v", all, ids)
	}
	// 内置角色 = 全局
	all, _ = svc.projectScopeOf([]string{"dev"})
	if !all {
		t.Error("内置角色应为全局范围")
	}
	// 无项目行的自定义角色 = 全局
	if _, err := svc.CreateRole(SaveRoleInput{Name: "全局观察", Actions: []string{"config.view"}}, true); err != nil {
		t.Fatalf("创建角色失败: %v", err)
	}
	all, _ = svc.projectScopeOf([]string{"全局观察"})
	if !all {
		t.Error("未配项目的自定义角色应为全局")
	}
	// 混合：内置 + scoped → 全局
	all, _ = svc.projectScopeOf([]string{"dev", "项目A运维"})
	if !all {
		t.Error("含任一内置角色应为全局")
	}
}

func TestUpdateAndDeleteRole(t *testing.T) {
	svc := newTestEnforcer(t)
	if _, err := svc.CreateRole(SaveRoleInput{Name: "临时角色", Actions: []string{"config.view"}}, true); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	// 更新动作集：casbin 策略随之重写
	if err := svc.UpdateRole("临时角色", SaveRoleInput{Actions: []string{"cron.trigger"}}, true); err != nil {
		t.Fatalf("更新失败: %v", err)
	}
	if svc.canAction(false, []string{"临时角色"}, "config.view") {
		t.Error("移除后 config.view 应失效")
	}
	if !svc.canAction(false, []string{"临时角色"}, "cron.trigger") {
		t.Error("新增后 cron.trigger 应生效")
	}
	// 内置角色动作可调、项目范围忽略
	if err := svc.UpdateRole("ops", SaveRoleInput{Actions: []string{"config.view"}, ProjectIDs: []uint{9}}, true); err != nil {
		t.Fatalf("内置角色动作更新失败: %v", err)
	}
	var cnt int64
	svc.db.Model(&RoleProject{}).Where("role_name = ?", "ops").Count(&cnt)
	if cnt != 0 {
		t.Error("内置角色不应写项目范围（恒全局）")
	}
	// 删除：无绑定可删，策略同步清理
	if err := svc.DeleteRole("临时角色"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if ps, _ := svc.enforcer.GetFilteredPolicy(0, "临时角色"); len(ps) != 0 {
		t.Error("删除后 casbin 策略应清空")
	}
	// 有绑定不可删
	if _, err := svc.CreateRole(SaveRoleInput{Name: "被绑角色", Actions: []string{"config.view"}}, true); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	ctx := t.Context()
	u := &identity.User{DisplayName: "绑定用户", Roles: "被绑角色"}
	if err := svc.users.Create(ctx, u); err != nil {
		t.Fatalf("建用户失败: %v", err)
	}
	if err := svc.DeleteRole("被绑角色"); err == nil {
		t.Error("有用户绑定的角色应拒绝删除")
	}
}

func TestIdentityHooks(t *testing.T) {
	svc := newTestEnforcer(t) // NewService 已装配 identity 钩子
	if _, err := svc.CreateRole(SaveRoleInput{Name: "审计角色", Actions: []string{"config.reveal"}}, true); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	// 自定义角色经钩子可分配
	if err := identity.ValidateRoles("dev,审计角色"); err != nil {
		t.Errorf("自定义角色应可分配: %v", err)
	}
	if err := identity.ValidateRoles("ghost角色"); err == nil {
		t.Error("未知角色应拒绝")
	}
	// 用户名撞角色名
	if !identity.UsernameReserved("审计角色") {
		t.Error("角色名应视为用户名保留字")
	}
	if identity.UsernameReserved("随便什么") {
		t.Error("非角色名不应被保留")
	}
}

func TestListRoles_Extended(t *testing.T) {
	svc := newTestEnforcer(t)
	if _, err := svc.CreateRole(SaveRoleInput{Name: "列表角色", Actions: []string{"config.view"}}, true); err != nil {
		t.Fatalf("创建失败: %v", err)
	}
	roles := svc.ListRoles()
	found := false
	for _, r := range roles {
		if r.Name == "列表角色" && len(r.Actions) == 1 && r.Actions[0] == "config.view" {
			found = true
		}
	}
	if !found {
		t.Error("ListRoles 应包含自定义角色及其动作集")
	}
	// dev 收紧点在列表可见
	for _, r := range roles {
		if r.Name == "dev" {
			for _, a := range r.Actions {
				if a == "config.reveal" {
					t.Error("dev 默认动作集不应含 config.reveal（收紧点）")
				}
			}
		}
	}
}
