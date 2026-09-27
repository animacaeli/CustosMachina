package main

import (
	"fmt"
	"time"

	"github.com/custos-machina/backend/internal/config"
	"github.com/custos-machina/backend/internal/pkg/jwt"
)

func main() {
	cfg := &config.Config{}
	cfg.Auth.JWTSecret = "change-me-in-production"
	cfg.Auth.Issuer = "custos-machina"
	cfg.Auth.TokenTTL = 3 * time.Hour
	tok, err := jwt.NewManager(cfg).Generate(1, "root", true)
	if err != nil {
		panic(err)
	}
	fmt.Println(tok)
}
