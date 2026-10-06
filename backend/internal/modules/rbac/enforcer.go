// Package rbac 基于 casbin 的权限裁决层（FR3）：后端为唯一裁决方。
// 模型：角色无继承的扁平 RBAC（角色间权限通过策略包含关系表达，避免 g 规则复杂化）；
// 对象为 API 路径模板（keyMatch：无 * 等值匹配，/services/* 前缀匹配），act 为 HTTP 方法。
// 本地超管（IsAdmin）在中间件层直接放行，不进策略表。
package rbac

import (
	"fmt"
	"strconv"

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
	{"admin", "/servers/*/terminal", "GET"}, // 展示用：终端实际裁决在 middleware/handler 双 gate，通配不生效（仅 admin 角色 + 逐主机 ACL）
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
	// AI 对话（P6 M1）：会话归属本人、挂载经角色过滤与 DLP，dev 可用
	{"dev", "/ai/chat", "GET|POST"},
	{"dev", "/ai/chat/*", "GET|POST|PUT|DELETE"},
	// AI 技能（P6 M3）：列表全角色（命令面板），写操作仅 admin（上方 admin 通配已覆盖）
	{"dev", "/ai/skills", "GET"},
	// v5：项目与通知群资源点（projects / notify 模块，第三阶段 M1）
	{"admin", "/projects", "GET|POST|PUT|DELETE"},
	{"admin", "/projects/*", "GET|PUT|DELETE"},
	{"ops", "/projects", "GET"},
	{"ops", "/projects/*", "GET"},
	{"dev", "/projects", "GET"},
	{"dev", "/projects/*", "GET"},
	{"admin", "/notify-groups", "GET|POST|PUT|DELETE"},
	{"admin", "/notify-groups/*", "GET|PUT|DELETE|POST"},
	// v8：统一通知路由规则管理（P5 M1，仅 admin；投递走服务端事件，无 API 操作面）
	{"admin", "/notify-rules", "GET|POST|PUT|DELETE"},
	{"admin", "/notify-rules/*", "GET|PUT|DELETE|POST"},
	// v13：AI 中转层配置（P5 M6）——仅 admin（API key 管理）
	{"admin", "/ai", "GET|PUT|POST"},
	{"admin", "/ai/*", "GET|PUT|POST|DELETE"},
	{"admin", "/mcp/tokens", "GET|POST"},
	{"admin", "/mcp/tokens/*", "POST"},
	// v12：证书管理（P5 M5）——admin/ops（DNS 凭证属密钥管理，dev 不可见）
	{"admin", "/certs", "GET|POST|PUT|DELETE"},
	{"admin", "/certs/*", "GET|PUT|DELETE|POST"},
	{"ops", "/certs", "GET|POST|PUT|DELETE"},
	{"ops", "/certs/*", "GET|PUT|DELETE|POST"},
	// v11：配置文件管理（P5 M4）——admin/ops 全量（明文 reveal 亦 admin/ops，落审计），dev 只读
	{"admin", "/config-files", "GET|POST|PUT|DELETE"},
	{"admin", "/config-files/*", "GET|PUT|DELETE|POST"},
	{"ops", "/config-files", "GET|POST|PUT|DELETE"},
	{"ops", "/config-files/*", "GET|PUT|DELETE|POST"},
	{"dev", "/config-files", "GET"},
	{"dev", "/config-files/*", "GET"},
	// v17：配置拉取凭证（P6 M5）——仅 admin（服务间凭证签发属管理动作）
	{"admin", "/config-pull-tokens", "GET|POST|PUT"},
	{"admin", "/config-pull-tokens/*", "PUT"},
	// v18：配置中心同步/对账（P6 M7，AgileConfig 纯后端通道）——admin/ops 运维职能
	// v19：终端会话审计与细粒度授权（P6-M6 堡垒机）——admin 专属
	{"admin", "/server-terminals", "GET"},
	{"admin", "/server-terminals/*", "GET"},
	{"admin", "/rbac/terminal-acls", "GET|PUT"},
	{"admin", "/rbac/terminal-acls/*", "GET|PUT"},
	{"admin", "/config-kv", "GET|POST|PUT"},
	{"admin", "/config-kv/*", "GET|POST|PUT"},
	{"ops", "/config-kv", "GET|POST|PUT"},
	{"ops", "/config-kv/*", "GET|POST|PUT"},
	// v10：O2 告警闭环（P5 M3）——admin/ops 均可管理告警模板（运维职能）
	{"admin", "/observ/alerts", "GET|POST|PUT|DELETE"},
	{"admin", "/observ/alerts/*", "GET|PUT|DELETE|POST"},
	{"admin", "/observ/o2-settings", "GET|PUT"},
	{"ops", "/observ/alerts", "GET|POST|PUT|DELETE"},
	{"ops", "/observ/alerts/*", "GET|PUT|DELETE|POST"},
	{"ops", "/observ/o2-settings", "GET|PUT"},
	// v14：告警模板化（R1）——模板 CRUD 仅 admin；ops/dev 可读（项目侧下拉）
	// 与渲染预览；dev 经 /observ/alerts/from-template 实例化（SQL 服务端渲染），
	// 删除/查看走 alerts 既有路径（handler 层再限项目侧）
	{"admin", "/observ/alert-templates", "GET|POST|PUT|DELETE"},
	{"admin", "/observ/alert-templates/*", "GET|PUT|DELETE|POST"},
	{"ops", "/observ/alert-templates", "GET"},
	{"ops", "/observ/alert-templates/*", "GET|POST"},
	{"dev", "/observ/alert-templates", "GET"},
	{"dev", "/observ/alert-templates/*", "GET|POST"},
	{"dev", "/observ/alerts", "GET|POST"},
	// v25：dev 收掉 DELETE——删除属管理动作，dev 无项目归属（InProjectScope
	// 对内置 dev 恒真=守卫空转），留着即「任意项目告警可删」（v0.12.1 复核 N3）
	{"dev", "/observ/alerts/*", "GET|POST"},
	// v9：备份任务管理（P5 M2）——admin 全量，ops 只读（可触发手动备份）
	{"admin", "/backup-jobs", "GET|POST|PUT|DELETE"},
	{"admin", "/backup-jobs/*", "GET|PUT|DELETE|POST"},
	{"ops", "/backup-jobs", "GET"},
	{"ops", "/backup-jobs/*", "GET|POST"},
	{"admin", "/notify-settings", "GET|PUT"},
	{"admin", "/notify-settings/*", "GET|PUT"},
	{"admin", "/ci", "GET|PUT"},
	{"admin", "/ci/*", "GET|PUT"},
	{"admin", "/registries", "GET|POST|PUT|DELETE"},
	{"admin", "/registries/*", "GET|PUT|DELETE"},
	{"admin", "/builds", "GET"},
	{"ops", "/builds", "GET"},
	{"dev", "/builds", "GET"},
	{"ops", "/project-branches", "GET"},
	{"ops", "/project-branches/*", "GET"},
	// v5（M3）：发布与伸缩。正式发布高危仅 admin；灰度发布 admin+ops；伸缩 admin+ops
	{"admin", "/releases", "GET|POST"},
	{"admin", "/releases/*", "POST"},
	{"ops", "/releases", "GET"},
	{"dev", "/releases", "GET"},
	// v5（M4）：灰度策略。管理 admin+ops（灰度发布允许 ops），dev 只读
	{"admin", "/canary-policies", "GET|POST|PUT|DELETE"},
	{"admin", "/canary-policies/*", "GET|POST|PUT|DELETE"},
	{"ops", "/canary-policies", "GET|POST|PUT|DELETE"},
	{"ops", "/canary-policies/*", "GET|POST|PUT|DELETE"},
	// v5（M5）：测试槽位。占用任何登录用户可用（exemptAnyRole 不动，种到各角色）；
	// 释放/续期的"本人或 admin"校验在 slots 模块内强制
	{"admin", "/slots", "GET|POST"},
	{"admin", "/slots/*", "GET|POST"},
	{"ops", "/slots", "GET|POST"},
	{"ops", "/slots/*", "GET|POST"},
	{"dev", "/slots", "GET|POST"},
	{"dev", "/slots/*", "GET|POST"},
	{"guest", "/slots", "GET|POST"},
	{"guest", "/slots/*", "GET|POST"},
	{"dev", "/project-branches", "GET"},
	{"dev", "/project-branches/*", "GET"},
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
	// v6→v7（第四阶段 M1）：定时任务/脚本库。写操作 admin 角色（计划"仅 admin 可建可改"，
	// 超管由中间件放行）；ops/dev 只读列表与运行历史；手动触发在 handler 层再拦一层 IsAdmin
	{"admin", "/cron-scripts", "GET|POST|PUT|DELETE"},
	{"admin", "/cron-scripts/*", "GET|PUT|DELETE"},
	{"admin", "/cron-jobs", "GET|POST|PUT|DELETE"},
	{"admin", "/cron-jobs/*", "GET|POST|PUT|DELETE"},
	{"ops", "/cron-scripts", "GET"},
	{"ops", "/cron-jobs", "GET"},
	{"ops", "/cron-runs", "GET"},
	{"dev", "/cron-scripts", "GET"},
	{"dev", "/cron-jobs", "GET"},
	{"dev", "/cron-runs", "GET"},
	// v6（M3）：观测组件一键部署。admin/ops 可部署管理，dev 只读
	{"admin", "/observ", "GET|POST|PUT"},
	{"admin", "/observ/*", "GET|POST|PUT"},
	{"ops", "/observ", "GET|POST|PUT"},
	{"ops", "/observ/*", "GET|POST|PUT"},
	{"dev", "/observ", "GET"},
	{"dev", "/observ/*", "GET"},
	// v6（M4）：SFTP 文件管理。admin/ops 可写，dev 只读浏览
	{"admin", "/server-files", "GET|POST"},
	{"admin", "/server-files/*", "GET|POST"},
	{"ops", "/server-files", "GET|POST"},
	{"ops", "/server-files/*", "GET|POST"},
	{"dev", "/server-files", "GET"},
	{"dev", "/server-files/*", "GET"},
	// v20：自定义角色 CRUD（P7-M1）——admin 可创建/编辑/删除自定义角色与查动作目录
	{"admin", "/roles", "POST"},
	{"admin", "/roles/actions", "GET"},
	{"admin", "/roles/*", "DELETE"},
	// v21：业务告警凭证管理（P7-M5）——admin 签发/吊销业务告警 token
	{"admin", "/notify-business-tokens", "GET|POST"},
	{"admin", "/notify-business-tokens/*", "PUT"},
	// v22：编辑器 AI 助手（P7-M3）——dev 起（编辑器场景 dev 可用，advisory）
	{"admin", "/ai/assist", "POST"},
	{"ops", "/ai/assist", "POST"},
	{"dev", "/ai/assist", "POST"},
	// v23：k3s 集群管理（P8-M3.2）——admin（kubeconfig 属集群凭证管理）
	{"admin", "/k3s-clusters", "GET|POST|DELETE"},
	{"admin", "/k3s-clusters/*", "PUT|POST"},
	// v24：告警事件留痕（v0.12.0 审计）——admin/ops 管理，dev 只读；
	// 通知投递记录（notify_records 读取端）仅 admin
	{"admin", "/observ/alert-events", "GET|POST"},
	{"ops", "/observ/alert-events", "GET|POST"},
	{"dev", "/observ/alert-events", "GET"},
	{"admin", "/notify/records", "GET"},
}

// policySeedVersion 策略种子版本：新增角色/矩阵调整时 +1，
// 已有部署按版本一次性补种（角色在表中无任何策略时才补），不会复活人为删改。
const policySeedVersion = "25" // v20：自定义角色 CRUD（P7-M1）；v19：终端审计 server-terminals + terminal-acls（P6-M6 堡垒机） // v15：MCP 接入凭证（P6 M2，admin）+ /ai/chat dev 放行（M1 遗漏补调——对话会话归属本人，dev 可用）；v14：告警模板化（R1）；v13：ai 资源点（P5 M6）；v12：certs（M5）；v11：config-files（M4）；v10：observ 告警（M3）

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
		if err := db.Where("skey = ?", "rbac.seed_version").First(&setting).Error; err != nil {
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
	if err := db.Where("skey = ?", "rbac.seed_version").First(&setting).Error; err == nil {
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
		// v4→v5：projects/notify-groups 对 admin（新增）与 ops/dev（只读）都是新资源点
		if len(ps) > 0 && oldVersion == "4" {
			entryLevelSeed[p[0]] = true
		}
		// v5/v6→v7：cron 增量资源点在 v6 下发的部署里只对部分角色生效过，
		// observ/server-files 对 ops/dev（以及 admin 的 cron 写权限）都需逐条补齐
		if len(ps) > 0 && (oldVersion == "5" || oldVersion == "6") {
			entryLevelSeed[p[0]] = true
		}
		// v7→v8：notify-rules 对 admin 是新资源点
		if len(ps) > 0 && oldVersion == "7" {
			entryLevelSeed[p[0]] = true
		}
		// v8→v9：backup-jobs 对 admin/ops 都是新资源点
		if len(ps) > 0 && oldVersion == "8" {
			entryLevelSeed[p[0]] = true
		}
		// v9→v10：observ/alerts、observ/o2-settings 对 admin/ops 都是新资源点
		if len(ps) > 0 && oldVersion == "9" {
			entryLevelSeed[p[0]] = true
		}
		// v10→v11：config-files 对 admin/ops/dev 都是新资源点
		if len(ps) > 0 && oldVersion == "10" {
			entryLevelSeed[p[0]] = true
		}
		// v11→v12：certs 对 admin/ops 是新资源点
		if len(ps) > 0 && oldVersion == "11" {
			entryLevelSeed[p[0]] = true
		}
		// v12→v13：ai 对 admin 是新资源点
		if len(ps) > 0 && oldVersion == "12" {
			entryLevelSeed[p[0]] = true
		}
		// v13~v16→v17：告警模板/ai 资源点/mcp tokens/ai skills/config-pull-tokens
		// 对 admin 都是新资源点（v14~v16 历史上未逐版本登记升级条目，按范围补齐）
		if len(ps) > 0 && p[0] == "admin" && oldVersion >= "13" && oldVersion < "17" {
			entryLevelSeed[p[0]] = true
		}
		// v17→v18：config-kv 对 admin/ops/dev 都是新资源点
		if len(ps) > 0 && oldVersion == "17" {
			entryLevelSeed[p[0]] = true
		}
		// v18→v19：server-terminals/terminal-acls 对 admin 是新资源点
		if len(ps) > 0 && p[0] == "admin" && oldVersion == "18" {
			entryLevelSeed[p[0]] = true
		}
		// v19→v20：自定义角色 CRUD 路由对 admin 是新资源点
		if len(ps) > 0 && p[0] == "admin" && oldVersion == "19" {
			entryLevelSeed[p[0]] = true
		}
		// v20→v21：业务告警凭证管理对 admin 是新资源点
		if len(ps) > 0 && p[0] == "admin" && oldVersion == "20" {
			entryLevelSeed[p[0]] = true
		}
		// v21→v22：/ai/assist 对 admin/ops/dev 都是新资源点
		if len(ps) > 0 && oldVersion == "21" {
			entryLevelSeed[p[0]] = true
		}
		// v22→v23：k3s-clusters 对 admin 是新资源点
		if len(ps) > 0 && p[0] == "admin" && oldVersion == "22" {
			entryLevelSeed[p[0]] = true
		}
		// v23→v24：alert-events 对 admin/ops/dev、notify/records 对 admin 都是新资源点
		if len(ps) > 0 && oldVersion == "23" {
			entryLevelSeed[p[0]] = true
		}
		// v24→v25：dev 的 /observ/alerts/* 收掉 DELETE（先移除旧条目再补新；
		// RemovePolicy 对不存在条目为无害 no-op，幂等）。数值比较——字典序
		// 下 "9" > "25"，跨多版直升的部署会漏掉收权
		if v, err := strconv.Atoi(oldVersion); err != nil || v < 25 {
			if _, err := e.RemovePolicy("dev", "/observ/alerts/*", "GET|DELETE|POST"); err == nil {
				if has, _ := e.HasPolicy("dev", "/observ/alerts/*", "GET|POST"); !has {
					_, _ = e.AddPolicy("dev", "/observ/alerts/*", "GET|POST")
				}
				changed = true
			}
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
