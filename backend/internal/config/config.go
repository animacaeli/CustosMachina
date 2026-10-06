// Package config 负责加载与校验平台配置。
// 所有配置项均可通过环境变量覆盖（CUSTOS_ 前缀），与 deploy/ 下的 env 示例对应。
// 实现为纯 os.Getenv + 默认值表（v0.12.0 审计依赖瘦身：viper 的
// 配置文件/TOML/Watch 等能力全部闲置，却拖入 hcl/toml/ini/fsnotify 等
// 数十个间接包——30 行 getenv 表语义完全等价）。
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTP     HTTP
	Database Database
	Auth     Auth
	Secrets  Secrets
	IM       IM
	Redis    Redis
	Log      Log
	CORS     CORS
}

// CORS 跨域来源白名单；逗号分隔。默认 * 兼容既有部署，生产建议
// 配置为前端实际域名（单镜像同源部署可直接配自身域名）。
type CORS struct {
	Origins string
}

// Log 日志配置（第三阶段 M0：zap + 文件滚动）。
type Log struct {
	Level      string // debug / info / warn / error
	Dir        string // 日志目录；空 = 只输出 stderr；容器部署映射为宿主机卷
	MaxSizeMB  int
	MaxBackups int
	MaxAgeDays int
}

type HTTP struct {
	Addr            string
	Mode            string // gin mode: debug / release / test
	ShutdownTimeout time.Duration
	TrustedProxies  string // 逗号分隔的 IP/CIDR（决定 ClientIP 从 XFF 取到哪一跳）
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

// Redis：refresh token 存储等；env 兜底（setup 向导/系统设置里的配置优先）。
type Redis struct {
	Addr     string
	Password string
	DB       int
}

// IM 扫码登录相关（FR2）。Provider: wecom / mock（本地联调）。
type IM struct {
	Provider    string
	PublicURL   string // 平台对外可达地址（企微回调要求公网 HTTPS）
	FrontendURL string // 回调成功后重定向回的前端地址
}

// env 读 CUSTOS_<KEY>，未设置返回 def。
func env(key, def string) string {
	if v, ok := os.LookupEnv("CUSTOS_" + key); ok && v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv("CUSTOS_" + key); ok && v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string) bool {
	v, _ := os.LookupEnv("CUSTOS_" + key)
	return v == "1" || strings.EqualFold(v, "true")
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv("CUSTOS_" + key); ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func Load() (*Config, error) {
	cfg := &Config{
		HTTP: HTTP{
			Addr:            env("HTTP_ADDR", ":8080"),
			Mode:            env("HTTP_MODE", "debug"),
			ShutdownTimeout: envDuration("HTTP_SHUTDOWN_TIMEOUT", 10*time.Second),
			// 可信代理：单镜像内 nginx(127.0.0.1) + 自托管常见私网链路；
			// 外层代理须设置 X-Forwarded-For，否则限速/审计按代理 IP 聚合（见 deploy-conventions）
			TrustedProxies: env("HTTP_TRUSTED_PROXIES", "127.0.0.1,::1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"),
		},
		Database: Database{
			Driver: env("DATABASE_DRIVER", "sqlite"),
			DSN:    env("DATABASE_DSN", "data/custos.db"),
		},
		Auth: Auth{
			JWTSecret: env("AUTH_JWT_SECRET", "change-me-in-production"),
			TokenTTL:  envDuration("AUTH_TOKEN_TTL", 24*time.Hour),
			Issuer:    env("AUTH_ISSUER", "custos-machina"),
		},
		Secrets: Secrets{
			MasterKey: env("SECRETS_MASTER_KEY", ""),
		},
		IM: IM{
			Provider:    env("IM_PROVIDER", "wecom"),
			PublicURL:   env("IM_PUBLIC_URL", ""),
			FrontendURL: env("IM_FRONTEND_URL", "http://localhost:5666"),
		},
		Redis: Redis{
			Addr:     env("REDIS_ADDR", ""),
			Password: env("REDIS_PASSWORD", ""),
			DB:       envInt("REDIS_DB", 0),
		},
		CORS: CORS{
			// CORS 默认同源（不回 CORS 头）：单镜像部署前后端同源，无需跨域；
			// 跨域部署显式配置来源白名单（P5 M1 安全欠账收敛，旧默认 * 已废弃）
			Origins: env("CORS_ORIGINS", ""),
		},
		Log: Log{
			Level:      env("LOG_LEVEL", "info"),
			Dir:        env("LOG_DIR", "data/logs"),
			MaxSizeMB:  envInt("LOG_MAX_SIZE_MB", 50),
			MaxBackups: envInt("LOG_MAX_BACKUPS", 5),
			MaxAgeDays: envInt("LOG_MAX_AGE_DAYS", 14),
		},
	}
	// P8-M1 安全硬化：JWT secret 为默认值或过短一律拒绝启动（生产防呆；
	// 双 token 会话体系全系签名依赖它）。开发直跑显式豁免：
	// CUSTOS_AUTH_ALLOW_DEFAULT_SECRET=1（make dev 已注入）。
	if cfg.Auth.JWTSecret == "change-me-in-production" || len(cfg.Auth.JWTSecret) < 32 {
		if !envBool("AUTH_ALLOW_DEFAULT_SECRET") {
			return nil, fmt.Errorf("CUSTOS_AUTH_JWT_SECRET 未设置或过短（<32 字节）——生产部署必须显式配置强随机密钥；本地开发可设 CUSTOS_AUTH_ALLOW_DEFAULT_SECRET=1 豁免")
		}
	}
	return cfg, nil
}
