// Package auth 提供本地超管登录、JWT 会话与 IM 扫码登录
// （IdentityProvider 插件，FR2.1；首家实现企业微信，T3 已定）。
package auth

import (
	"context"
	"errors"
	"sync"

	"golang.org/x/crypto/bcrypt"

	"github.com/custos-machina/backend/internal/config"
	"github.com/custos-machina/backend/internal/modules/identity"
	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
	jwtpkg "github.com/custos-machina/backend/internal/pkg/jwt"
)

var (
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrUserDisabled       = errors.New("账号已禁用")
)

type AuthService struct {
	users         identity.UserRepository
	bindings      identity.IMBindingRepository
	settings      identity.SettingsRepository
	jwt           *jwtpkg.Manager
	cfg           *config.Config
	cipher        *cryptopkg.Cipher
	qrStates      sync.Map            // state -> 过期时间，5 分钟有效
	refreshHolder *refreshStoreHolder // refresh token 存储（Redis/内存）
	tickets       *ticketStore        // WS/SSE 一次性短时 ticket
}

func NewAuthService(
	users identity.UserRepository,
	bindings identity.IMBindingRepository,
	settings identity.SettingsRepository,
	jwt *jwtpkg.Manager,
	cfg *config.Config,
	cipher *cryptopkg.Cipher,
) *AuthService {
	return &AuthService{
		users:         users,
		bindings:      bindings,
		settings:      settings,
		jwt:           jwt,
		cfg:           cfg,
		cipher:        cipher,
		refreshHolder: newRefreshStoreHolder(),
		tickets:       newTicketStore(),
	}
}

// dummyHash 用户不存在时的假哈希：预跑一次同代价比较拉平耗时，
// 消除"用户名存在性"时序侧信道（P5 M1 安全欠账 #2）。
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password-for-timing"), bcrypt.DefaultCost)

// LoginLocal 本地账号登录（超管 break-glass / 开发期使用）。
func (s *AuthService) LoginLocal(ctx context.Context, username, password string) (*LoginResult, error) {
	u, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, ErrInvalidCredentials
	}
	if u.PasswordHash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	if u.Status == identity.StatusDisabled {
		return nil, ErrUserDisabled
	}
	return s.IssueTokenPair(ctx, u)
}

func (s *AuthService) ParseToken(tokenStr string) (*jwtpkg.Claims, error) {
	return s.jwt.Parse(tokenStr)
}
