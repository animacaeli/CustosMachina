// Package config 负责加载与校验平台配置。
// 所有配置项均可通过环境变量覆盖（CUSTOS_ 前缀），与 deploy/ 下的 env 示例对应。
package config

import (
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	HTTP     HTTP
	Database Database
	Auth     Auth
	Secrets  Secrets
	IM       IM
}

type HTTP struct {
	Addr            string
	Mode            string // gin mode: debug / release / test
	ShutdownTimeout time.Duration
}

type Database struct {
	Driver string // sqlite / mysql / postgres
	DSN    string
}

type Auth struct {
	JWTSecret string
	TokenTTL  time.Duration
	Issuer    string
}

// Secrets 用于组件凭证 / SSH 私钥的 AES-256-GCM 加密主密钥。
type Secrets struct {
	MasterKey string // 32 字节 hex；生产环境必须显式注入
}

// IM 扫码登录相关（FR2）。Provider: wecom / mock（本地联调）。
type IM struct {
	Provider    string
	PublicURL   string // 平台对外可达地址（企微回调要求公网 HTTPS）
	FrontendURL string // 回调成功后重定向回的前端地址
}

func Load() (*Config, error) {
	v := viper.New()
	v.SetEnvPrefix("CUSTOS")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	v.SetDefault("http.addr", ":8080")
	v.SetDefault("http.mode", "debug")
	v.SetDefault("http.shutdown_timeout", "10s")
	v.SetDefault("database.driver", "sqlite")
	v.SetDefault("database.dsn", "data/custos.db")
	v.SetDefault("auth.jwt_secret", "change-me-in-production")
	v.SetDefault("auth.token_ttl", "24h")
	v.SetDefault("auth.issuer", "custos-machina")
	v.SetDefault("secrets.master_key", "")
	v.SetDefault("im.provider", "wecom")
	v.SetDefault("im.public_url", "")
	v.SetDefault("im.frontend_url", "http://localhost:5666")

	// 注意：viper 的 AutomaticEnv 对嵌套 key 的 Unmarshal 不可靠，
	// 这里显式逐项读取，保证 env 覆盖一定生效。
	cfg := &Config{
		HTTP: HTTP{
			Addr:            v.GetString("http.addr"),
			Mode:            v.GetString("http.mode"),
			ShutdownTimeout: v.GetDuration("http.shutdown_timeout"),
		},
		Database: Database{
			Driver: v.GetString("database.driver"),
			DSN:    v.GetString("database.dsn"),
		},
		Auth: Auth{
			JWTSecret: v.GetString("auth.jwt_secret"),
			TokenTTL:  v.GetDuration("auth.token_ttl"),
			Issuer:    v.GetString("auth.issuer"),
		},
		Secrets: Secrets{
			MasterKey: v.GetString("secrets.master_key"),
		},
		IM: IM{
			Provider:    v.GetString("im.provider"),
			PublicURL:   v.GetString("im.public_url"),
			FrontendURL: v.GetString("im.frontend_url"),
		},
	}
	return cfg, nil
}
