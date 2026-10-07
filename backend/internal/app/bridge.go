// bridge.go 跨模块桥接层（独立审核第 3 批 T9 拆分）：
// alertContextBridge（告警诊断上下文）/ chatContextBridge（对话挂载）/
// toolsBridge（AI 工具数据源）/ chatActionBridge（NL→操作确认层）。
// 从 app.go 整体搬移，逻辑不变——读模型下沉到独立文件后 app.go 只留装配。
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/ai"
	"github.com/custos-machina/backend/internal/modules/ci"
	"github.com/custos-machina/backend/internal/modules/configs"
	"github.com/custos-machina/backend/internal/modules/observ"
	"github.com/custos-machina/backend/internal/modules/resources"
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

	// 项目概况：挂载 = 指定项目详情；未挂载 = 全部项目简要清单（平台级问题
	// 如"有多少个项目"不要求用户先挂载——挂载是聚焦，不是数据开关）
	var pb strings.Builder
	if len(m.ProjectIDs) > 0 {
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
	} else {
		// 全平台（挂载概念已移除的默认形态）：项目数不多（单实例轻量），
		// 直接给详情级清单——微服务排查天然跨项目
		var total int64
		b.db.WithContext(ctx).Table("projects").Count(&total)
		prows, err := b.db.WithContext(ctx).Table("projects").
			Select("id, name, repo_path, provider, default_branch").
			Order("id").Limit(20).Rows()
		if err == nil {
			fmt.Fprintf(&pb, "平台共 %d 个项目：\n", total)
			for prows.Next() {
				var id uint
				var name, repo, provider, branch string
				_ = prows.Scan(&id, &name, &repo, &provider, &branch)
				fmt.Fprintf(&pb, "#%d %s（%s，%s，默认分支 %s）\n", id, name, repo, provider, branch)
			}
			_ = prows.Close()
		}
	}
	// 各项目环境部署目标（项目↔主机绑定，"哪个服务部署在哪台主机"的直接答案）
	{
		var db_ strings.Builder
		drows, err := b.db.WithContext(ctx).Table("project_env_targets t").
			Select("t.project_id, t.env_type, s.name, s.host").
			Joins("LEFT JOIN servers s ON s.id = t.server_id").
			Order("t.project_id, t.env_type").Limit(60).Rows()
		if err == nil {
			fmt.Fprintf(&db_, "各项目部署目标（项目×环境 → 主机）：\n")
			for drows.Next() {
				var pid uint
				var env, sname, shost string
				_ = drows.Scan(&pid, &env, &sname, &shost)
				fmt.Fprintf(&db_, "项目#%d %s → %s（%s）\n", pid, env, sname, shost)
			}
			_ = drows.Close()
		}
		if db_.Len() > 0 {
			blocks = append(blocks, ai.ContextBlock{Source: "deploy_targets", Text: db_.String()})
		}
	}

	if pb.Len() > 0 {
		blocks = append(blocks, ai.ContextBlock{Source: "project_overview", Text: pb.String()})
	}

	// 近期构建与发布：挂载 = 指定项目聚焦；未挂载 = 全平台最近摘要
	// （"各项目最近构建情况"是平台级问题，不要求用户先挂载）
	{
		var bb strings.Builder
		bq := b.db.WithContext(ctx).Table("builds").
			Select("project_id, tag, env_type, status, created_at").
			Order("id DESC").Limit(8)
		if len(m.ProjectIDs) > 0 {
			bq = bq.Where("project_id IN ?", m.ProjectIDs)
		}
		brows, err := bq.Rows()
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
			blocks = append(blocks, ai.ContextBlock{Source: "recent_builds", Text: bb.String()})
		}

		var rb strings.Builder
		rq := b.db.WithContext(ctx).Table("releases").
			Select("project_id, tag, env_type, status, created_at").
			Order("id DESC").Limit(8)
		if len(m.ProjectIDs) > 0 {
			rq = rq.Where("project_id IN ?", m.ProjectIDs)
		}
		rrows, err := rq.Rows()
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

	// 主机清单：挂载 = 指定主机；未挂载 = 全部主机简要（name/host 非敏感）
	var sb strings.Builder
	sq := b.db.WithContext(ctx).Table("servers").
		Select("id, name, host, status").Order("id").Limit(20)
	if len(m.ServerIDs) > 0 {
		sq = sq.Where("id IN ?", m.ServerIDs)
	}
	srows, err := sq.Rows()
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
	}

	// 近期主机事件（Sensitive：含操作与命令记录，仅 admin 可见；全平台最近）
	{
		var eb strings.Builder
		erows, err := b.db.WithContext(ctx).Table("server_events").
			Select("server_id, type, message, created_at").
			Where("created_at > ?", since).
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

// toolsBridge mcp.ToolsSource 的 app 层实现：六类只读投影。
// 容器查询经 resources.Service（docker over SSH）；其余直查同库业务表。
type toolsBridge struct {
	db   *gorm.DB
	res  *resources.Service
	obs  *observ.Service  // P7-M2：O2 日志/指标查询（search_o2_logs / query_o2_metrics）
	ci   *ci.Service      // P7-M2：构建日志（get_build_logs）
	cfgs *configs.Service // P7-M2：配置变更（get_config_changes）
}

// SetSources P7-M2：告警分析工具的数据源注入（wire 时序在 ProvideModules 里直接构造）。
func (b *toolsBridge) SetSources(obs *observ.Service, ciSvc *ci.Service, cfgs *configs.Service) {
	b.obs, b.ci, b.cfgs = obs, ciSvc, cfgs
}

func (b *toolsBridge) ListServers(ctx context.Context) []map[string]any {
	var rows []map[string]any
	b.db.WithContext(ctx).Table("servers").
		Select("id, name, host, status").Order("id").Limit(50).
		Scan(&rows)
	return rows
}

func (b *toolsBridge) ListProjects(ctx context.Context) []map[string]any {
	var rows []map[string]any
	b.db.WithContext(ctx).Table("projects").
		Select("id, name, repo_path, provider").Order("id").Limit(50).
		Scan(&rows)
	return rows
}

func (b *toolsBridge) ListBuilds(ctx context.Context, projectID uint, limit int) []map[string]any {
	q := b.db.WithContext(ctx).Table("builds").
		Select("id, project_id, tag, env_type, status, builder, created_at").
		Order("id DESC").Limit(limit)
	if projectID > 0 {
		q = q.Where("project_id = ?", projectID)
	}
	var rows []map[string]any
	q.Scan(&rows)
	return rows
}

func (b *toolsBridge) ListReleases(ctx context.Context, projectID uint, limit int) []map[string]any {
	q := b.db.WithContext(ctx).Table("releases").
		Select("id, project_id, tag, env_type, status, release_by, created_at").
		Order("id DESC").Limit(limit)
	if projectID > 0 {
		q = q.Where("project_id = ?", projectID)
	}
	var rows []map[string]any
	q.Scan(&rows)
	return rows
}

func (b *toolsBridge) ListCronRuns(ctx context.Context, limit int) []map[string]any {
	var rows []map[string]any
	b.db.WithContext(ctx).Table("cron_runs").
		Select("id, job_name, status, exit_code, created_at").
		Order("id DESC").Limit(limit).Scan(&rows)
	return rows
}

func (b *toolsBridge) ListCronJobs(ctx context.Context) []map[string]any {
	var rows []map[string]any
	b.db.WithContext(ctx).Table("cron_jobs").
		Select("id, name, schedule, enabled, last_status").
		Order("id").Limit(50).Scan(&rows)
	return rows
}

func (b *toolsBridge) ListConfigs(ctx context.Context) []map[string]any {
	var rows []map[string]any
	b.db.WithContext(ctx).Table("config_files").
		Select("id, name, server_id, rel_path, path, format, apply_action").
		Order("id").Limit(50).Scan(&rows)
	return rows
}

func (b *toolsBridge) ListContainers(ctx context.Context, serverID uint) ([]map[string]any, error) {
	// resources.Service 的容器列表（docker over SSH）
	out, err := b.res.ContainersBrief(ctx, serverID)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ChatTools 对话内工具（P6-M3 function calling）：与 MCP tools 同源同实现，
// 六类只读投影 + JSON Schema 出参。角色语义同 MCP 侧（list 系两角色一致，
// 敏感过滤在数据源头——投影列本就不含敏感字段）。
func (b *toolsBridge) ChatTools(ctx context.Context, viewerRoles []string) []ai.ChatTool {
	intProp := func(desc string) map[string]any {
		return map[string]any{"type": "integer", "description": desc}
	}
	schema := func(props map[string]any) map[string]any {
		return map[string]any{"type": "object", "properties": props}
	}
	listSchema := func() map[string]any {
		return schema(map[string]any{
			"project_id": intProp("可选，按项目 ID 过滤（0 = 不过滤）"),
			"server_id":  intProp("可选，主机 ID"),
			"limit":      intProp("返回条数上限，默认 10，最大 50"),
		})
	}
	rowsToText := func(rows []map[string]any) string {
		if len(rows) == 0 {
			return "（无记录）"
		}
		var sb strings.Builder
		for _, r := range rows {
			fmt.Fprintf(&sb, "%v\n", r)
		}
		return sb.String()
	}
	lim := func(n any) int {
		v, _ := n.(float64)
		i := int(v)
		if i <= 0 {
			return 10
		}
		if i > 50 {
			return 50
		}
		return i
	}
	argUint := func(args map[string]any, key string) uint {
		v, _ := args[key].(float64)
		if v <= 0 {
			return 0
		}
		return uint(v)
	}

	return []ai.ChatTool{
		{
			Name:        "list_servers",
			Description: "列出平台管理的主机（名称/地址/状态）",
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				return rowsToText(b.ListServers(ctx)), nil
			},
		},
		{
			Name:        "list_projects",
			Description: "列出平台项目（名称/仓库/git 托管）",
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				return rowsToText(b.ListProjects(ctx)), nil
			},
		},
		{
			Name:        "list_builds",
			Description: "近期 CI 构建记录（可按项目过滤）",
			Parameters:  listSchema(),
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				return rowsToText(b.ListBuilds(ctx, argUint(args, "project_id"), lim(args["limit"]))), nil
			},
		},
		{
			Name:        "list_releases",
			Description: "近期发布记录（可按项目过滤）",
			Parameters:  listSchema(),
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				return rowsToText(b.ListReleases(ctx, argUint(args, "project_id"), lim(args["limit"]))), nil
			},
		},
		{
			Name:        "list_cron_runs",
			Description: "近期定时任务执行记录（含失败摘要）",
			Parameters:  listSchema(),
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				return rowsToText(b.ListCronRuns(ctx, lim(args["limit"]))), nil
			},
		},
		{
			Name:        "list_cron_jobs",
			Description: "列出定时任务定义（ID/名称/调度表达式/启用状态/最近结果）——手动触发前用它查 job_id",
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				return rowsToText(b.ListCronJobs(ctx)), nil
			},
		},
		{
			Name:        "list_configs",
			Description: "列出配置文件（ID/名称/目标主机/路径/格式/生效动作）——下发前用它查 file_id",
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				return rowsToText(b.ListConfigs(ctx)), nil
			},
		},
		{
			Name:        "list_containers",
			Description: "列出指定主机上的容器（名称/镜像/状态）",
			Parameters: schema(map[string]any{
				"server_id": intProp("必填，主机 ID"),
			}),
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				sid := argUint(args, "server_id")
				if sid == 0 {
					return "", errors.New("server_id 必填")
				}
				rows, err := b.ListContainers(ctx, sid)
				if err != nil {
					return "", err
				}
				return rowsToText(rows), nil
			},
		},
		{
			Name:        "container_logs",
			Description: "查看指定主机上某容器最近的日志尾部（只读，排障用——用户报错时先看日志）",
			Parameters: schema(map[string]any{
				"server_id": intProp("必填，主机 ID"),
				"container": map[string]any{"type": "string", "description": "必填，容器名或 ID 前缀（list_containers 可查）"},
				"lines":     intProp("可选，尾部行数，默认 100，最大 500"),
			}),
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				sid := argUint(args, "server_id")
				cname := actionArgStr(args, "container")
				if sid == 0 || cname == "" {
					return "", errors.New("server_id 与 container 均必填")
				}
				cons, err := b.ListContainers(ctx, sid)
				if err != nil {
					return "", err
				}
				cid := ""
				for _, c := range cons {
					name, _ := c["name"].(string)
					id, _ := c["id"].(string)
					if name == cname || strings.HasPrefix(id, cname) {
						cid = id
						break
					}
				}
				if cid == "" {
					return "", fmt.Errorf("主机上不存在容器 %q", cname)
				}
				lines := lim(args["lines"])
				if lines > 500 {
					lines = 500
				}
				out, err := b.res.ContainerLogsTail(ctx, sid, cid, lines)
				if err != nil {
					return "", err
				}
				if out == "" {
					return "（无日志输出）", nil
				}
				return out, nil
			},
		},
		// ---- P7-M2 告警 AI 分析五工具（只读；对话与告警分析共用一套投影） ----
		{
			Name:        "search_o2_logs",
			Description: "查 O2 日志（按 SQL 条件过滤最近时间窗，排障首选）",
			Parameters: schema(map[string]any{
				"query":   map[string]any{"type": "string", "description": "可选，SQL WHERE 片段（如 message ILIKE '%error%'）；空=最近日志"},
				"stream":  map[string]any{"type": "string", "description": "可选，日志流名（默认全流）"},
				"minutes": intProp("可选，时间窗分钟数，默认 60，最大 1440"),
				"limit":   intProp("可选，返回条数上限，默认 30，最大 100"),
			}),
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				return b.obs.O2SearchLogs(ctx,
					actionArgStr(args, "query"), actionArgStr(args, "stream"),
					int(limNum(args["minutes"], 60)), int(limNum(args["limit"], 30)))
			},
		},
		{
			Name:        "query_o2_metrics",
			Description: "查 O2 指标（PromQL 范围查询，判断 CPU/内存/网络是否同时异常）",
			Parameters: schema(map[string]any{
				"promql":  map[string]any{"type": "string", "description": "必填，PromQL 表达式"},
				"minutes": intProp("可选，范围窗口分钟数，默认 30"),
			}),
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				q := actionArgStr(args, "promql")
				if q == "" {
					return "", errors.New("promql 必填")
				}
				return b.obs.O2QueryMetrics(ctx, q, int(limNum(args["minutes"], 30)))
			},
		},
		{
			Name:        "get_build_logs",
			Description: "取构建日志（优先平台留存的尾部日志；构建失败排错用）",
			Parameters:  listSchema(),
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				return b.ci.RecentBuildLogsText(ctx, argUint(args, "project_id"), int(lim(args["limit"])))
			},
		},
		{
			Name:        "get_git_commits",
			Description: "查仓库最近提交（谁在什么时候改了什么——定位引入问题的 commit）",
			Parameters: schema(map[string]any{
				"repo_path":   map[string]any{"type": "string", "description": "必填，owner/repo（list_projects 可查）"},
				"since_hours": intProp("可选，只取最近 N 小时，默认 24"),
				"limit":       intProp("可选，条数上限，默认 20，最大 50"),
			}),
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				rp := actionArgStr(args, "repo_path")
				if rp == "" {
					return "", errors.New("repo_path 必填")
				}
				hours := limNum(args["since_hours"], 24)
				return b.ci.GitCommitsText(ctx, rp, int(hours), int(limNum(args["limit"], 20)))
			},
		},
		{
			Name:        "get_config_changes",
			Description: "查最近配置变更记录（文件/环境/操作人/时间——配置改动引发告警时对时间线）",
			Parameters: schema(map[string]any{
				"limit": intProp("可选，条数上限，默认 15，最大 50"),
			}),
			Fn: func(ctx context.Context, args map[string]any) (string, error) {
				return b.cfgs.RecentChanges(ctx, int(limNum(args["limit"], 15)))
			},
		},
	}
}

// limNum 数值参数取值（缺省/越界回落 def；P7-M2 工具参数用）。
func limNum(v any, def int) int64 {
	f, _ := v.(float64)
	n := int64(f)
	if n <= 0 {
		return int64(def)
	}
	return n
}

// seedSkills 内置技能种子（P6 M3）：首次启动种入；管理员可在后台改删。

// ---- P6-M4：NL→操作确认层执行器 ----

// chatActionBridge 白名单三件套执行器：Validate 拒绝幻觉 ID（对象必须真实
// 存在，校验时顺带解析容器名→cid、回填展示字段）；Execute 先按 REST 同款
// casbin 资源点判权（dev 触发 admin 级操作被拒——与中间件同语义：admin 直放，
// 其余角色走种子矩阵），再经既有 service 执行并审计（标注"AI 对话发起"）。
type chatActionBridge struct {
	db  *gorm.DB
	res *resources.Service
}

func actionArgUint(args map[string]any, key string) uint {
	v, _ := args[key].(float64)
	if v <= 0 {
		return 0
	}
	return uint(v)
}

func actionArgStr(args map[string]any, key string) string {
	s, _ := args[key].(string)
	return strings.TrimSpace(s)
}

func (b *chatActionBridge) ChatActions() []ai.ChatActionDef {
	// 注意：required 是 schema 对象级数组（放属性内会被上游 400 拒绝并触发
	// 整体去工具降级——DeepSeek 实测踩过）
	objSchema := func(props map[string]any, required ...string) map[string]any {
		s := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			s["required"] = required
		}
		return s
	}
	return []ai.ChatActionDef{
		{
			Type:        ai.ActRestartContainer,
			Description: "为用户生成「重启容器」的操作建议卡（你不执行任何变更，卡上「去处理」引导用户到平台页面自行操作）。当用户表达重启容器意图时调用；先用 list_servers/list_containers 查到真实 server_id 与容器名",
			Parameters: objSchema(map[string]any{
				"server_id": map[string]any{"type": "integer", "description": "主机 ID（list_servers 可查）"},
				"container": map[string]any{"type": "string", "description": "容器名或 ID 前缀（list_containers 可查）"},
			}, "server_id", "container"),
			Summary: func(p map[string]any) string {
				return fmt.Sprintf("建议重启主机「%v」上的容器「%v」", p["_server_name"], p["container"])
			},
			Route: func(_ map[string]any) string { return "/resources/servers" },
		},
		{
			Type:        ai.ActTriggerCron,
			Description: "为用户生成「手动触发定时任务」的操作建议卡（你不执行任何变更，卡上「去处理」引导用户到平台页面自行操作）。当用户表达触发任务意图时调用；先用 list_cron_jobs 查到真实 job_id",
			Parameters: objSchema(map[string]any{
				"job_id": map[string]any{"type": "integer", "description": "定时任务 ID（list_cron_jobs 可查）"},
			}, "job_id"),
			Summary: func(p map[string]any) string {
				return fmt.Sprintf("建议手动触发定时任务「%v」（#%v）", p["_job_name"], p["job_id"])
			},
			Route: func(_ map[string]any) string { return "/cron" },
		},
		{
			Type:        ai.ActDeployConfig,
			Description: "为用户生成「下发配置文件」的操作建议卡（你不执行任何变更，卡上「去处理」引导用户到平台页面自行操作）。当用户表达配置下发意图时调用；先用 list_configs 查到真实 file_id",
			Parameters: objSchema(map[string]any{
				"file_id": map[string]any{"type": "integer", "description": "配置文件 ID（list_configs 可查）"},
			}, "file_id"),
			Summary: func(p map[string]any) string {
				return fmt.Sprintf("建议下发配置「%v」到 %v", p["_file_name"], p["_path"])
			},
			Route: func(_ map[string]any) string { return "/configs" },
		},
	}
}

// ValidateChatAction 参数校验：对象必须真实存在（拒绝幻觉 ID），顺带回填
// 解析结果（_cid/_server_name/_job_name/_file_name/_path）供确认卡与执行使用。
func (b *chatActionBridge) ValidateChatAction(ctx context.Context, typ string, params map[string]any) (string, error) {
	for _, def := range b.ChatActions() {
		if def.Type != typ {
			continue
		}
		switch typ {
		case ai.ActRestartContainer:
			sid := actionArgUint(params, "server_id")
			cname := actionArgStr(params, "container")
			if sid == 0 || cname == "" {
				return "", fmt.Errorf("server_id 与 container 均必填")
			}
			var srvName string
			if err := b.db.WithContext(ctx).Table("servers").Select("name").
				Where("id = ?", sid).Scan(&srvName).Error; err != nil || srvName == "" {
				return "", fmt.Errorf("主机 #%d 不存在", sid)
			}
			cons, err := b.res.ContainersBrief(ctx, sid)
			if err != nil {
				return "", fmt.Errorf("查询主机容器失败: %v", err)
			}
			var cid string
			for _, c := range cons {
				name, _ := c["name"].(string)
				id, _ := c["id"].(string)
				if name == cname || strings.HasPrefix(id, cname) {
					cid = id
					break
				}
			}
			if cid == "" {
				return "", fmt.Errorf("主机「%s」上不存在容器 %q", srvName, cname)
			}
			params["_cid"] = cid
			params["_server_name"] = srvName
		case ai.ActTriggerCron:
			jid := actionArgUint(params, "job_id")
			if jid == 0 {
				return "", fmt.Errorf("job_id 必填")
			}
			var name string
			if err := b.db.WithContext(ctx).Table("cron_jobs").Select("name").
				Where("id = ?", jid).Scan(&name).Error; err != nil || name == "" {
				return "", fmt.Errorf("定时任务 #%d 不存在", jid)
			}
			params["_job_name"] = name
		case ai.ActDeployConfig:
			fid := actionArgUint(params, "file_id")
			if fid == 0 {
				return "", fmt.Errorf("file_id 必填")
			}
			var row struct{ Name, Path string }
			if err := b.db.WithContext(ctx).Table("config_files").Select("name, path").
				Where("id = ?", fid).Scan(&row).Error; err != nil || row.Name == "" {
				return "", fmt.Errorf("配置文件 #%d 不存在", fid)
			}
			params["_file_name"], params["_path"] = row.Name, row.Path
		}
		// 校验通过后再渲染摘要（此时回填字段已就绪）
		for _, def := range b.ChatActions() {
			if def.Type == typ {
				return def.Summary(params), nil
			}
		}
	}
	return "", fmt.Errorf("未知操作类型 %q", typ)
}
