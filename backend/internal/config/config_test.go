package config

import (
	"testing"
	"time"
)

// viper → os.Getenv 等价性（v0.12.0 审计依赖瘦身）：env 覆盖与默认值必须逐项成立。
func TestLoadEnvOverrideAndDefaults(t *testing.T) {
	t.Setenv("CUSTOS_AUTH_JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("CUSTOS_HTTP_ADDR", ":9090")
	t.Setenv("CUSTOS_DATABASE_DRIVER", "mysql")
	t.Setenv("CUSTOS_AUTH_TOKEN_TTL", "2h")
	t.Setenv("CUSTOS_REDIS_DB", "3")
	t.Setenv("CUSTOS_LOG_MAX_SIZE_MB", "99")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("加载失败: %v", err)
	}
	if cfg.HTTP.Addr != ":9090" || cfg.Database.Driver != "mysql" {
		t.Errorf("env 覆盖未生效: %+v", cfg.HTTP)
	}
	if cfg.Auth.TokenTTL != 2*time.Hour {
		t.Errorf("时长覆盖未生效: %v", cfg.Auth.TokenTTL)
	}
	if cfg.Redis.DB != 3 || cfg.Log.MaxSizeMB != 99 {
		t.Errorf("整数覆盖未生效: redis=%d log=%d", cfg.Redis.DB, cfg.Log.MaxSizeMB)
	}
	// 未设置项走默认
	if cfg.IM.Provider != "wecom" || cfg.Log.Level != "info" || cfg.HTTP.Mode != "debug" {
		t.Errorf("默认值不符: im=%s log=%s mode=%s", cfg.IM.Provider, cfg.Log.Level, cfg.HTTP.Mode)
	}
}

func TestLoadRejectsWeakJWTSecret(t *testing.T) {
	t.Setenv("CUSTOS_AUTH_JWT_SECRET", "")
	if _, err := Load(); err == nil {
		t.Error("默认 secret 且未豁免应拒绝启动")
	}
	t.Setenv("CUSTOS_AUTH_ALLOW_DEFAULT_SECRET", "1")
	if _, err := Load(); err != nil {
		t.Errorf("显式豁免后应放行: %v", err)
	}
}
