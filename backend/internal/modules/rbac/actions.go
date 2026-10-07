// actions.go P7-M1 业务动作目录：动作 Key 是权限语义的最小单位，handler 层
// gate 消费（细粒度），Routes 是自定义角色据此生成的 casbin 策略（粗粒度可达）。
// 结构性边界（D3）：管理面能力（用户/角色/系统设置）不入目录——自定义角色
// 无论怎么组合都触不到 admin 专属路由。
package rbac

// ActionDef 动作定义。Routes 仅对自定义角色生效（内置角色的 casbin 策略不因此改动）。
type ActionDef struct {
	Key      string   `json:"key"`
	Category string   `json:"category"`
	Desc     string   `json:"desc"`
	Routes   []Policy `json:"-"`
}

// ActionCatalog 动作目录（顺序即前端展示顺序）。
var ActionCatalog = []ActionDef{
	{Key: "config.view", Category: "配置", Desc: "配置文件只读（列表/内容/版本）", Routes: []Policy{
		{Path: "/config-files", Act: "GET"},
		{Path: "/config-files/*", Act: "GET"},
		{Path: "/config-kv", Act: "GET"},
		{Path: "/config-kv/*", Act: "GET"},
	}},
	{Key: "config.edit", Category: "配置", Desc: "编辑配置文件、版本回退", Routes: []Policy{
		{Path: "/config-files", Act: "POST|PUT|DELETE"},
		{Path: "/config-files/*", Act: "PUT|POST|DELETE"},
	}},
	{Key: "config.deploy", Category: "配置", Desc: "下发配置到目标机 / env 同步", Routes: []Policy{
		// deploy 与 rollback/env-sync 同为 POST /config-files/*，路由层不可分——
		// 由 handler gate 按动作分派（deploy/env-sync=deploy，版本回退=edit）
		{Path: "/config-files/*", Act: "POST"},
	}},
	{Key: "config.reveal", Category: "密钥", Desc: "敏感文件明文查看（独立于编辑权限）", Routes: nil},
	{Key: "release.publish.test", Category: "发布", Desc: "发布到 test 环境", Routes: publishRoutes},
	{Key: "release.publish.canary", Category: "发布", Desc: "灰度发布", Routes: publishRoutes},
	{Key: "release.publish.prod", Category: "发布", Desc: "正式环境发布（高危）", Routes: publishRoutes},
	{Key: "release.rollback", Category: "发布", Desc: "回滚发布", Routes: []Policy{
		{Path: "/releases/*", Act: "POST"},
	}},
	{Key: "terminal.access", Category: "终端", Desc: "Web 终端（按主机 ACL 继续叠加生效）", Routes: []Policy{
		{Path: "/servers/*/terminal", Act: "GET"},
		{Path: "/auth/tickets", Act: "POST"},
	}},
	{Key: "cron.trigger", Category: "任务", Desc: "手动执行定时任务", Routes: []Policy{
		{Path: "/cron-jobs/*", Act: "POST"},
	}},
}

var publishRoutes = []Policy{
	{Path: "/releases", Act: "POST"},
	{Path: "/releases/*", Act: "POST"},
}

// customBaseRoutes 自定义角色自动附加的基础读集：保证页面可渲染的只读面。
// 未授予的写操作仍被 casbin 拒（菜单可见但 API 403 是 M1 已知边界，见设计文档 §六）。
var customBaseRoutes = []Policy{
	{Path: "/home/summary", Act: "GET"},
	{Path: "/home/readiness", Act: "GET"},
	{Path: "/projects", Act: "GET"},
	{Path: "/projects/*", Act: "GET"},
	{Path: "/config-files", Act: "GET"},
	{Path: "/config-files/*", Act: "GET"},
	{Path: "/servers", Act: "GET"},
	{Path: "/servers/*", Act: "GET"},
	{Path: "/server-groups", Act: "GET"},
	{Path: "/server-container-stats", Act: "GET"},
	{Path: "/server-container-stats/*", Act: "GET"},
	{Path: "/server-events/*", Act: "GET"},
	{Path: "/server-env", Act: "GET"},
	{Path: "/server-env/*", Act: "GET"},
	{Path: "/server-env-guide", Act: "GET"},
	{Path: "/server-files", Act: "GET"},
	{Path: "/server-files/*", Act: "GET"},
	{Path: "/server-metrics", Act: "GET"},
	{Path: "/server-metrics/*", Act: "GET"},
	{Path: "/server-containers", Act: "GET"},
	{Path: "/server-containers/*", Act: "GET"},
	{Path: "/builds", Act: "GET"},
	{Path: "/releases", Act: "GET"},
	{Path: "/project-branches", Act: "GET"},
	{Path: "/project-branches/*", Act: "GET"},
	{Path: "/cron-scripts", Act: "GET"},
	{Path: "/cron-jobs", Act: "GET"},
	{Path: "/cron-runs", Act: "GET"},
	{Path: "/observ", Act: "GET"},
	{Path: "/observ/*", Act: "GET"},
	{Path: "/ai/chat", Act: "GET|POST"},
	{Path: "/ai/chat/*", Act: "GET|POST|PUT|DELETE"},
	{Path: "/ai/skills", Act: "GET"},
	{Path: "/slots", Act: "GET|POST"},
	{Path: "/slots/*", Act: "GET|POST"},
}

// builtinDefaultActions 内置角色默认动作集（与现状行为对齐；可在角色管理页调整）。
// 收紧点：dev 不含 config.reveal——现状 dev 凭 GET 路由即可 reveal，属权限漏洞（D2）。
var builtinDefaultActions = map[string][]string{
	"admin": {"config.view", "config.edit", "config.deploy", "config.reveal",
		"release.publish.test", "release.publish.canary", "release.publish.prod", "release.rollback",
		"terminal.access", "cron.trigger"},
	"ops":   {"config.view", "config.edit", "config.deploy", "config.reveal"},
	"dev":   {"config.view"},
	"guest": {},
}

// actionIndex Key → 定义（未知动作 gate 一律拒绝）。
var actionIndex = func() map[string]ActionDef {
	m := make(map[string]ActionDef, len(ActionCatalog))
	for _, a := range ActionCatalog {
		m[a.Key] = a
	}
	return m
}()

// AllActionKeys 目录全集（D3 授予面上限）。
func AllActionKeys() []string {
	keys := make([]string, 0, len(ActionCatalog))
	for _, a := range ActionCatalog {
		keys = append(keys, a.Key)
	}
	return keys
}

// validActionKey 动作 Key 是否在目录内。
func validActionKey(key string) bool { _, ok := actionIndex[key]; return ok }
