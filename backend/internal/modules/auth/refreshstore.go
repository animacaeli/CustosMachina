package auth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// RefreshStore refresh token 存储：签发写入、刷新时取出并删除（轮换）、登出吊销。
// 双实现：Redis（生产，支持跨实例与 TTL 自动过期）/ 内存（单实例开发降级）。
type RefreshStore interface {
	Save(ctx context.Context, token string, userID uint, ttl time.Duration) error
	// Consume 取出并删除；不存在/已过期返回 false。
	Consume(ctx context.Context, token string) (uint, bool, error)
	Revoke(ctx context.Context, token string) error
}

// --- 内存实现（sync.Map + 过期惰性清理） ---

type memoryRefreshStore struct {
	mu     sync.Mutex
	tokens map[string]memoryEntry
}

type memoryEntry struct {
	userID uint
	expire time.Time
}

func newMemoryRefreshStore() *memoryRefreshStore {
	return &memoryRefreshStore{tokens: map[string]memoryEntry{}}
}

func (m *memoryRefreshStore) Save(_ context.Context, token string, userID uint, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[token] = memoryEntry{userID: userID, expire: time.Now().Add(ttl)}
	return nil
}

func (m *memoryRefreshStore) Consume(_ context.Context, token string) (uint, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.tokens[token]
	if ok {
		delete(m.tokens, token)
	}
	if !ok || time.Now().After(e.expire) {
		return 0, false, nil
	}
	return e.userID, true, nil
}

func (m *memoryRefreshStore) Revoke(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.tokens, token)
	return nil
}

// --- Redis 实现 ---

const redisKeyPrefix = "custos:refresh:"

type redisRefreshStore struct {
	client *redis.Client
}

func (r *redisRefreshStore) Save(ctx context.Context, token string, userID uint, ttl time.Duration) error {
	return r.client.Set(ctx, redisKeyPrefix+token, userID, ttl).Err()
}

func (r *redisRefreshStore) Consume(ctx context.Context, token string) (uint, bool, error) {
	v, err := r.client.GetDel(ctx, redisKeyPrefix+token).Result()
	if err == redis.Nil {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	var uid uint
	if _, err := fmt.Sscanf(v, "%d", &uid); err != nil {
		return 0, false, fmt.Errorf("refresh token 记录损坏: %w", err)
	}
	return uid, true, nil
}

func (r *redisRefreshStore) Revoke(ctx context.Context, token string) error {
	return r.client.Del(ctx, redisKeyPrefix+token).Err()
}

// RedisConfig Redis 连接参数（setup 向导写入 settings 表，env 可兜底）。
type RedisConfig struct {
	Addr     string `json:"addr"`
	Password string `json:"password"`
	DB       int    `json:"db"`
}

// refreshStoreHolder 按当前配置惰性构建 store；配置变化（setup 修改）后重建。
type refreshStoreHolder struct {
	mu       sync.Mutex
	store    RefreshStore
	buildFor string // 已构建的 addr 标识
}

func newRefreshStoreHolder() *refreshStoreHolder {
	return &refreshStoreHolder{store: newMemoryRefreshStore()}
}

// Get 返回当前应使用的 store；redis addr 非空则用 Redis（并 PING 验证），否则内存。
func (h *refreshStoreHolder) Get(ctx context.Context, cfg *RedisConfig) (RefreshStore, error) {
	addr := ""
	if cfg != nil {
		addr = cfg.Addr
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if addr == "" {
		// 回退内存模式（同时释放旧 redis 连接）
		if _, isRedis := h.store.(*redisRefreshStore); isRedis {
			h.store = newMemoryRefreshStore()
			h.buildFor = ""
		}
		return h.store, nil
	}
	if h.buildFor == addr && h.store != nil {
		return h.store, nil
	}
	client := redis.NewClient(&redis.Options{Addr: addr, Password: cfg.Password, DB: cfg.DB})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("Redis 连接失败（%s）: %w", addr, err)
	}
	h.store = &redisRefreshStore{client: client}
	h.buildFor = addr
	return h.store, nil
}
