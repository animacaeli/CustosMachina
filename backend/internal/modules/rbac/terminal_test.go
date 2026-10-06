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
