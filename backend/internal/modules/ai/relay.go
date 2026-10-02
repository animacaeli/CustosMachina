package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

// AI 中转层（P5 M6，roadmap AI 铁律第一条：后端中转）：
// OpenAI 兼容协议（自托管小模型与云 API 通吃）；endpoint/model/key 可配（key AES）；
// 用量记录 + 审计。本阶段仅请求-响应式（advisory 摘要）；SSE 流式属 P6 对话 UI。

const (
	settingEndpoint = "ai.endpoint" // 如 https://api.deepseek.com 或 http://ollama:11434/v1
	settingAPIKey   = "ai.api_key"  // AES
	settingModel    = "ai.model"
)

// Usage 用量记录（审计与成本追踪）。
type Usage struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	Caller    string    `gorm:"size:32;index" json:"caller"` // 场景：alert_digest / test ...
	Model     string    `gorm:"size:64" json:"model"`
	PromptCh  int       `json:"promptChars"` // 中转层不重复计数 token，按字符近似留痕
	OutputCh  int       `json:"outputChars"`
	LatencyMs int64     `json:"latencyMs"`
	OK        bool      `json:"ok"`
	Error     string    `gorm:"size:512" json:"error"`
	CreatedAt time.Time `json:"createdAt"`
}

func (Usage) TableName() string { return "ai_usages" }

func Models() []any { return []any{&Usage{}} }

type Service struct {
	db     *gorm.DB
	cipher *cryptopkg.Cipher
}

func NewService(db *gorm.DB, cipher *cryptopkg.Cipher) *Service {
	return &Service{db: db, cipher: cipher}
}

// Configured 中转层是否可用（三个配置齐备）。
func (s *Service) Configured(ctx context.Context) bool {
	_, ok := s.config(ctx)
	return ok
}

type relayConfig struct {
	Endpoint string
	APIKey   string
	Model    string
}

func (s *Service) config(ctx context.Context) (*relayConfig, bool) {
	ep, _ := s.setting(ctx, settingEndpoint)
	model, _ := s.setting(ctx, settingModel)
	if ep == "" || model == "" {
		return nil, false
	}
	cfg := &relayConfig{Endpoint: ep, Model: model}
	if enc, _ := s.setting(ctx, settingAPIKey); enc != "" && s.cipher != nil {
		if k, err := s.cipher.Decrypt(enc); err == nil {
			cfg.APIKey = k
		}
	}
	return cfg, true
}

// SaveSettings 保存中转层配置（key 留空保留）。
func (s *Service) SaveSettings(ctx context.Context, endpoint, model, apiKey string) error {
	if endpoint != "" && !strings.HasPrefix(endpoint, "http") {
		return fmt.Errorf("endpoint 须为 http(s):// 地址")
	}
	set := func(k, v string) error {
		return s.db.WithContext(ctx).Exec(
			`INSERT INTO platform_settings (key, value) VALUES (?, ?)
			 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v).Error
	}
	if endpoint != "" {
		if err := set(settingEndpoint, strings.TrimRight(endpoint, "/")); err != nil {
			return err
		}
	}
	if model != "" {
		if err := set(settingModel, model); err != nil {
			return err
		}
	}
	if apiKey != "" {
		if s.cipher == nil {
			return cryptopkg.ErrNoMasterKey
		}
		enc, err := s.cipher.Encrypt(apiKey)
		if err != nil {
			return err
		}
		if err := set(settingAPIKey, enc); err != nil {
			return err
		}
	}
	return nil
}

type SettingsOut struct {
	Configured bool   `json:"configured"`
	Endpoint   string `json:"endpoint"`
	Model      string `json:"model"`
}

func (s *Service) Settings(ctx context.Context) SettingsOut {
	cfg, ok := s.config(ctx)
	if !ok {
		ep, _ := s.setting(ctx, settingEndpoint)
		return SettingsOut{Endpoint: ep}
	}
	return SettingsOut{Configured: true, Endpoint: cfg.Endpoint, Model: cfg.Model}
}

// Message OpenAI 兼容消息。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Complete 调中转层生成一次补全（caller 记用量）。失败返回错误（调用方自行降级）。
func (s *Service) Complete(ctx context.Context, caller string, messages []Message, maxTokens int) (string, error) {
	cfg, ok := s.config(ctx)
	if !ok {
		return "", fmt.Errorf("AI 中转层未配置")
	}
	start := time.Now()
	out, err := s.doComplete(ctx, cfg, messages, maxTokens)
	u := Usage{Caller: caller, Model: cfg.Model,
		PromptCh: promptChars(messages), OutputCh: len(out),
		LatencyMs: time.Since(start).Milliseconds(), OK: err == nil}
	if err != nil {
		u.Error = truncStr(err.Error(), 500)
	}
	s.db.WithContext(ctx).Create(&u) // 用量留痕 best-effort
	return out, err
}

func (s *Service) doComplete(ctx context.Context, cfg *relayConfig, messages []Message, maxTokens int) (string, error) {
	body := map[string]any{
		"model":    cfg.Model,
		"messages": messages,
	}
	if maxTokens > 0 {
		body["max_tokens"] = maxTokens
	}
	b, _ := json.Marshal(body)
	url := strings.TrimRight(cfg.Endpoint, "/") + "/chat/completions"
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("中转层 HTTP %d: %.300s", resp.StatusCode, raw)
	}
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return "", fmt.Errorf("解析中转层响应失败: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("中转层无 choices 返回")
	}
	return parsed.Choices[0].Message.Content, nil
}

// Test 配置连通性测试（管理后台用）。
func (s *Service) Test(ctx context.Context) error {
	out, err := s.Complete(ctx, "test", []Message{
		{Role: "user", Content: "回复两个字：正常"},
	}, 16)
	if err != nil {
		return err
	}
	logger.Infof("[ai] 连通性测试成功: %.60s", out)
	return nil
}

func (s *Service) setting(ctx context.Context, key string) (string, error) {
	var row struct{ Value string }
	err := s.db.WithContext(ctx).Table("platform_settings").
		Select("value").Where("key = ?", key).First(&row).Error
	if err != nil {
		return "", err
	}
	return row.Value, nil
}

func promptChars(ms []Message) int {
	n := 0
	for _, m := range ms {
		n += len(m.Content)
	}
	return n
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
