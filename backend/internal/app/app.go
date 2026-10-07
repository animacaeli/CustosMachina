// Package app 是组装根（composition root）：聚合所有模块的 wire ProviderSet，
// 并提供跨模块的基础对象（数据库、模块列表）。新增模块只需：实现 server.Module、
// 导出 Set、在下面追加一行 —— 其余无需改动。
package app

import (
	casbin "github.com/casbin/casbin/v2"
	"github.com/google/wire"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/config"
	"github.com/custos-machina/backend/internal/modules/ai"
	"github.com/custos-machina/backend/internal/modules/auth"
	"github.com/custos-machina/backend/internal/modules/backup"
	"github.com/custos-machina/backend/internal/modules/canary"
	"github.com/custos-machina/backend/internal/modules/certs"
	"github.com/custos-machina/backend/internal/modules/ci"
	"github.com/custos-machina/backend/internal/modules/configs"
	cronmod "github.com/custos-machina/backend/internal/modules/cron"
	"github.com/custos-machina/backend/internal/modules/health"
	"github.com/custos-machina/backend/internal/modules/home"
	"github.com/custos-machina/backend/internal/modules/identity"
	k3smod "github.com/custos-machina/backend/internal/modules/k3s"
	mcpmod "github.com/custos-machina/backend/internal/modules/mcp"
	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/modules/observ"
	"github.com/custos-machina/backend/internal/modules/projects"
	"github.com/custos-machina/backend/internal/modules/rbac"
	"github.com/custos-machina/backend/internal/modules/release"
	"github.com/custos-machina/backend/internal/modules/resources"
	"github.com/custos-machina/backend/internal/modules/setup"
	"github.com/custos-machina/backend/internal/modules/slots"
	"github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/database"
	jwtpkg "github.com/custos-machina/backend/internal/pkg/jwt"
	"github.com/custos-machina/backend/internal/server"
)

// P7 版本化迁移的 Go 钩子（存量库建新表；新库 AutoMigrate 已覆盖，钩子幂等无害）。
// 自增主键 CREATE TABLE 写不了 SQLite/MySQL 双方言 DDL，建表走 gorm。
func init() {
	database.RegisterGoHook("0002", func(db *gorm.DB) error {
		return db.AutoMigrate(&rbac.Role{}, &rbac.RoleAction{}, &rbac.RoleProject{})
	})
	database.RegisterGoHook("0003", func(db *gorm.DB) error {
		return db.AutoMigrate(&notify.BusinessToken{})
	})
	// 0006：key→skey 改名（gorm Migrator 按方言转义保留字；幂等：旧列存在才改）
	database.RegisterGoHook("0006", func(db *gorm.DB) error {
		m := db.Migrator()
		if m.HasColumn(&identity.PlatformSetting{}, "key") && !m.HasColumn(&identity.PlatformSetting{}, "skey") {
			return m.RenameColumn(&identity.PlatformSetting{}, "key", "skey")
		}
		return nil
	})
	// 0004/0005/0007：ALTER-ADD-COLUMN 改守卫式 Go 钩——跳级升级时
	// Migrate 先全量 AutoMigrate（列已由当前模型带上），裸 ALTER 会报
	// duplicate column / no such table（2026-10-07 生产事故：v0.9.x 老库
	// 直升 v0.12.3，0005 对从未建过的 ai_conversations 做 ALTER）。
	// SQLite 无 ADD COLUMN IF NOT EXISTS，只能代码层守卫。
	addColIfMissing := func(tbl any, field, column string) func(*gorm.DB) error {
		return func(db *gorm.DB) error {
			m := db.Migrator()
			// 表不存在（跳级升级）直接跳过——由 Migrate 末尾的模型补齐
			// 按当前模型整体建出，列随之带上
			if !m.HasTable(tbl) || m.HasColumn(tbl, field) {
				return nil
			}
			return m.AddColumn(tbl, field)
		}
	}
	database.RegisterGoHook("0004", addColIfMissing(&ci.Build{}, "LogTail", "log_tail"))
	database.RegisterGoHook("0005", func(db *gorm.DB) error {
		m := db.Migrator()
		if m.HasTable("ai_conversations") && !m.HasColumn("ai_conversations", "compact_text") {
			if err := db.Exec("ALTER TABLE ai_conversations ADD COLUMN compact_text TEXT").Error; err != nil {
				return err
			}
		}
		if m.HasTable("ai_conversations") && !m.HasColumn("ai_conversations", "compact_after_id") {
			return db.Exec("ALTER TABLE ai_conversations ADD COLUMN compact_after_id INTEGER NOT NULL DEFAULT 0").Error
		}
		return nil
	})
	database.RegisterGoHook("0007", addColIfMissing(&projects.EnvTarget{}, "ClusterID", "cluster_id"))

	// 0009/0010：v0.12.1~v0.12.2 新表补存量库迁移（复核 N7——新库走
	// AutoMigrate 无感，存量库增量路径不跑 AutoMigrate，只认 SQL+钩子）
	database.RegisterGoHook("0009", func(db *gorm.DB) error {
		return db.AutoMigrate(&observ.AlertEvent{})
	})
	database.RegisterGoHook("0010", func(db *gorm.DB) error {
		return db.AutoMigrate(&k3smod.Cluster{})
	})
	// 0008：gitee webhook 密码存量明文哈希化（sha256 纯计算无密钥依赖；
	// 幂等：已是 64-hex 哈希形态则跳过）
	database.RegisterGoHook("0008", func(db *gorm.DB) error {
		if !db.Migrator().HasTable("ci_global_config") {
			return nil // 跳级升级无此表：由模型补齐建出，无存量可迁移
		}
		var row struct {
			ID           uint
			GiteeWebhook string
		}
		if err := db.Table("ci_global_config").Where("id = ?", 1).Scan(&row).Error; err != nil || row.ID == 0 {
			return err
		}
		if row.GiteeWebhook == "" || ci.IsHashedWebhookPass(row.GiteeWebhook) {
			return nil
		}
		return db.Exec("UPDATE ci_global_config SET gitee_webhook = ? WHERE id = 1", ci.HashWebhookPass(row.GiteeWebhook)).Error
	})
}

// ProvideDBModelList 全量模型清单（模块在此登记）——Migrate 与跳级回归测试共用。
func ProvideDBModelList() []any {
	models := identity.Models()
	models = append(models, rbac.Models()...)
	models = append(models, resources.Models()...)
	models = append(models, notify.Models()...)
	models = append(models, backup.Models()...)
	models = append(models, observ.Models()...)
	models = append(models, configs.Models()...)
	models = append(models, certs.Models()...)
	models = append(models, ai.Models()...)
	models = append(models, ai.ChatModels()...)
	models = append(models, ai.SkillModels()...)
	models = append(models, mcpmod.Models()...)
	models = append(models, projects.Models()...)
	models = append(models, ci.Models()...)
	models = append(models, release.Models()...)
	models = append(models, canary.Models()...)
	models = append(models, slots.Models()...)
	models = append(models, cronmod.Models()...)
	models = append(models, k3smod.Models()...)
	return models
}

// ProvideDB 打开数据库并迁移全部模块的模型。
func ProvideDB(cfg *config.Config) (*gorm.DB, func(), error) {
	db, err := database.Open(&cfg.Database, ProvideDBModelList())
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	return db, cleanup, nil
}

// ProvideModules 汇总所有模块为 server.Modules。
// 追加新模块时在此加一行 wire.Struct 不能自动完成，显式列出保证可读。
func ProvideModules(
	cfg *config.Config,
	health *health.Handler,
	auth *auth.Handler,
	setup *setup.Handler,
	identity *identity.Handler,
	rbac *rbac.Handler,
	resources *resources.Handler,
	resSvc *resources.Service,
	notify *notify.Handler,
	projects *projects.Handler,
	ciMod *ci.Handler,
	releaseMod *release.Handler,
	canaryMod *canary.Handler,
	observH *observ.Handler,
	slotsMod *slots.Handler,
	cronH *cronmod.Handler,
	cronSvc *cronmod.Service,
	cronSched *cronmod.Scheduler, // 拉起 cron:sched 到点扫描任务（哨兵依赖）
	backupH *backup.Handler,
	configsH *configs.Handler,
	cfgSvc *configs.Service,
	certsH *certs.Handler,
	certsSvc *certs.Service,
	certsSched *certs.Scheduler, // 拉起 certs:sched 到期扫描（哨兵依赖）
	aiH *ai.Handler,
	aiAlert *ai.AlertAnalysisService,
	aiRelay *ai.Service,
	aiChatSvc *ai.ChatService,
	aiChatH *ai.ChatHandler,
	aiSkillSvc *ai.SkillService,
	mcpSvc *mcpmod.Service,
	mcpH *mcpmod.Handler,
	db *gorm.DB,
	cipher *crypto.Cipher,
	backupSvc *backup.Service,
	backupSched *backup.Scheduler, // 拉起 backup:sched 调度扫描（哨兵依赖）
	slotsSvc *slots.Service,
	ciSvc *ci.Service,
	ciPoller *ci.Poller, // 拉起 ci:poll 状态轮询任务（哨兵依赖）
	slotsSweeper *slots.Sweeper, // 拉起 slots:sweep 到期扫描任务（哨兵依赖）
	notifySvc *notify.Service,
	releaseSvc *release.Service,
	canarySvc *canary.Service,
	observSvc *observ.Service,
	casbinEnf *casbin.SyncedEnforcer,
) server.Modules {
	// 桥接：服务器不可达/恢复事件推运维群（第二阶段空壳的补全）
	resources.AttachNotifier(notifySvc)
	// 桥接：分支 push → 匹配占用该分支的测试槽位自动重建
	ciSvc.BranchPushHook = slotsSvc.OnBranchPush
	// 桥接：canary 渲染 conf 时取蓝绿活跃色（release→canary 单向依赖，
	// color getter 事后注入避免构造环）
	canarySvc.SetColorGetter(releaseSvc)
	// 桥接：cron 的 compose 载体任务跟随蓝绿活跃颜色域（同注入模式解构造环）
	cronSvc.SetDomainResolver(releaseSvc)
	// 桥接（P8-M3.2）：k3s 集群服务注入 release（k3s 目标分流；cipher 复用平台主密钥）
	k3sSvc := k3smod.NewService(db, cipher)
	releaseSvc.SetK3sDeployer(k3sSvc)
	// 桥接（P8-M3.3）：观测栈 O2 地址供给（observ settings → k3s DaemonSet 渲染）
	k3sSvc.SetObservURL(observSvc.O2URL)
	// 桥接：cron 任务失败 / observ 部署失败 / 备份失败推统一通知路由
	cronSvc.SetNotifier(notifySvc)
	observSvc.SetNotifier(notifySvc)
	backupSvc.SetNotifier(notifySvc)
	observSvc.SetPublicURL(cfg.IM.PublicURL)
	certsSvc.SetNotifier(notifySvc)
	// 桥接（P6 M1）：对话挂载上下文供给（项目/发布/构建/主机/事件只读查询）
	aiChatSvc.MountSource = &chatContextBridge{db: db}
	// 桥接（P6 M3）：/命令技能注入
	aiChatSvc.Skills = aiSkillSvc
	seedSkills(db)
	// 桥接（P6 M2/M3）：MCP tools 与对话内 function calling 同一套数据投影
	tools := &toolsBridge{db: db, res: resSvc}
	tools.SetSources(observSvc, ciSvc, cfgSvc) // P7-M2：告警分析五工具数据源
	// 桥接（P7-M2）：告警 AI 分析——notify 统一入口两阶段（warn/info 先析后发、
	// critical 先发后补），工具与对话 function calling 同一投影
	aiAlert.SetToolSource(tools)
	notifySvc.SetAlertAnalyzer(aiAlert)
	// 桥接（P7-M3）：编辑器 AI 助手（/ai/assist 无会话一次性；三场景 advisory，
	// 工具与对话/告警分析同一投影）
	assistSvc := ai.NewAssistService(aiRelay)
	assistSvc.SetToolSource(tools)
	// 桥接（P7-M3）：编辑器 AI 助手（/ai/assist 无会话一次性；三场景 advisory）
	aiChatSvc.ToolSource = tools
	mcpSvc.SetSources(tools, aiChatSvc.MountSource)
	// 桥接（P6 M4，建议卡定调）：NL→操作建议——白名单三件套只生成建议卡
	// （校验对象真实存在 + 跳转路由），AI 不执行任何变更
	aiChatSvc.ActionSource = &chatActionBridge{db: db, res: resSvc}
	return server.Modules{health, auth, home.NewHandler(db), setup, identity, rbac, resources, notify, projects, ciMod, releaseMod, canaryMod, slotsMod, cronH, observH, backupH, configsH, certsH, aiH, aiChatH, mcpH, ai.NewAssistHandler(assistSvc), k3smod.NewHandler(k3sSvc)}
}

// infraSet 基础设施：配置、JWT、数据库。
// 凭据加密器 *crypto.Cipher 由 auth.Set 提供（缺主密钥时为 nil，使用处报明确错误）。
var infraSet = wire.NewSet(
	config.Load,
	jwtpkg.NewManager,
	ProvideDB,
)

// moduleSet 业务模块（新模块在此追加）。
var moduleSet = wire.NewSet(
	health.Set,
	auth.Set,
	setup.Set,
	identity.Set,
	rbac.Set,
	resources.Set,
	notify.Set,
	backup.Set,
	configs.Set,
	certs.Set,
	ai.Set,
	projects.Set,
	ci.Set,
	release.Set,
	canary.Set,
	slots.Set,
	observ.Set,
	cronmod.Set,
	mcpmod.Set,
	// canary 的 SSHRunner 由 resources.Service 实现（灰度承载层复用 SSH 通道）
	wire.Bind(new(canary.SSHRunner), new(*resources.Service)),
	// cron 的 Runner（SSH 执行 + 事件审计）同样由 resources.Service 实现
	wire.Bind(new(cronmod.Runner), new(*resources.Service)),
	wire.Bind(new(backup.SSHExecutor), new(*resources.Service)),
	wire.Bind(new(configs.Executor), new(*resources.Service)),
	wire.Bind(new(certs.SSHExecutor), new(*resources.Service)),
	// release 的 deployer/confRenderer 接口化便于测试，实现仍是 resources/canary
	wire.Bind(new(release.Deployer), new(*resources.Service)),
	wire.Bind(new(release.ConfRenderer), new(*canary.Service)),
	// ci/release/canary/slots 通过只读投影取项目数据（替代跨模块直读表）
	wire.Bind(new(projects.Reader), new(*projects.Service)),
	ProvideModules,
)

// Set 完整组装集合。
var Set = wire.NewSet(
	infraSet,
	moduleSet,
	server.New,
	server.NewEngine,
)

// seedSkills 内置技能种子（P6 M3）：首次启动种入；管理员可在后台改删。
func seedSkills(db *gorm.DB) {
	var n int64
	db.Model(&ai.Skill{}).Count(&n)
	if n > 0 {
		return
	}
	seeds := []ai.Skill{
		{
			Name: "troubleshoot", Title: "故障排查", Enabled: true,
			Description: "按 runbook 结构化排查：先读上下文数据块，再定位、假设、验证",
			Prompt:      "用户报告了以下问题：{{q}}\n请按以下流程回答：1) 从上方平台数据块中提取与该问题相关的事实（构建/发布/事件/任务/配置变更）；2) 列出 2~3 个最可能的假设并按可能性排序；3) 给出每个假设的验证方法（可执行的查询或命令）；4) 明确指出数据块中缺失、需要用户补充的信息。不要臆造数据块之外的状态。",
			Runbook:     "## 通用排查顺序\n1. 最近 30 分钟的主机事件与 cron 失败\n2. 最近构建/发布是否失败、失败时间与问题出现时间的相关性\n3. 配置元信息近期是否有变更\n4. 以上都正常时考虑上游依赖（数据库/缓存/网络）",
		},
		{
			Name: "release-check", Title: "发布检查", Enabled: true,
			Description: "发布前体检：构建状态、环境隔离、部署目标、回滚路径",
			Prompt:      "用户即将发布或刚完成发布，诉求：{{q}}\n请基于平台数据块做发布检查：1) 目标项目最近的构建是否全部通过（列出失败项）；2) 部署目标主机与环境是否正常（单点/隔离风险）；3) 上一次发布的版本与状态（回滚基线）；4) 给出 GO / NO-GO 结论与理由。数据块之外的信息向用户询问，不要假设。",
			Runbook:     "## 发布检查单\n- 构建门禁：最近构建全绿\n- 部署目标：目标环境存在且主机可达\n- 回滚基线：上一成功版本已知\n- 观测就绪：O2 告警规则已覆盖该服务",
		},
	}
	for i := range seeds {
		db.Create(&seeds[i])
	}
}
