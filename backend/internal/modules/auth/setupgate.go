package auth

import (
	"context"
	"encoding/json"
)

// SetupGate 暴露给 setup 向导的受控子集（避免向导直接持有整个 AuthService）。
type SetupGate struct{ svc *AuthService }

func NewSetupGate(svc *AuthService) *SetupGate { return &SetupGate{svc: svc} }

// SaveIMProviderConfigMap 以 map 形式保存提供商凭证（向导前端直接提交表单字段）。
func (g *SetupGate) SaveIMProviderConfigMap(ctx context.Context, provider string, cfg map[string]string, enabled bool) error {
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return g.svc.SaveIMProviderConfig(ctx, provider, string(b), enabled)
}

// SaveRedis 保存并验证 Redis 配置。
func (g *SetupGate) SaveRedis(ctx context.Context, addr, password string, db int) error {
	return g.svc.SaveRedisConfig(ctx, RedisConfig{Addr: addr, Password: password, DB: db})
}
