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

	"github.com/custos-machina/backend/internal/modules/identity"
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

// 默认权限矩阵：admin 全量（中间件放行，此处仅作展示），其余按资源点。
// 资源点规划：services（服务操作）、ci、config（配置读写）、alerts（告警处理）、users/roles（系统设置）。
var defaultPolicies = [][]string{
	// 普通管理员：用户与角色管理、管理后台（任命 admin 由 identity 层拦住，仅超管）
	{"admin", "/users", "GET|POST"},
	{"ops", "/server-container-stats", "GET"},
	{"ops", "/server-container-stats/*", "GET"},
	{"admin", "/server-container-stats", "GET"},
	{"admin", "/server-container-stats/*", "GET"},
	{"admin", "/users/*", "GET|PUT"},
	{"admin", "/roles", "GET"},
	{"admin", "/roles/*", "GET|PUT"},
	{"admin", "/im-configs", "GET"},
	{"admin", "/im-configs/*", "GET|PUT|POST"},
	{"admin", "/settings/*", "GET|PUT"},
	{"admin", "/servers", "GET|POST|PUT|DELETE"},
	{"admin", "/servers/*", "GET|PUT|DELETE|POST"},
	{"admin", "/server-groups", "GET|POST|PUT|DELETE"},
	{"admin", "/server-groups/*", "GET|PUT|DELETE"},
	{"admin", "/server-metrics", "GET"},
	{"admin", "/server-metrics/*", "GET"},
	{"admin", "/server-events/*", "GET"},
	{"admin", "/servers/*/terminal", "GET"}, // Web 终端：仅 admin（超管中间件直接放行）
	{"admin", "/server-containers", "GET|POST"},
	{"admin", "/server-containers/*", "GET|POST"},
	{"admin", "/server-env", "GET"},
	{"admin", "/server-env/*", "GET"},
	{"admin", "/server-env-guide", "GET"},
	{"admin", "/server-env-guide/*", "GET"},
	{"admin", "/auth/tickets", "POST"},
	{"admin", "/server-compose", "POST"},
	{"admin", "/server-compose/*", "POST"},
	{"admin", "/server-compose/*", "GET|PUT"},
	{"ops", "/servers", "GET|POST|PUT|DELETE"},
	{"ops", "/servers/*", "GET|PUT|DELETE|POST"},
	{"ops", "/server-groups", "GET|POST|PUT|DELETE"},
	{"ops", "/server-groups/*", "GET|PUT|DELETE"},
	{"ops", "/server-metrics", "GET"},
	{"ops", "/server-metrics/*", "GET"},
	{"ops", "/server-events/*", "GET"},
	{"ops", "/server-containers", "GET|POST"},
	{"ops", "/server-containers/*", "GET|POST"},
	{"ops", "/server-env", "GET"},
	{"ops", "/server-env/*", "GET"},
	{"ops", "/server-env-guide", "GET"},
	{"ops", "/server-env-guide/*", "GET"},
	{"ops", "/auth/tickets", "POST"},
	{"ops", "/server-compose", "POST"},
	{"ops", "/server-compose/*", "POST"},
	{"ops", "/server-compose/*", "GET|PUT"},
	{"dev", "/servers", "GET"},
	{"dev", "/servers/*", "GET"},
	{"dev", "/server-groups", "GET"},
	{"dev", "/server-metrics", "GET"},
	{"dev", "/server-metrics/*", "GET"},
	{"dev", "/server-events/*", "GET"},
	{"dev", "/server-containers", "GET"},
	{"dev", "/server-containers/*", "GET"},
	{"dev", "/server-container-stats", "GET"},
	{"dev", "/server-container-stats/*", "GET"},
	{"dev", "/server-env", "GET"},
	{"dev", "/server-env/*", "GET"},
	{"dev", "/auth/tickets", "POST"},
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

// policySeedVersion 策略种子版本：新增角色/矩阵调整时 +1，
// 已有部署按版本一次性补种（角色在表中无任何策略时才补），不会复活人为删改。
const policySeedVersion = "4" // v4：新增服务器/分组资源点（resources 模块 M1）

// NewEnforcer 构建 casbin enforcer。
// 首次启动（表全空）种入全部默认矩阵；后续仅当种子版本升级时，
// 为表中完全没有策略的新角色补种（通过 platform_settings 记录版本）。
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
	seeded := false
	if existing, _ := e.GetPolicy(); len(existing) == 0 {
		for _, p := range defaultPolicies {
			if _, err := e.AddPolicy(p[0], p[1], p[2]); err != nil {
				return nil, nil, err
			}
		}
		seeded = true
	}
	if !seeded {
		migrated, err := migrateSeedVersion(db, e)
		if err != nil {
			return nil, nil, err
		}
		seeded = migrated
	}
	if seeded {
		var setting identity.PlatformSetting
		if err := db.Where("key = ?", "rbac.seed_version").First(&setting).Error; err != nil {
			setting = identity.PlatformSetting{Key: "rbac.seed_version"}
		}
		setting.Value = policySeedVersion
		if err := db.Save(&setting).Error; err != nil {
			return nil, nil, err
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

// migrateSeedVersion 种子版本升级时为"表中无任何策略"的角色补种默认矩阵。
// v2 存在"只种首条"的 bug（admin 角色残留 1 条），v3 对 admin 做条目级补齐。
func migrateSeedVersion(db *gorm.DB, e *casbin.SyncedEnforcer) (bool, error) {
	oldVersion := ""
	var setting identity.PlatformSetting
	if err := db.Where("key = ?", "rbac.seed_version").First(&setting).Error; err == nil {
		if setting.Value == policySeedVersion {
			return false, nil
		}
		oldVersion = setting.Value
	}
	changed := false
	// 先收集表中完全无策略的角色，再整角色补种（避免种一条后误判"已有策略"）
	roleNeedsSeed := map[string]bool{}
	entryLevelSeed := map[string]bool{} // v2 残缺角色：逐条补齐
	for _, p := range defaultPolicies {
		if roleNeedsSeed[p[0]] {
			continue
		}
		ps, _ := e.GetFilteredPolicy(0, p[0])
		roleNeedsSeed[p[0]] = len(ps) == 0
		if len(ps) > 0 && p[0] == "admin" && oldVersion == "2" {
			entryLevelSeed[p[0]] = true // v2 bug 残留，按条目补齐
		}
		// v3→v4：servers/server-groups 是新资源点，admin/ops 已有其他策略，
		// 需逐条补齐（HasPolicy 去重，不覆盖人为调整过的旧条目）
		if len(ps) > 0 && (p[0] == "admin" || p[0] == "ops") && oldVersion == "3" {
			entryLevelSeed[p[0]] = true
		}
	}
	for _, p := range defaultPolicies {
		if !roleNeedsSeed[p[0]] && !entryLevelSeed[p[0]] {
			continue // 该角色已有策略（含管理员调整过），不覆盖
		}
		if has, _ := e.HasPolicy(p[0], p[1], p[2]); !has {
			if _, err := e.AddPolicy(p[0], p[1], p[2]); err != nil {
				return changed, err
			}
			changed = true
		}
	}
	return changed, nil
}
