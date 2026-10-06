package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/modules/identity"

	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

// AI 中转层（P5 M6，roadmap AI 铁律第一条：后端中转）：
// OpenAI 兼容协议（自托管小模型与云 API 通吃）；endpoint/model/key 可配（key AES）；
// 用量记录 + 审计。P6-M1 起补 CompleteStream（SSE 流式，OpenAI stream 语义）。

const (
	settingEndpoint = "ai.endpoint" // 如 https://api.deepseek.com 或 http://ollama:11434/v1
	settingAPIKey   = "ai.api_key"  // AES
	settingModel    = "ai.model"
	settingWindow   = "ai.context_window" // token 数（P7-M4 自动压缩阈值；空=默认 32768）
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

// validateEndpoint 中转层端点校验：url.Parse 结构校验（拒绝 httpfoo:// 这类
// 前缀绕过）+ 禁私网/回环/链路本地地址（防 SSRF：请求会把解密后的 LLM API Key
// 作为鉴权头发往该地址，v0.12.0 审计中等项）。自建内网 LLM 场景可用
// CUSTOS_AI_ALLOW_PRIVATE_ENDPOINT=1 显式豁免。
func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("endpoint 不是合法 URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("endpoint 须为 http(s):// 地址")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("endpoint 缺少主机名")
	}
	if os.Getenv("CUSTOS_AI_ALLOW_PRIVATE_ENDPOINT") == "1" {
		return nil
	}
	block := func(ips []net.IP) error {
		for _, ip := range ips {
			if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
				return fmt.Errorf("endpoint 指向内网/回环地址 %s（自建内网 LLM 可设 CUSTOS_AI_ALLOW_PRIVATE_ENDPOINT=1 豁免）", ip)
			}
		}
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		return block([]net.IP{ip})
	}
	if host == "localhost" {
		return fmt.Errorf("endpoint 指向 localhost（自建内网 LLM 可设 CUSTOS_AI_ALLOW_PRIVATE_ENDPOINT=1 豁免）")
	}
	// 域名场景：解析后逐个检查（防域名指向内网的绕过）
	if ips, err := net.LookupIP(host); err == nil {
		return block(ips)
	}
	// 解析失败留给请求时报错（校验层不因临时 DNS 故障拒绝保存）
	return nil
}

// SaveSettings 保存中转层配置（key 留空保留）。
func (s *Service) SaveSettings(ctx context.Context, endpoint, model, apiKey string) error {
	if endpoint != "" {
		if err := validateEndpoint(endpoint); err != nil {
			return err
		}
	}
	set := func(k, v string) error {
		return identity.UpsertSetting(s.db, ctx, k, v)
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
	// ContextWindow 上下文窗口（token；P7-M4，0 = 默认 32768）
	ContextWindow int `json:"contextWindow"`
}

func (s *Service) Settings(ctx context.Context) SettingsOut {
	cfg, ok := s.config(ctx)
	if !ok {
		ep, _ := s.setting(ctx, settingEndpoint)
		return SettingsOut{Endpoint: ep, ContextWindow: s.ContextWindow(ctx)}
	}
	return SettingsOut{Configured: true, Endpoint: cfg.Endpoint, Model: cfg.Model,
		ContextWindow: s.ContextWindow(ctx)}
}

// ContextWindow 模型上下文窗口（token 近似；未配置返回 0 由调用方兜底默认）。
func (s *Service) ContextWindow(ctx context.Context) int {
	v, _ := s.setting(ctx, settingWindow)
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// SaveContextWindow 窗口设置（0/空 = 恢复默认）。
func (s *Service) SaveContextWindow(ctx context.Context, window int) error {
	v := ""
	if window > 0 {
		v = fmt.Sprintf("%d", window)
	}
	return identity.UpsertSetting(s.db, ctx, settingWindow, v)
}

// Message OpenAI 兼容消息。Content 为 any：普通对话传 string；
// 多模态（P6-M1 增强）传 OpenAI 数组格式（[{type:text},{type:image_url}]），
// 序列化后即为上游所需结构，中转层不感知具体模态。
// P6-M3 工具调用：assistant 消息可带 ToolCalls（发起调用），tool 消息带
// ToolCallID 回填结果。
type Message struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall 一次工具调用（流式增量聚合后的完整形态）。
type ToolCall struct {
	ID       string     `json:"id"`
	Type     string     `json:"type"` // "function"
	Function ToolCallFn `json:"function"`
}

type ToolCallFn struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON 字符串
}

// ToolDef 对话内工具定义（OpenAI tools 数组项；Parameters 为 JSON Schema）。
type ToolDef struct {
	Type     string    `json:"type"` // "function"
	Function ToolDefFn `json:"function"`
}

type ToolDefFn struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
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
	resp, err := relayHTTPClient.Do(req)
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

// CompleteStream 流式补全（M1 对话）：OpenAI 兼容 stream 语义，每个增量回调
// onDelta；返回聚合完整回复与模型发起的工具调用（有则非空，调用方执行后以
// tool 消息回填再调一轮）；ctx 取消时聚合的部分内容仍随 err 返回——调用方
// 落库用。治理设计见 docs/design-chat-sse.md。
func (s *Service) CompleteStream(ctx context.Context, caller string, messages []Message, maxTokens int, tools []ToolDef, onDelta func(string)) (string, []ToolCall, error) {
	cfg, ok := s.config(ctx)
	if !ok {
		return "", nil, fmt.Errorf("AI 中转层未配置")
	}
	start := time.Now()
	out, calls, err := s.doCompleteStream(ctx, cfg, messages, maxTokens, tools, onDelta)
	canceled := errors.Is(err, context.Canceled)
	u := Usage{Caller: caller, Model: cfg.Model,
		PromptCh: promptChars(messages), OutputCh: len(out),
		LatencyMs: time.Since(start).Milliseconds(), OK: err == nil || canceled}
	if err != nil && !canceled {
		u.Error = truncStr(err.Error(), 500)
	}
	// 用量留痕不受客户端断连影响（best-effort）
	s.db.WithContext(context.WithoutCancel(ctx)).Create(&u)
	return out, calls, err
}

func (s *Service) doCompleteStream(ctx context.Context, cfg *relayConfig, messages []Message, maxTokens int, tools []ToolDef, onDelta func(string)) (string, []ToolCall, error) {
	body := map[string]any{
		"model":    cfg.Model,
		"messages": messages,
		"stream":   true,
	}
	if maxTokens > 0 {
		body["max_tokens"] = maxTokens
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	b, _ := json.Marshal(body)
	url := strings.TrimRight(cfg.Endpoint, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	// 流式回答无固定时长，整体 deadline 由调用方（chat 5min）控制；空闲超时用
	// 看门狗 goroutine cancel 内部 ctx——Scanner 阻塞在读上，select 检查不了时钟。
	// defer 为 LIFO：注册顺序必须保证 cancel 先于 <-watchdogDone 执行，
	// 否则正常返回也要干等看门狗的 60s 空闲超时（单测实测复现过）
	watchdogDone := make(chan struct{})
	defer func() { <-watchdogDone }() // 最后执行：收尸等待
	cctx, cancel := context.WithCancel(ctx)
	defer cancel() // 先于上一条执行：触发看门狗退出
	req = req.WithContext(cctx)
	var lastDelta atomic.Int64
	lastDelta.Store(time.Now().Unix())
	go func() {
		defer close(watchdogDone)
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-cctx.Done():
				return
			case <-t.C:
				if time.Since(time.Unix(lastDelta.Load(), 0)) > 60*time.Second {
					cancel() // 解除 Scanner 的阻塞读
					return
				}
			}
		}
	}()
	resp, err := relayHTTPClient.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", nil, fmt.Errorf("中转层 HTTP %d: %.300s", resp.StatusCode, raw)
	}

	var aggregated strings.Builder
	// tool_calls 流式增量：按 index 聚合（id/name 首块到达，arguments 分片追加）
	type callAcc struct{ c ToolCall }
	calls := map[int]*callAcc{}
	maxIdx := -1
	collectCalls := func() []ToolCall {
		if len(calls) == 0 {
			return nil
		}
		out := make([]ToolCall, 0, len(calls))
		for i := 0; i <= maxIdx; i++ {
			if acc, ok := calls[i]; ok {
				out = append(out, acc.c)
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for {
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				// 读被 cancel 打断：区分父 ctx（客户端断开/整体超时）与看门狗（空闲超时）
				if ctx.Err() != nil {
					return aggregated.String(), collectCalls(), ctx.Err()
				}
				if cctx.Err() != nil {
					return aggregated.String(), collectCalls(), errors.New("中转层空闲超时（60s 无增量）")
				}
				return aggregated.String(), collectCalls(), err
			}
			return aggregated.String(), collectCalls(), nil // 流正常关闭
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, ":") { // 空行/SSE 注释（上游保活）
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			return aggregated.String(), collectCalls(), nil
		}
		var chunk struct {
			Choices []struct {
				FinishReason *string `json:"finish_reason"`
				Delta        struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue // 心跳/非标准块，跳过
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		if ch.Delta.Content != "" {
			lastDelta.Store(time.Now().Unix())
			aggregated.WriteString(ch.Delta.Content)
			if onDelta != nil {
				onDelta(ch.Delta.Content)
			}
		}
		for _, tc := range ch.Delta.ToolCalls {
			lastDelta.Store(time.Now().Unix())
			if tc.Index > maxIdx {
				maxIdx = tc.Index
			}
			acc, ok := calls[tc.Index]
			if !ok {
				acc = &callAcc{}
				calls[tc.Index] = acc
				acc.c.Type = "function"
			}
			if tc.ID != "" {
				acc.c.ID = tc.ID
			}
			if tc.Type != "" {
				acc.c.Type = tc.Type
			}
			if tc.Function.Name != "" {
				acc.c.Function.Name += tc.Function.Name // 少数上游分片发 name
			}
			acc.c.Function.Arguments += tc.Function.Arguments
		}
	}
}

func (s *Service) setting(ctx context.Context, key string) (string, error) {
	var row struct{ Value string }
	err := s.db.WithContext(ctx).Table("platform_settings").
		Select("value").Where("skey = ?", key).Order("skey").First(&row).Error
	if err != nil {
		return "", err
	}
	return row.Value, nil
}

func promptChars(ms []Message) int {
	n := 0
	for _, m := range ms {
		switch v := m.Content.(type) {
		case string:
			n += len(v)
		case []any: // 多模态：按各 part 文本粗估
			for _, p := range v {
				if pm, ok := p.(map[string]any); ok {
					if t, ok := pm["text"].(string); ok {
						n += len(t)
					} else {
						n += 1024 // 图片等非文本 part 按固定量计
					}
				}
			}
		default:
			n += 256
		}
	}
	return n
}

func truncStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// relayHTTPClient 中转层专用 client：连接/TLS/响应头超时齐备但不设整体
// Timeout（SSE 流式对话长连接会超过任何整体上限；空闲断流由调用侧看门狗处理）。
// 替换 http.DefaultClient（无任何超时，挂起即 goroutine 泄漏，v0.12.0 审计中等项）。
var relayHTTPClient = &http.Client{
	Transport: &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
	},
}
