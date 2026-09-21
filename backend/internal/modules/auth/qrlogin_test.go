package auth

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/config"
	"github.com/custos-machina/backend/internal/modules/identity"
	jwtpkg "github.com/custos-machina/backend/internal/pkg/jwt"
)

// mocks：内存仓储桩，覆盖 QR 链路用到的方法。
type mockUsers struct {
	identity.UserRepository
	byID map[uint]*identity.User
	next uint
}

func (m *mockUsers) Create(_ context.Context, u *identity.User) error {
	m.next++
	u.ID = m.next
	m.byID[u.ID] = u
	return nil
}

func (m *mockUsers) GetByID(_ context.Context, id uint) (*identity.User, error) {
	if u, ok := m.byID[id]; ok {
		return u, nil
	}
	return nil, gorm.ErrRecordNotFound
}

type mockBindings struct {
	identity.IMBindingRepository
	bindings map[string]*identity.UserIMBinding // key: provider|imUserID
}

func (m *mockBindings) GetBinding(_ context.Context, provider, imUserID string) (*identity.UserIMBinding, error) {
	if b, ok := m.bindings[provider+"|"+imUserID]; ok {
		return b, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (m *mockBindings) SaveBinding(_ context.Context, b *identity.UserIMBinding) error {
	m.bindings[b.Provider+"|"+b.IMUserID] = b
	return nil
}

func newTestAuthService(t *testing.T) *AuthService {
	t.Helper()
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "test-secret"
	cfg.Auth.Issuer = "custos-machina"
	cfg.Auth.TokenTTL = time.Hour
	cfg.IM.Provider = "mock"
	cfg.IM.PublicURL = "https://custos.example.com"
	cfg.IM.FrontendURL = "http://localhost:5666"
	return NewAuthService(
		&mockUsers{byID: map[uint]*identity.User{}},
		&mockBindings{bindings: map[string]*identity.UserIMBinding{}},
		&mockSettings{kv: map[string]string{}},
		jwtpkg.NewManager(cfg),
		cfg,
		nil,
	)
}

func TestQRCallback_JITRegisterThenRelogin(t *testing.T) {
	svc := newTestAuthService(t)
	ctx := context.Background()

	// 拿授权地址，从中取 state
	urlStr, err := svc.QRLoginURL(ctx)
	if err != nil {
		t.Fatalf("生成授权地址失败: %v", err)
	}
	state := urlStr[len(urlStr)-32:] // mock provider 的 state 是 16 字节 hex

	// 首次扫码：JIT 注册 guest 用户
	r1, err := svc.HandleQRCallback(ctx, "zhangsan", state)
	if err != nil {
		t.Fatalf("首次回调失败: %v", err)
	}
	if r1.User.Roles != "guest" {
		t.Errorf("JIT 注册默认角色应为 guest，实际 %q", r1.User.Roles)
	}
	if r1.User.DisplayName != "Mock 用户 zhangsan" {
		t.Errorf("显示名应取 IM 昵称，实际 %q", r1.User.DisplayName)
	}
	if r1.AccessToken == "" || r1.RefreshToken == "" {
		t.Error("应签发 token")
	}

	// state 一次性：重放应失败
	if _, err := svc.HandleQRCallback(ctx, "zhangsan", state); err == nil {
		t.Error("state 重放应被拒绝")
	}

	// 二次登录：复用绑定，不新建用户
	urlStr2, _ := svc.QRLoginURL(ctx)
	state2 := urlStr2[len(urlStr2)-32:]
	r2, err := svc.HandleQRCallback(ctx, "zhangsan", state2)
	if err != nil {
		t.Fatalf("二次回调失败: %v", err)
	}
	if r2.User.ID != r1.User.ID {
		t.Errorf("同一 IM 用户应复用同一平台用户：%d vs %d", r1.User.ID, r2.User.ID)
	}
}

func TestQRLoginURL_MockShape(t *testing.T) {
	svc := newTestAuthService(t)
	u, err := svc.QRLoginURL(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := svc.cfg.IM.PublicURL + "/api/auth/qrlogin/callback?provider=mock&code=mock-user&state="; len(u) < len(want) || u[:len(want)] != want {
		t.Errorf("mock 授权地址形态不符: %s", u)
	}
}

// mockSettings KV 设置桩。
type mockSettings struct {
	identity.SettingsRepository
	kv map[string]string
}

func (m *mockSettings) Get(_ context.Context, key string) (string, bool, error) {
	v, ok := m.kv[key]
	return v, ok, nil
}

func (m *mockSettings) Set(_ context.Context, key, value string) error {
	m.kv[key] = value
	return nil
}

func TestTokenPair_RefreshRotateAndLogout(t *testing.T) {
	svc := newTestAuthService(t)
	ctx := context.Background()

	u := &identity.User{DisplayName: "u", Roles: "guest"}
	if err := svc.users.Create(ctx, u); err != nil {
		t.Fatalf("建用户失败: %v", err)
	}
	pair, err := svc.IssueTokenPair(ctx, u)
	if err != nil {
		t.Fatalf("签发失败: %v", err)
	}
	// 刷新（轮换）
	pair2, err := svc.Refresh(ctx, pair.RefreshToken)
	if err != nil {
		t.Fatalf("刷新失败: %v", err)
	}
	if pair2.AccessToken == "" || pair2.RefreshToken == "" {
		t.Fatal("刷新后应返回新 token 对")
	}
	// 旧 refresh 已作废
	if _, err := svc.Refresh(ctx, pair.RefreshToken); err == nil {
		t.Error("旧 refresh token 应已轮换作废")
	}
	// 登出吊销
	if err := svc.Logout(ctx, pair2.RefreshToken); err != nil {
		t.Fatalf("登出失败: %v", err)
	}
	if _, err := svc.Refresh(ctx, pair2.RefreshToken); err == nil {
		t.Error("登出后 refresh token 应已吊销")
	}
}

func TestSetTokenTTLs(t *testing.T) {
	svc := newTestAuthService(t)
	ctx := context.Background()
	a, r := svc.TokenTTLs()
	if a != 30*time.Minute || r != 7*24*time.Hour {
		t.Errorf("默认 TTL 不符: %v / %v", a, r)
	}
	if err := svc.SetTokenTTLs(ctx, time.Hour, 48*time.Hour); err != nil {
		t.Fatalf("设置失败: %v", err)
	}
	a, r = svc.TokenTTLs()
	if a != time.Hour || r != 48*time.Hour {
		t.Errorf("设置后 TTL 不符: %v / %v", a, r)
	}
	if err := svc.SetTokenTTLs(ctx, 2*time.Hour, time.Hour); err == nil {
		t.Error("access >= refresh 应被拒绝")
	}
}
