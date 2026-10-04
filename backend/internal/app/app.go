// Package app 是组装根（composition root）：聚合所有模块的 wire ProviderSet，
// 并提供跨模块的基础对象（数据库、模块列表）。新增模块只需：实现 server.Module、
// 导出 Set、在下面追加一行 —— 其余无需改动。
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/modules/notify"
	"github.com/custos-machina/backend/internal/modules/observ"
	"github.com/custos-machina/backend/internal/modules/projects"
	"github.com/custos-machina/backend/internal/modules/rbac"
	"github.com/custos-machina/backend/internal/modules/release"
	"github.com/custos-machina/backend/internal/modules/resources"
	"github.com/custos-machina/backend/internal/modules/setup"
	"github.com/custos-machina/backend/internal/modules/slots"
	"github.com/custos-machina/backend/internal/pkg/database"
	jwtpkg "github.com/custos-machina/backend/internal/pkg/jwt"
	"github.com/custos-machina/backend/internal/server"
)

// ProvideDB 打开数据库并迁移全部模块的模型（模型清单随模块在此登记）。
func ProvideDB(cfg *config.Config) (*gorm.DB, func(), error) {
	models := identity.Models()
	models = append(models, resources.Models()...)
	models = append(models, notify.Models()...)
	models = append(models, backup.Models()...)
	models = append(models, observ.Models()...)
	models = append(models, configs.Models()...)
	models = append(models, certs.Models()...)
	models = append(models, ai.Models()...)
	models = append(models, ai.ChatModels()...)
	models = append(models, projects.Models()...)
	models = append(models, ci.Models()...)
	models = append(models, release.Models()...)
	models = append(models, canary.Models()...)
	models = append(models, slots.Models()...)
	models = append(models, cronmod.Models()...)
	db, err := database.Open(&cfg.Database, models)
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
	certsH *certs.Handler,
	certsSvc *certs.Service,
	certsSched *certs.Scheduler, // 拉起 certs:sched 到期扫描（哨兵依赖）
	aiH *ai.Handler,
	aiDigest *ai.DigestService,
	aiChatSvc *ai.ChatService,
	aiChatH *ai.ChatHandler,
	db *gorm.DB,
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
	// 桥接：cron 任务失败 / observ 部署失败 / 备份失败推统一通知路由
	cronSvc.SetNotifier(notifySvc)
	observSvc.SetNotifier(notifySvc)
	backupSvc.SetNotifier(notifySvc)
	observSvc.SetPublicURL(cfg.IM.PublicURL)
	certsSvc.SetNotifier(notifySvc)
	// 桥接（P5 M6）：O2 告警 → AI 诊断摘要；ai 的上下文供给由 app 层桥实现
	aiDigest.SetNotifier(notifySvc)
	aiDigest.SetContextSource(&alertContextBridge{db: db})
	observSvc.SetDigestor(aiDigest)
	// 桥接（P6 M1）：对话挂载上下文供给（项目/发布/构建/主机/事件只读查询）
	aiChatSvc.MountSource = &chatContextBridge{db: db}
	return server.Modules{health, auth, setup, identity, rbac, resources, notify, projects, ciMod, releaseMod, canaryMod, slotsMod, cronH, observH, backupH, configsH, certsH, aiH, aiChatH}
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

// alertContextBridge ai.ContextSource 的 app 层实现：
// 告警关联上下文只读查询（server_events / cron_runs / config_files 快照摘要）。
type alertContextBridge struct {
	db *gorm.DB
}

func (b *alertContextBridge) AlertDigestContext(ctx context.Context, _ string, _ []string) (string, string, string) {
	var ev strings.Builder
	rows, err := b.db.WithContext(ctx).Table("server_events").
		Select("server_id, type, message, created_at").
		Where("created_at > ?", time.Now().Add(-24*time.Hour)).
		Order("id DESC").Limit(10).Rows()
	if err == nil {
		for rows.Next() {
			var sid uint
			var typ, msg string
			var ts time.Time
			_ = rows.Scan(&sid, &typ, &msg, &ts)
			fmt.Fprintf(&ev, "server=%d %s %s %s\n", sid, typ, msg, ts.Format(time.DateTime))
		}
		_ = rows.Close()
	}
	var cronB strings.Builder
	if b.db != nil {
		crows, err := b.db.WithContext(ctx).Table("cron_runs").
			Select("job_name, status, created_at").
			Where("status IN ? AND created_at > ?", []string{"failed", "timeout"}, time.Now().Add(-24*time.Hour)).
			Order("id DESC").Limit(8).Rows()
		if err == nil {
			for crows.Next() {
				var name, status string
				var ts time.Time
				_ = crows.Scan(&name, &status, &ts)
				fmt.Fprintf(&cronB, "%s %s %s\n", name, status, ts.Format(time.DateTime))
			}
			_ = crows.Close()
		}
	}
	// 配置快照：文件名与格式（不含内容——DLP 最小暴露面）
	var cfgB strings.Builder
	c2rows, err := b.db.WithContext(ctx).Table("config_files").
		Select("name, path, format, updated_at").Order("updated_at DESC").Limit(8).Rows()
	if err == nil {
		for c2rows.Next() {
			var name, path, format string
			var ts time.Time
			_ = c2rows.Scan(&name, &path, &format, &ts)
			fmt.Fprintf(&cfgB, "%s（%s，%s，更新于 %s）\n", name, path, format, ts.Format(time.DateTime))
		}
		_ = c2rows.Close()
	}
	return ev.String(), cronB.String(), cfgB.String()
}

// chatContextBridge ai.ChatContextSource 的 app 层实现：对话挂载上下文只读查询。
// 块的 Sensitive 标记决定角色过滤（BuildContextPack）：配置元信息与主机事件
// 仅 admin 可见——dev 的 pack 不含这些（AI 铁律：对话不成为只读超权）。
type chatContextBridge struct {
	db *gorm.DB
}

func (b *chatContextBridge) MountContext(ctx context.Context, m ai.Mount, _ []string) []ai.ContextBlock {
	since := time.Now().Add(-time.Duration(m.Hours) * time.Hour)
	var blocks []ai.ContextBlock

	if len(m.ProjectIDs) > 0 {
		var pb strings.Builder
		prows, err := b.db.WithContext(ctx).Table("projects").
			Select("id, name, repo_path, provider, default_branch").
			Where("id IN ?", m.ProjectIDs).Rows()
		if err == nil {
			for prows.Next() {
				var id uint
				var name, repo, provider, branch string
				_ = prows.Scan(&id, &name, &repo, &provider, &branch)
				fmt.Fprintf(&pb, "#%d %s（%s，%s，默认分支 %s）\n", id, name, repo, provider, branch)
			}
			_ = prows.Close()
		}
		if pb.Len() > 0 {
			blocks = append(blocks, ai.ContextBlock{Source: "project_overview", Text: pb.String()})

			// 近期失败构建（挂载项目范围）
			var bb strings.Builder
			brows, err := b.db.WithContext(ctx).Table("builds").
				Select("project_id, tag, env_type, status, created_at").
				Where("project_id IN ? AND status = ? AND created_at > ?", m.ProjectIDs, "failed", since).
				Order("id DESC").Limit(8).Rows()
			if err == nil {
				for brows.Next() {
					var pid uint
					var tag, env, status string
					var ts time.Time
					_ = brows.Scan(&pid, &tag, &env, &status, &ts)
					fmt.Fprintf(&bb, "项目#%d %s %s %s %s\n", pid, tag, env, status, ts.Format(time.DateTime))
				}
				_ = brows.Close()
			}
			if bb.Len() > 0 {
				blocks = append(blocks, ai.ContextBlock{Source: "recent_build_failures", Text: bb.String()})
			}

			// 近期发布（挂载项目范围）
			var rb strings.Builder
			rrows, err := b.db.WithContext(ctx).Table("releases").
				Select("project_id, tag, env_type, status, created_at").
				Where("project_id IN ? AND created_at > ?", m.ProjectIDs, since).
				Order("id DESC").Limit(8).Rows()
			if err == nil {
				for rrows.Next() {
					var pid uint
					var tag, env, status string
					var ts time.Time
					_ = rrows.Scan(&pid, &tag, &env, &status, &ts)
					fmt.Fprintf(&rb, "项目#%d %s %s %s %s\n", pid, tag, env, status, ts.Format(time.DateTime))
				}
				_ = rrows.Close()
			}
			if rb.Len() > 0 {
				blocks = append(blocks, ai.ContextBlock{Source: "recent_releases", Text: rb.String()})
			}
		}
	}

	if len(m.ServerIDs) > 0 {
		var sb strings.Builder
		srows, err := b.db.WithContext(ctx).Table("servers").
			Select("id, name, host, status").
			Where("id IN ?", m.ServerIDs).Rows()
		if err == nil {
			for srows.Next() {
				var id uint
				var name, host string
				var status any
				_ = srows.Scan(&id, &name, &host, &status)
				fmt.Fprintf(&sb, "#%d %s（%s）\n", id, name, host)
			}
			_ = srows.Close()
		}
		if sb.Len() > 0 {
			blocks = append(blocks, ai.ContextBlock{Source: "server_inventory", Text: sb.String()})

			// 挂载主机的近期事件（Sensitive：含操作与命令记录）
			var eb strings.Builder
			erows, err := b.db.WithContext(ctx).Table("server_events").
				Select("server_id, type, message, created_at").
				Where("server_id IN ? AND created_at > ?", m.ServerIDs, since).
				Order("id DESC").Limit(12).Rows()
			if err == nil {
				for erows.Next() {
					var sid uint
					var typ, msg string
					var ts time.Time
					_ = erows.Scan(&sid, &typ, &msg, &ts)
					fmt.Fprintf(&eb, "server#%d %s %s %s\n", sid, typ, msg, ts.Format(time.DateTime))
				}
				_ = erows.Close()
			}
			if eb.Len() > 0 {
				blocks = append(blocks, ai.ContextBlock{Source: "server_events_window", Text: eb.String(), Sensitive: true})
			}
		}
	}

	// 近期 cron 失败（全平台，非敏感摘要）
	var cb strings.Builder
	crows, err := b.db.WithContext(ctx).Table("cron_runs").
		Select("job_name, status, created_at").
		Where("status IN ? AND created_at > ?", []string{"failed", "timeout"}, since).
		Order("id DESC").Limit(8).Rows()
	if err == nil {
		for crows.Next() {
			var name, status string
			var ts time.Time
			_ = crows.Scan(&name, &status, &ts)
			fmt.Fprintf(&cb, "%s %s %s\n", name, status, ts.Format(time.DateTime))
		}
		_ = crows.Close()
	}
	if cb.Len() > 0 {
		blocks = append(blocks, ai.ContextBlock{Source: "cron_failures_recent", Text: cb.String()})
	}

	// 配置文件元信息（Sensitive：路径暴露部署拓扑）
	var mb strings.Builder
	mrows, err := b.db.WithContext(ctx).Table("config_files").
		Select("name, path, format, updated_at").Order("updated_at DESC").Limit(8).Rows()
	if err == nil {
		for mrows.Next() {
			var name, path, format string
			var ts time.Time
			_ = mrows.Scan(&name, &path, &format, &ts)
			fmt.Fprintf(&mb, "%s（%s，%s，更新于 %s）\n", name, path, format, ts.Format(time.DateTime))
		}
		_ = mrows.Close()
	}
	if mb.Len() > 0 {
		blocks = append(blocks, ai.ContextBlock{Source: "config_meta", Text: mb.String(), Sensitive: true})
	}
	return blocks
}
