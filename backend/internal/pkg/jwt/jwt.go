// Package jwt 提供 token 签发与校验，auth 模块独占使用。
package jwt

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/custos-machina/backend/internal/config"
)

var ErrInvalidToken = errors.New("无效的 token")

type Claims struct {
	UserID      uint   `json:"uid"`
	DisplayName string `json:"name"`
	IsAdmin     bool   `json:"adm"`
	jwt.RegisteredClaims
}

type Manager struct {
	secret []byte
	issuer string
	ttl    time.Duration
}

func NewManager(cfg *config.Config) *Manager {
	return &Manager{secret: []byte(cfg.Auth.JWTSecret), issuer: cfg.Auth.Issuer, ttl: cfg.Auth.TokenTTL}
}

func (m *Manager) Generate(userID uint, displayName string, isAdmin bool) (string, error) {
	return m.GenerateWithTTL(userID, displayName, isAdmin, m.ttl)
}

// GenerateWithTTL 按给定有效期签发（access 短效 / refresh 由调用方另行存储，不走 JWT）。
func (m *Manager) GenerateWithTTL(userID uint, displayName string, isAdmin bool, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:      userID,
		DisplayName: displayName,
		IsAdmin:     isAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    m.issuer,
			Subject:   displayName,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

func (m *Manager) Parse(tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*Claims)
	if !ok {
		return nil, ErrInvalidToken
	}
	return claims, nil
}
