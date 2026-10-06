package rbac

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/custos-machina/backend/internal/modules/identity"
	jwtpkg "github.com/custos-machina/backend/internal/pkg/jwt"
)

// 终端越权回归（v0.12.0 审计严重项 1）：keyMatch 前缀通配 /servers/* 会命中
// /servers/:id/terminal，旧实现下 dev/ops/自定义角色可打开任意主机终端。
// gate 后通配一律不认，只认内置 admin 角色与登录名精确 ACL。
func TestTerminalGate(t *testing.T) {
	svc := newTestEnforcer(t)

	// 前提复现：通配策略本身仍会放行（证明 gate 必要性）
	ok, err := EnforceAny(svc.enforcer, identity.ParseRoleList("dev"), "/servers/1/terminal", "GET")
	if err != nil || !ok {
		t.Fatalf("前提失败：dev 经 /servers/* 通配应可 Enforce（got=%v err=%v），gate 需拦住它", ok, err)
	}

	cases := []struct {
		name     string
		roles    []string
		username string
		serverID uint
		want     bool
	}{
		{"dev 角色被拒", []string{"dev"}, "", 1, false},
		{"ops 角色被拒", []string{"ops"}, "", 1, false},
		{"自定义角色被拒", []string{"发布员"}, "", 1, false},
		{"admin 角色放行", []string{"admin"}, "", 1, true},
		{"多角色含 admin 放行", []string{"dev", "admin"}, "", 1, true},
		{"无 ACL 的登录名被拒", []string{"dev"}, "alice", 1, false},
		{"用户名撞 admin 字面量不等于角色", []string{"dev"}, "admin", 1, false},
		{"serverID=0 拒绝", []string{"dev"}, "alice", 0, false},
	}
	for _, tc := range cases {
		got := terminalAllowed(tc.roles, tc.username, tc.serverID)
		if got != tc.want {
			t.Errorf("%s: terminalAllowed(%v,%s,%d)=%v want %v", tc.name, tc.roles, tc.username, tc.serverID, got, tc.want)
		}
	}

	// 登录名精确 ACL：授权后放行，未授权主机仍拒
	if err := svc.SetServerTerminalACLs(1, []string{"alice"}); err != nil {
		t.Fatalf("写入终端 ACL 失败: %v", err)
	}
	if !terminalAllowed([]string{"dev"}, "alice", 1) {
		t.Error("alice 已被授权主机 1，应放行")
	}
	if terminalAllowed([]string{"dev"}, "alice", 2) {
		t.Error("alice 未被授权主机 2，应拒绝")
	}
	// ACL 收回后立即失效
	if err := svc.SetServerTerminalACLs(1, nil); err != nil {
		t.Fatalf("清空终端 ACL 失败: %v", err)
	}
	if terminalAllowed([]string{"dev"}, "alice", 1) {
		t.Error("alice 授权已清空，应拒绝")
	}
}

// handler 层兜底 gate：从 gin context 取主体裁决。
func TestTerminalAllowedForServer(t *testing.T) {
	svc := newTestEnforcer(t)
	_ = svc

	gin.SetMode(gin.TestMode)

	newCtx := func(isAdmin bool, roles []string, username string) *gin.Context {
		c := &gin.Context{}
		claims := &jwtpkg.Claims{UserID: 1, IsAdmin: isAdmin}
		c.Set(jwtpkg.CtxClaimsKey, claims)
		if !isAdmin {
			c.Set(subjectCtxKey, subject{Roles: roles, Username: username})
		}
		return c
	}

	if !TerminalAllowedForServer(newCtx(true, nil, ""), 5) {
		t.Error("超管应放行任意主机终端")
	}
	if TerminalAllowedForServer(newCtx(false, []string{"dev"}, "bob"), 5) {
		t.Error("dev 用户未授权应拒绝")
	}
	c := newCtx(false, []string{"dev"}, "bob")
	c.Request = httptest.NewRequest("GET", "/api/servers/5/terminal", nil)
	if TerminalAllowedForServer(c, 5) {
		t.Error("dev 用户未授权应拒绝（带请求）")
	}
}

func TestServerIDFromTerminalPath(t *testing.T) {
	cases := map[string]uint{
		"/servers/12/terminal":  12,
		"/servers/0/terminal":   0,
		"/servers/abc/terminal": 0,
		"/servers/1/x":          0,
		"/other/1/terminal":     0,
	}
	for path, want := range cases {
		if got := serverIDFromTerminalPath(path); got != want {
			t.Errorf("serverIDFromTerminalPath(%q)=%d want %d", path, got, want)
		}
	}
}

// 超管禁用/降级即时生效（v0.12.0 审计中等项）：claims.IsAdmin 不再直接放行。
func TestMiddlewareAdminDisabledAndDemoted(t *testing.T) {
	svc := newTestEnforcer(t)
	users := svc.users

	gin.SetMode(gin.TestMode)
	newCtx := func(claims *jwtpkg.Claims) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("GET", "/api/projects", nil)
		c.Set(jwtpkg.CtxClaimsKey, claims)
		return c
	}

	// 存量超管（newTestEnforcer 建的 "admin" 角色用户）——IsAdmin claims + 非 IsLocalAdmin → 降级路径
	u, _ := users.GetByID(t.Context(), 1)

	// 1) 正常超管（IsLocalAdmin）放行
	u.IsLocalAdmin = true
	_ = users.Update(t.Context(), u)
	claims := &jwtpkg.Claims{UserID: u.ID, IsAdmin: true}
	c := newCtx(claims)
	mw := NewMiddleware(MiddlewareDeps{Enforcer: svc.enforcer, Users: users})
	mw(c)
	if c.IsAborted() {
		t.Error("正常超管应放行")
	}

	// 2) 禁用即时拦截
	u.Status = identity.StatusDisabled
	_ = users.Update(t.Context(), u)
	c = newCtx(&jwtpkg.Claims{UserID: u.ID, IsAdmin: true})
	mw(c)
	if !c.IsAborted() {
		t.Error("禁用超管应被立即拦截（不等 TTL）")
	}

	// 3) 降级：IsAdmin claims 但库里已非超管 → 按普通角色裁决
	u.Status = identity.StatusActive
	u.IsLocalAdmin = false
	_ = users.Update(t.Context(), u)
	claims2 := &jwtpkg.Claims{UserID: u.ID, IsAdmin: true}
	c = newCtx(claims2)
	mw(c)
	// 该用户 roles=admin，走 casbin：/projects admin 有 GET → 放行但 claims 已纠正
	if c.IsAborted() {
		t.Error("降级后按普通角色裁决（admin 角色对 /projects 应放行）")
	}
	if claims2.IsAdmin {
		t.Error("降级后本请求 claims.IsAdmin 应被纠正为 false")
	}
}

// v0.12.1 复核 N3：dev 收 DELETE + 混合角色范围收窄。
func TestDevLosesAlertDelete(t *testing.T) {
	svc := newTestEnforcer(t)
	ok, err := EnforceAny(svc.enforcer, identity.ParseRoleList("dev"), "/observ/alerts/5", "DELETE")
	if err != nil || ok {
		t.Errorf("dev 不应再有告警 DELETE（got=%v err=%v）", ok, err)
	}
	ok, _ = EnforceAny(svc.enforcer, identity.ParseRoleList("dev"), "/observ/alerts/5", "GET")
	if !ok {
		t.Error("dev 告警查看应保留")
	}
}

func TestProjectScopeMixedRoles(t *testing.T) {
	svc := newTestEnforcer(t)
	// 自定义角色配了项目行：纯自定义 → 限定
	all, ids := svc.projectScopeOf([]string{"发布员"})
	if all || len(ids) != 0 {
		// 无行时仍全局（既有语义）
		if !all {
			t.Errorf("自定义角色无项目行应为全局: all=%v ids=%v", all, ids)
		}
	}
	if err := svc.db.Create(&RoleProject{RoleName: "发布员", ProjectID: 3}).Error; err != nil {
		t.Fatal(err)
	}
	// dev + 带行自定义 → 收窄到显式项目（不再被 dev 的全局性绕过）
	all, ids = svc.projectScopeOf([]string{"dev", "发布员"})
	if all {
		t.Error("混合角色且自定义角色有项目行时应收窄（dev 不再使其全局）")
	}
	if len(ids) != 1 || ids[0] != 3 {
		t.Errorf("范围应为 [3]，got %v", ids)
	}
	// 纯 dev 仍全局（slots/告警查看等既有行为不破坏）
	if all, _ = svc.projectScopeOf([]string{"dev"}); !all {
		t.Error("纯 dev 应保持全局")
	}
	// 管理角色恒全局
	if all, _ = svc.projectScopeOf([]string{"ops", "发布员"}); !all {
		t.Error("ops 混合仍应全局")
	}
}

// v0.12.2 复核 N3 残留：guest 不再能写任意项目测试槽位；且收权迁移
// 不复活管理员人为删除的条目。
func TestGuestLosesSlotsWrite(t *testing.T) {
	svc := newTestEnforcer(t)
	ok, _ := EnforceAny(svc.enforcer, identity.ParseRoleList("guest"), "/slots/1/occupy", "POST")
	if ok {
		t.Error("guest 不应再有 slots POST（自注册用户可触发任意项目部署的链路）")
	}
	if ok, _ = EnforceAny(svc.enforcer, identity.ParseRoleList("guest"), "/slots", "GET"); !ok {
		t.Error("guest slots 只读应保留")
	}
}

// deNarrowPolicies 直测（v0.12.3 复核：上一版是空转测试——被测函数是
// 闭包且从未被调用，任何实现都绿）。
func TestDeNarrowDoesNotResurrectDeleted(t *testing.T) {
	svc := newTestEnforcer(t)
	e := svc.enforcer
	// 场景①：管理员整体删除 dev 的 alerts 条目（连旧带新都删），
	// 从 v25 升级收权不得复活
	_, _ = e.RemovePolicy("dev", "/observ/alerts/*", "GET|DELETE|POST")
	_, _ = e.RemovePolicy("dev", "/observ/alerts/*", "GET|POST")
	if changed := deNarrowPolicies(e, "25"); changed {
		t.Error("旧条目已不存在时应为 no-op（changed=false）")
	}
	if has, _ := e.HasPolicy("dev", "/observ/alerts/*", "GET|POST"); has {
		t.Error("被删除的条目不应被复活")
	}

	// 场景②：存量 v24 库（dev 旧 GET|DELETE|POST 在）→ 收权生效且降级为新条目
	svc2 := newTestEnforcer(t)
	e2 := svc2.enforcer
	if _, err := e2.AddPolicy("dev", "/observ/alerts/*", "GET|DELETE|POST"); err != nil {
		t.Fatal(err)
	}
	if !deNarrowPolicies(e2, "24") {
		t.Error("移除旧条目应返回 changed=true")
	}
	if has, _ := e2.HasPolicy("dev", "/observ/alerts/*", "GET|DELETE|POST"); has {
		t.Error("旧条目应被移除")
	}
	if has, _ := e2.HasPolicy("dev", "/observ/alerts/*", "GET|POST"); !has {
		t.Error("新条目应被补上")
	}

	// 场景③：已是 v26+ 的库 → 幂等跳过
	if deNarrowPolicies(e2, "26") {
		t.Error("v26 及以上应直接跳过（changed=false）")
	}
	// 场景④：极旧部署（版本缺失不可解析）→ 照样收权
	svc3 := newTestEnforcer(t)
	e3 := svc3.enforcer
	_, _ = e3.AddPolicy("guest", "/slots", "GET|POST")
	if !deNarrowPolicies(e3, "") {
		t.Error("版本不可解析（极旧部署）应执行收权")
	}
	if has, _ := e3.HasPolicy("guest", "/slots", "POST"); has {
		t.Error("guest 旧 POST 条目应被移除")
	}
}
