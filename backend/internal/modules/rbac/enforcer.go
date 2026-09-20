// Package rbac 基于 casbin 的权限裁决层（FR3）：后端为唯一裁决方。
// 模型：角色无继承的扁平 RBAC（角色间权限通过策略包含关系表达，避免 g 规则复杂化）；
// 对象为 API 路径模板（keyMatch：无 * 等值匹配，/services/* 前缀匹配），act 为 HTTP 方法。
// 本地超管（IsAdmin）在中间件层直接放行，不进策略表。
package rbac

import (
	"fmt"

	"github.com/casbin/casbin/v2"
	"github.com/casbin/casbin/v2/model"
	gormadapter "github.com/casbin/gorm-adapter/v3"
	"gorm.io/gorm"
)

const modelText = `
[request_definition]
r = sub, obj, act

[policy_definition]
p = sub, obj, act

[policy_effect]
e = some(where (p.eft == allow))

[matchers]
m = r.sub == p.sub && keyMatch(r.obj, p.obj) && (r.act == p.act || regexMatch(r.act, p.act))
`

// 内置角色（FR3.2）。角色集合固定，权限矩阵可经 API 调整。
var BuiltinRoles = []string{"admin", "ops", "dev", "guest"}

// 默认权限矩阵：admin 全量（中间件放行，此处仅作展示），其余按资源点。
// 资源点规划：services（服务操作）、ci、config（配置读写）、alerts（告警处理）、users/roles（系统设置）。
var defaultPolicies = [][]string{
	{"ops", "/services/*", "GET|POST|PUT"},
	{"ops", "/services", "GET|POST|PUT"},
	{"ops", "/alerts/*", "GET|PUT"},
	{"ops", "/alerts", "GET"},
	{"ops", "/config/*", "GET|PUT"},
	{"dev", "/services/*", "GET"},
	{"dev", "/services", "GET"},
	{"dev", "/alerts", "GET"},
	{"dev", "/alerts/*", "GET"},
	{"guest", "/services", "GET"},
	{"guest", "/services/*", "GET"},
}

// NewEnforcer 构建 casbin enforcer。
// 默认矩阵仅在策略表完全为空（首次启动）时种入；之后管理员的任何调整
// （含删除默认条目）都不会被重启覆盖。策略表 casbin_rule 由 gorm-adapter 管理。
func NewEnforcer(db *gorm.DB) (*casbin.SyncedEnforcer, func(), error) {
	adapter, err := gormadapter.NewAdapterByDBUseTableName(db, "casbin_", "rule")
	if err != nil {
		return nil, nil, fmt.Errorf("初始化 casbin 适配器失败: %w", err)
	}
	m, err := model.NewModelFromString(modelText)
	if err != nil {
		return nil, nil, fmt.Errorf("解析 casbin 模型失败: %w", err)
	}
	e, err := casbin.NewSyncedEnforcer(m, adapter)
	if err != nil {
		return nil, nil, fmt.Errorf("初始化 casbin 失败: %w", err)
	}
	if err := e.LoadPolicy(); err != nil {
		return nil, nil, fmt.Errorf("加载 casbin 策略失败: %w", err)
	}
	if existing, _ := e.GetPolicy(); len(existing) == 0 {
		for _, p := range defaultPolicies {
			if _, err := e.AddPolicy(p[0], p[1], p[2]); err != nil {
				return nil, nil, err
			}
		}
	}
	// 未启用 auto-load/watcher，无需显式停止；保留 cleanup 供未来扩展。
	cleanup := func() {}
	return e, cleanup, nil
}

// EnforceAny 对用户的角色逐一裁决，任一命中即放行。
func EnforceAny(e *casbin.SyncedEnforcer, roles []string, obj, act string) (bool, error) {
	if len(roles) == 0 {
		return false, nil
	}
	for _, role := range roles {
		ok, err := e.Enforce(role, obj, act)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}
