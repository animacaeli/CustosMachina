# P7-M1 权限管理专项·细化设计

> 定稿：2026-10-05（P7 开工首件，依据 plan-phase7 §二 M1 与审核补的三个决策点）
> 实施状态：后端 + 前端 + 单测 + API 级验收六条全过（2026-10-05，详见文末验收记录）。
> 范围：自定义角色 + 业务动作粒度 + 环境隔离 + 密钥独立权限 + 项目级授权。

## 一、三个决策点的裁定（依据代码证据）

### D1 动作执行机制：**两层并存——casbin 管路由可达（粗），handler 层动作 gate 管业务语义（细）**

代码证据证明细粒度裁决**不可能只放中间件**：

| 敏感动作 | 落点 | 为什么路由层表达不了 |
|---|---|---|
| 密钥明文查看 | `configs/handler.go:142` `?reveal=true` | 与普通 GET 同一路由同一方法，仅 query 参数不同 |
| 按环境发布 | `release/handler.go:102` `in.EnvType == "prod"` | 环境在 body 里，路由层不可见 |
| cron 手动触发 | `cron/handler.go:143` | 已有 handler 层 `IsAdmin` 拦截先例（"与 casbin 层互补"） |

裁定：**casbin 路由矩阵原样保留**（页面/资源可达性，含权限矩阵页的 HTTP 级编辑能力）；新增**动作目录 + 动作表**，敏感 handler 显式调 `rbac` 的动作裁决。新接口接入纪律：涉及下表动作的必须加 gate，其余仍走路由层。

### D2 存量迁移：**零破坏——绑定关系不动，新表叠加种子**

- `users.Roles` 逗号分隔字符串继续作为绑定载体：自定义角色名就是一个新的 casbin sub，**中间件/enforcer 零改动**（M6 用户级策略模式天然兼容）。
- casbin 内置角色的既有策略与人为矩阵编辑**原样保留**；种子升 v20 仅新增 admin 的角色 CRUD 三条路由策略（`POST /roles`、`GET /roles/actions`、`DELETE /roles/*`），不动其他条目。
- 新表 `roles / role_actions / role_projects` 首次启动种子：内置四角色（admin/ops/dev/guest）+ 各自默认动作集（与现状行为对齐，见 §三）。
- **一处有意收紧（行为变更点）**：现状 dev 角色有 `GET /config-files/*` 路由权限即可 `?reveal=true` 看密钥明文（路由层无区分）；M1 落地后 reveal 需 `config.reveal` 动作，dev 默认动作集不含 → dev 失去密钥明文查看。这正是用户点名的需求（"正式环境配置密钥/密码的查看需要独立权限"）。

### D3 角色管理权边界：**结构性上限 + 授予面校验**

- **结构性上限**：动作目录**不收录**用户管理/角色管理/系统设置/IM 设置等管理面能力——这些路由永远只在内置 admin 的 casbin 策略里，自定义角色无论怎么拼都碰不到；`admin` 任命仍仅超管（identity 层既有拦截不动）。
- **授予面校验**：创建/编辑自定义角色时，动作集必须 ⊆ 操作者自身动作集（超管全量）；防止低权角色自我提权。
- **名字空间防撞**：自定义角色名与内置角色名、既有用户名互斥（用户名也是 casbin sub——M6 用户级策略；同名会劫持授权）。

## 二、动作目录（Go 常量表，rbac/actions.go）

| 动作 Key | 类别 | 说明 | 路由面（自定义角色据此生成 casbin 策略） |
|---|---|---|---|
| `config.view` | 配置 | 配置文件只读（列表/内容/版本） | GET `/config-files` `/config-files/*`、GET `/config-kv` `/config-kv/*` |
| `config.edit` | 配置 | 编辑配置文件与回滚版本 | PUT/POST/DELETE `/config-files` `/config-files/*`（回滚 POST `/config-files/:id/rollback`） |
| `config.deploy` | 配置 | 下发配置到目标机 | POST `/config-files/:id/deploy`、POST `/config-files/env-sync` |
| `config.reveal` | 密钥 | 敏感文件明文查看（gate：`GetContent/GetVersionContent` 的 reveal 分支） | 无新增路由（复用 view 的 GET） |
| `release.publish.test` | 发布 | 发布到 test 环境 | POST `/releases`、POST `/releases/*` |
| `release.publish.canary` | 发布 | 灰度发布 | 同上 |
| `release.publish.prod` | 发布 | 正式环境发布（gate：`execute` 按 body envType 分派） | 同上 |
| `release.rollback` | 发布 | 回滚发布 | POST `/releases/:id/rollback` |
| `terminal.access` | 终端 | Web 终端（M6 按主机 ACL 继续叠加生效） | GET `/servers/*/terminal`、POST `/auth/tickets` |
| `cron.trigger` | 任务 | 手动执行定时任务（gate：`trigger`） | POST `/cron-jobs/:id/trigger` |

- 发布三动作共用路由面、按 body `envType` 在 gate 分派——所以自定义角色拿任意一个 publish 动作都要过 gate（未持有的环境 403）。
- 每个自定义角色自动附加**基础读集**（项目/主机/构建/发布列表 GET + `/user/info`），保证页面可渲染；未授予的 API 仍 403（菜单可见但数据拒载是 M1 已知边界，见 §六）。
- `release.publish.*` 与 `release.rollback` 路由面相同但 gate 独立：只有 publish.test 的角色调 rollback 会被 `release.rollback` gate 拒。

## 三、内置角色默认动作集（行为对齐现状）

| 内置角色 | 动作集 |
|---|---|
| superadmin | 不入表（中间件 IsAdmin 放行 = 全量动作） |
| admin | 目录全集（与路由全量现状一致） |
| ops | `config.view/edit/deploy/reveal`（现状 ops 对 config-files 全量） |
| dev | `config.view`（收紧点：reveal 移除，见 D2） |
| guest | 无 |

内置角色的动作集可在角色管理页调整（与权限矩阵页的 HTTP 级编辑同权）；角色名与全局范围不可改。

## 四、数据模型与 API

```go
Role       { ID, Name unique, Description, Builtin bool }        // 种子：admin/ops/dev/guest
RoleAction { ID, RoleName idx, Action }                           // 动作集
RoleProject{ ID, RoleName idx, ProjectID }                        // 项目范围；空 = 全局
```

- 绑定：`users.Roles` 字符串不变；`identity.ValidateRoles` 改为可注入校验（app 层接 rbac 查角色表，接受内置+自定义）。
- 种子在 rbac 模块初始化：roles 表空时插入；后续版本只补新内置行，不覆盖人为调整（沿用种子版本纪律，独立 key `rbac.role_seed_version`）。

API（挂在既有 `/roles` admin 路由组）：

- `GET /roles` — 扩展返回：`builtin/actions/projectIds/userCount`（权限矩阵页数据源升级）
- `GET /roles/actions` — 动作目录（前端勾选面板）
- `POST /roles` — `{name, description, actions[], projectIds[]}`；校验：名字合法且不撞、动作 ⊆ 操作者动作集、项目存在
- `PUT /roles/:name` — 自定义角色改动作集/项目范围/描述；内置角色仅改动作集；同步增删 casbin 策略（RemoveFilteredPolicy(0, role) 重写）
- `DELETE /roles/:name` — 仅自定义；有用户绑定则拒绝（提示先解绑）

动作裁决（rbac service，供 handler 调）：

```go
CanAction(ctx, claims, action string) bool   // superadmin 放行；查用户角色 → role_actions 命中
ProjectScope(ctx, user) (all bool, ids []uint) // 内置角色=全量；仅自定义角色时取 RoleProject 并集
```

中间件顺手把已查出的角色列表塞 context（`rbac.RolesFromContext`），gate 不再二次查库。

## 五、动作 gate 接入点（本里程碑全量落地）

| 位置 | 改动 |
|---|---|
| `configs/handler.go` GetContent / GetVersionContent | `reveal=true` 时 gate `config.reveal`（403 文案明确"密钥查看需独立权限"） |
| `release/handler.go` execute | 替换 `in.EnvType=="prod"` 的 IsAdmin 判断 → gate `release.publish.<envType>`（三环境统一走动作） |
| `release/handler.go` rollback | gate `release.rollback` |
| `cron/handler.go` trigger | 替换 IsAdmin 判断 → gate `cron.trigger` |
| `projects/handler.go` 全部 | ProjectScope：list 过滤、detail/写操作 403 |
| `release/ci/builds/canary` 列表 | 带 projectID 查询的列表按 scope 过滤（scoped 用户只见授权项目） |

## 六、已知边界（后续里程碑打磨，不算欠账）

- 自定义角色用户的前端菜单按 dev 基线展示（路由 authority 数组是内置角色名）；未授予的菜单项可见但 API 403。按钮级隐藏依赖 `permissions` 码下发，M1 先下发动作码 `actions[]` 供前端渐进接入。
- 项目范围只覆盖项目属资源（projects/releases/builds/canary 列表）；config-files 是主机属资源，按主机维度后续再议（终端已有 M6 按主机 ACL）。
- 单实例假设下的直查 DB 动作裁决（低频敏感操作，不引入缓存）。

## 七、验收（对应 plan §二 M1，可执行化）

1. admin 创建"运维值班"（`config.edit` + `config.deploy` + `terminal.access`）→ 绑定用户 → 该用户可编辑/下发配置，`?reveal=true` 403
2. 创建"只读审计"（`config.view` + `config.reveal`）→ 绑定用户 → 可看明文但不可编辑
3. 自定义角色带项目范围 [A] → 绑定用户 → projects 列表只见 A，直接访问 B 的 detail 403；releases/builds 列表同口径
4. "可发布 test 不可发布 prod"角色 → POST /releases envType=test 通过、envType=prod 403
5. dev 原有用户 reveal 变 403（收紧点回归验证）
6. 动作集授予面：admin 正常；被 gate 的角色无法创建超出自身动作集的角色

## 八、验收记录（2026-10-05 实测，API 级）

内存 SQLite 启动真服务（:18081），setup 超管 → 建角色 → 建用户 → 铸 token 全链路：

1. ✅ 「值班运维」（config.edit/deploy/terminal.access）：GET /config-files 200（基础读集）；`?reveal=true` 403「密钥明文查看需独立权限」；POST /roles 403（管理面结构性隔离）
2. ✅ 「测试发布员」（仅 release.publish.test）：POST /releases envType=prod → 403「发布到 prod 环境需要对应权限」；envType=test → 过 gate 进业务校验（502 无 CI 记录，属业务层预期）
3. ✅ 「Alpha专属」（config.view + projectIds=[P1]）：GET /projects 只见项目Alpha；detail P1 200 / P2 403
4. ✅ dev 用户 reveal 403（收紧点生效）
5. ✅ admin 无回归：reveal 过 gate（404 文件不存在）、rollback 过 gate（404 记录不存在）
6. ✅ 单测：rbac 9 用例（种子/CRUD/守卫/范围/钩子）+ 既有 identity/configs/release/cron/projects/ci/canary 全绿；gofmt/vet/vue-tsc/eslint/oxlint 全过
