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
	users    identity.UserRepository
	bindings identity.IMBindingRepository
	jwt      *jwtpkg.Manager
	cfg      *config.Config
	cipher   *cryptopkg.Cipher
	qrStates sync.Map // state -> 过期时间，5 分钟有效
}

func NewAuthService(
	users identity.UserRepository,
	bindings identity.IMBindingRepository,
	jwt *jwtpkg.Manager,
	cfg *config.Config,
	cipher *cryptopkg.Cipher,
) *AuthService {
	return &AuthService{users: users, bindings: bindings, jwt: jwt, cfg: cfg, cipher: cipher}
}

type LoginResult struct {
	Token string        `json:"token"`
	User  identity.User `json:"user"`
}

// LoginLocal 本地账号登录（超管 break-glass / 开发期使用）。
func (s *AuthService) LoginLocal(ctx context.Context, username, password string) (*LoginResult, error) {
	u, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		return nil, ErrInvalidCredentials
	}
	if u.PasswordHash == "" {
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	if u.Status == identity.StatusDisabled {
		return nil, ErrUserDisabled
	}
	token, err := s.jwt.Generate(u.ID, u.DisplayName, u.IsLocalAdmin)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, User: *u}, nil
}

// IssueToken 为 IM 扫码登录（JIT 注册后）签发 token 复用。
func (s *AuthService) IssueToken(u *identity.User) (*LoginResult, error) {
	token, err := s.jwt.Generate(u.ID, u.DisplayName, u.IsLocalAdmin)
	if err != nil {
		return nil, err
	}
	return &LoginResult{Token: token, User: *u}, nil
}

func (s *AuthService) ParseToken(tokenStr string) (*jwtpkg.Claims, error) {
	return s.jwt.Parse(tokenStr)
}
