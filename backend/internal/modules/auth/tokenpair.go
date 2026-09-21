package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/custos-machina/backend/internal/modules/identity"
)

// 双 token（FR2.3）：access 走 JWT 短效（默认 30m，可在系统设置调整），
// refresh 为随机 opaque token（默认 7d），存 RefreshStore（Redis/内存），
// 刷新即轮换（旧的取出即作废），登出可吊销。
var ErrInvalidRefreshToken = errors.New("refresh token 无效或已过期")

const (
	settingKeyAccessTTL  = "token.access_ttl"  // 如 "30m"
	settingKeyRefreshTTL = "token.refresh_ttl" // 如 "168h"
	settingKeyRedis      = "redis.config"      // RedisConfig JSON

	defaultAccessTTL  = 30 * time.Minute
	defaultRefreshTTL = 7 * 24 * time.Hour
)

// LoginResult 双 token 登录返回。
type LoginResult struct {
	AccessToken  string        `json:"accessToken"`
	RefreshToken string        `json:"refreshToken"`
	User         identity.User `json:"user"`
}

func (s *AuthService) accessTTL() time.Duration {
	if v, ok, _ := s.settings.Get(context.Background(), settingKeyAccessTTL); ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultAccessTTL
}

func (s *AuthService) refreshTTL() time.Duration {
	if v, ok, _ := s.settings.Get(context.Background(), settingKeyRefreshTTL); ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return defaultRefreshTTL
}

func (s *AuthService) refreshStore(ctx context.Context) (RefreshStore, error) {
	var cfg *RedisConfig
	if raw, ok, _ := s.settings.Get(ctx, settingKeyRedis); ok && raw != "" {
		c := &RedisConfig{}
		if err := unmarshalJSON(raw, c); err == nil && c.Addr != "" {
			cfg = c
		}
	}
	return s.refreshHolder.Get(ctx, cfg)
}

// IssueTokenPair 签发 access + refresh。
func (s *AuthService) IssueTokenPair(ctx context.Context, u *identity.User) (*LoginResult, error) {
	access, err := s.jwt.GenerateWithTTL(u.ID, u.DisplayName, u.IsLocalAdmin, s.accessTTL())
	if err != nil {
		return nil, err
	}
	refresh := randomToken()
	store, err := s.refreshStore(ctx)
	if err != nil {
		return nil, err
	}
	if err := store.Save(ctx, refresh, u.ID, s.refreshTTL()); err != nil {
		return nil, err
	}
	return &LoginResult{AccessToken: access, RefreshToken: refresh, User: *u}, nil
}

// Refresh 用 refresh token 换新的 token 对（轮换：旧 refresh 立即作废）。
func (s *AuthService) Refresh(ctx context.Context, refreshToken string) (*LoginResult, error) {
	if refreshToken == "" {
		return nil, ErrInvalidRefreshToken
	}
	store, err := s.refreshStore(ctx)
	if err != nil {
		return nil, err
	}
	uid, ok, err := store.Consume(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrInvalidRefreshToken
	}
	u, err := s.users.GetByID(ctx, uid)
	if err != nil {
		return nil, ErrInvalidRefreshToken
	}
	if u.Status == identity.StatusDisabled {
		return nil, ErrUserDisabled
	}
	return s.IssueTokenPair(ctx, u)
}

// Logout 吊销 refresh token（access 短效自然过期）。
func (s *AuthService) Logout(ctx context.Context, refreshToken string) error {
	store, err := s.refreshStore(ctx)
	if err != nil {
		return err
	}
	return store.Revoke(ctx, refreshToken)
}

// TokenTTLs 当前 token 有效期（系统设置页展示）。
func (s *AuthService) TokenTTLs() (access, refresh time.Duration) {
	return s.accessTTL(), s.refreshTTL()
}

// SetTokenTTLs 修改 token 有效期（管理员系统设置）。
func (s *AuthService) SetTokenTTLs(ctx context.Context, access, refresh time.Duration) error {
	if access <= 0 || refresh <= 0 {
		return errors.New("有效期必须为正")
	}
	if access >= refresh {
		return errors.New("access 有效期应短于 refresh")
	}
	if err := s.settings.Set(ctx, settingKeyAccessTTL, access.String()); err != nil {
		return err
	}
	return s.settings.Set(ctx, settingKeyRefreshTTL, refresh.String())
}

// SaveRedisConfig 保存并验证 Redis 配置（setup 向导 / 系统设置）。
func (s *AuthService) SaveRedisConfig(ctx context.Context, cfg RedisConfig) error {
	if _, err := s.refreshHolder.Get(ctx, &cfg); err != nil {
		return err // 连接失败直接报错
	}
	return s.settings.Set(ctx, settingKeyRedis, marshalJSON(cfg))
}

// RedisConfigCurrent 当前生效的 Redis 配置（未配置返回空 addr）。
func (s *AuthService) RedisConfigCurrent() RedisConfig {
	if raw, ok, _ := s.settings.Get(context.Background(), settingKeyRedis); ok {
		c := RedisConfig{}
		if unmarshalJSON(raw, &c) == nil {
			return c
		}
	}
	return RedisConfig{}
}

func randomToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func unmarshalJSON(s string, v any) error { return json.Unmarshal([]byte(s), v) }
func marshalJSON(v any) string            { b, _ := json.Marshal(v); return string(b) }

// jsonMarshalString 供 handler 序列化 config map。
func jsonMarshalString(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
