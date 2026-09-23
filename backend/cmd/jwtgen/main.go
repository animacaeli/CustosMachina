// cmd/jwtgen 开发调试用：按指定/默认密钥签发超管 JWT（仅本地冒烟辅助，不进产物）。
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/custos-machina/backend/internal/config"
	"github.com/custos-machina/backend/internal/pkg/jwt"
)

func main() {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "change-me-in-production"
	}
	cfg := &config.Config{Auth: config.Auth{
		JWTSecret: secret, TokenTTL: time.Hour, Issuer: "custos-machina",
	}}
	m := jwt.NewManager(cfg)
	t, err := m.Generate(1, "smoke", true)
	if err != nil {
		panic(err)
	}
	fmt.Println(t)
}
