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
	"github.com/custos-machina/backend/internal/pkg/logger"
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
	// v26：guest 收掉 /slots 写权——IM 扫码 JIT 自注册即得 guest，保留 POST
	// 等于「组织内任意扫码者可占用/重建任意项目测试环境」（v0.12.2 复核 N3）。
	// 测试槽位仍对 dev/ops/admin 与授权自定义角色开放
	{"guest", "/slots", "GET"},
	{"guest", "/slots/*", "GET"},
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
	// v27：首页运维态势/就绪度（v0.12.8~v0.12.10 上线）——登录后第一屏，
	// admin/ops/dev 可读；guest 不给（扫码即得的角色，与 v26 收权方向一致）
	{"admin", "/home/summary", "GET"},
	{"admin", "/home/readiness", "GET"},
	{"ops", "/home/summary", "GET"},
	{"ops", "/home/readiness", "GET"},
	{"dev", "/home/summary", "GET"},
	{"dev", "/home/readiness", "GET"},
}

// policySeedVersion 策略种子版本：新增角色/矩阵调整时 +1，
// 已有部署按版本一次性补种（角色在表中无任何策略时才补），不会复活人为删改。
const policySeedVersion = "27" // v27：首页 /home summary+readiness admin/ops/dev 可读（v0.12.14 复核 §7.1——上线时漏种，非超管登录首屏 403）；v20：自定义角色 CRUD（P7-M1）；v19：终端审计 server-terminals + terminal-acls（P6-M6 堡垒机） // v15：MCP 接入凭证（P6 M2，admin）+ /ai/chat dev 放行（M1 遗漏补调——对话会话归属本人，dev 可用）；v14：告警模板化（R1）；v13：ai 资源点（P5 M6）；v12：certs（M5）；v11：config-files（M4）；v10：observ 告警（M3）

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
		// 首次初始化判据：表空 且 无 rbac.seed_version 记录（v0.12.19
		// 复核 P1-B：全表清空是合法配置——依次清空全部内置角色后重启，
		// 不得据此全量重种 196 条默认权限。seed 已存在则交由迁移路径按
		// 精确 delta 处理：seed=当前 → 保持清空；旧版本 → 只补新增面）
		var s identity.PlatformSetting
		hasSeed := db.Where("skey = ?", "rbac.seed_version").First(&s).Error == nil
		if !hasSeed {
			for _, p := range defaultPolicies {
				if _, err := e.AddPolicy(p[0], p[1], p[2]); err != nil {
					return nil, nil, err
				}
			}
			seeded = true
		}
	}
	if !seeded {
		migrated, err := migrateSeedVersion(db, e)
		if err != nil {
			// 迁移失败（如替换持久化错误）：不写版本号——下次启动重试，
			// 不让半迁移永久化（v0.12.19 复核 P1-C）
			return nil, nil, fmt.Errorf("种子迁移失败（保留旧版本号待重试）: %w", err)
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

// migrateSeedVersion 种子版本升级迁移。三层语义（v0.12.19 独立复核 R1/R2
// 补丁后）：①精确 delta——只补「引入版本 > 库版本」的默认条目；其中
// 替换产生的新形态按替换语义分流（见 policyReplacements），升级绝不
// 复活管理员人为删除的权限；②替换/废弃——历史宽形态收窄与纯废弃，
// 旧形态真实存在才替换（防复活），含废弃形态的库升级后不残留旧权限；
// ③不再有「角色首种」——管理员清空某内置角色全部策略是合法配置
// （role.vue 支持编辑内置角色矩阵），迁移不得据此恢复默认矩阵（独立
// 复核 R2：策略数 0 无法区分「从未初始化」与「主动清空」，取不复活）。
// 非法/缺失的版本值：只补 home 基础面并告警，绝不以「极旧部署」名义
// 全量恢复权限；高于当前种子的版本（降级二进制）不迁移不降写。
func migrateSeedVersion(db *gorm.DB, e *casbin.SyncedEnforcer) (bool, error) {
	oldVersion := ""
	var setting identity.PlatformSetting
	if err := db.Where("skey = ?", "rbac.seed_version").First(&setting).Error; err == nil {
		if setting.Value == policySeedVersion {
			return false, nil
		}
		oldVersion = setting.Value
	}
	oldN, perr := strconv.Atoi(oldVersion)
	if perr == nil && oldN > seedVersionNum() {
		// 高于当前种子：降级运行的二进制——不迁移、不降写版本号
		logger.Warnf("[rbac] 种子版本 %q 高于当前二进制 %q（降级运行？），跳过迁移", oldVersion, policySeedVersion)
		return false, nil
	}
	knownSeed := perr == nil
	if !knownSeed {
		logger.Warnf("[rbac] 种子版本缺失或不可解析 %q——仅补 home 基础面，不执行全量补种", oldVersion)
	}

	// ① 精确 delta：引入版本 > 库版本的条目直达（含 v27 的 home 六条——
	// admin/ops/dev 在 defaultPolicies 内走本表；自定义角色见下方特例块）。
	// 替换产生的新形态分流：库版本早于旧形态引入（从未有过该资源）时按
	// 普通新资源点补入；库经历过旧形态时代则跳过——由下方替换块按
	// 「旧形态在才换新」的防复活语义控制（否则 seed 18 库删除过宽形态的
	// 角色会被新形态变相复活，v0.12.19 独立复核 R1）
	if knownSeed {
		for _, p := range defaultPolicies {
			pk := policyKey{p[0], p[1], p[2]}
			if policyIntroducedAt[pk] <= oldN {
				continue // 库版本已知时已有此面或管理员主动删除——不补
			}
			if oldVer, replaced := replacementNewKeys[pk]; replaced && oldN >= oldVer {
				continue // 替换新形态：库经历过旧形态时代——走替换块
			}
			if has, _ := e.HasPolicy(p[0], p[1], p[2]); !has {
				if _, err := e.AddPolicy(p[0], p[1], p[2]); err != nil {
					return false, err
				}
			}
		}
	}

	// ①' v27 home 特例：自定义角色不在 defaultPolicies（customBaseRoutes
	// 动态附加），同样只精确补两条——home 条目在 v27 前不存在，无复活面；
	// v27+ 的库不再重放。guest 不补（扫码即得角色，与 v26 收权方向一致）。
	// 非法 seed（knownSeed=false）时内置三角色也走本块补 home（delta 块
	// 不执行，基础可用性兜底）
	if !knownSeed || oldN < 27 {
		for _, sub := range append(customRoleSubjects(e), "admin", "ops", "dev") {
			for _, obj := range []string{"/home/summary", "/home/readiness"} {
				if has, _ := e.HasPolicy(sub, obj, "GET"); !has {
					if _, err := e.AddPolicy(sub, obj, "GET"); err != nil {
						return false, err
					}
				}
			}
		}
	}

	// ② 替换/废弃：替换版本晚于库版本的记录逐条执行——旧形态真实存在
	// 才移除并补新形态（管理员删除过旧形态则新形态不补，不复活）；纯废弃
	// 只移除旧形态。极旧/非法 seed 传 0（收权方向安全，全部执行；替换
	// 只影响持有旧形态的库，无中生有的新形态不会出现）。
	// 持久化错误上抛（P1-C）：失败不推进版本号，下次启动重试——
	// 吞错会把半迁移永久化（旧宽权限残留或合法新面丢失且不再补）
	denN := 0
	if knownSeed {
		denN = oldN
	}
	if _, err := applyReplacements(e, denN); err != nil {
		return false, fmt.Errorf("策略替换持久化失败: %w", err)
	}

	// 返回 true = 需要推进版本号（调用方据此写回 setting）。走到这里说明
	// 库版本低于当前种子——即使本轮无策略变更（条目人工加过/收权 no-op），
	// 也必须推进，否则每次启动重复跑迁移且版本永久卡死（v0.12.15 复核
	// §八.1-B 场景 C 的另一半根因）
	return true, nil
}

// seedVersionNum 当前种子版本的整数形态（迁移用；版本号单调递增）。
func seedVersionNum() int {
	n, err := strconv.Atoi(policySeedVersion)
	if err != nil {
		return 0
	}
	return n
}

// applyReplacements 按库版本执行历史形态替换/废弃（包级函数可单测——
// 空转测试的教训，v0.12.3 复核）。仅当 r.ver > oldN（替换晚于库版本）
// 才执行；旧形态真实存在才移除并补新——不复活管理员人为删除的策略。
// 持久化错误（Remove/Add 落库失败）立即上抛，由调用方决定不推进版本号
// 以便重试（v0.12.19 复核 P1-C：吞错会使半迁移永久化）。
func applyReplacements(e *casbin.SyncedEnforcer, oldN int) (bool, error) {
	changed := false
	for _, r := range policyReplacements {
		if r.ver <= oldN {
			continue // 替换不晚于库版本——该库已是新形态（或当年已处理）
		}
		removed, err := e.RemovePolicy(r.old[0], r.old[1], r.old[2])
		if err != nil {
			return changed, fmt.Errorf("移除废弃形态 %v 失败: %w", r.old, err)
		}
		if !removed {
			continue // 旧形态不存在（人为删改或从未有过）——不动
		}
		if r.new != nil {
			has, err := e.HasPolicy(r.new[0], r.new[1], r.new[2])
			if err != nil {
				return changed, fmt.Errorf("检查新形态 %v 失败: %w", *r.new, err)
			}
			if !has {
				if _, err := e.AddPolicy(r.new[0], r.new[1], r.new[2]); err != nil {
					return changed, fmt.Errorf("补入新形态 %v 失败: %w", *r.new, err)
				}
			}
		}
		changed = true
	}
	return changed, nil
}

// customRoleSubjects casbin 策略表中的自定义角色名（内置角色之外）。
// 自定义角色由 customBaseRoutes 自动附加基础读集，存量角色靠种子迁移补新面。
func customRoleSubjects(e *casbin.SyncedEnforcer) []string {
	builtin := map[string]bool{}
	for _, r := range identity.BuiltinRoles {
		builtin[r] = true
	}
	seen := map[string]bool{}
	var out []string
	policies, _ := e.GetPolicy()
	for _, p := range policies {
		sub := p[0]
		if builtin[sub] || seen[sub] {
			continue
		}
		seen[sub] = true
		out = append(out, sub)
	}
	return out
}
