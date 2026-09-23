package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// 企微群机器人限频：每个机器人每分钟最多 20 条。
// 这里按群做最小间隔闸门（60s/20 = 3s），超时未获得配额则放弃并记日志（事件通知可丢，不阻塞调用方）。
const (
	wecomMinInterval = 3 * time.Second
	wecomMaxWait     = 30 * time.Second
)

type wecomSender struct {
	client *http.Client

	mu   sync.Mutex
	last map[uint]time.Time // groupID → 上次发送时间
}

func newWecomSender() *wecomSender {
	return &wecomSender{
		client: &http.Client{Timeout: 10 * time.Second},
		last:   map[uint]time.Time{},
	}
}

// sendMarkdown 推送 markdown 消息到企微群机器人 webhook。
func (s *wecomSender) sendMarkdown(ctx context.Context, groupID uint, webhook, text string) error {
	if err := s.acquire(groupID); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"msgtype":  "markdown",
		"markdown": map[string]string{"content": text},
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhook, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("企微 webhook 返回 %d", resp.StatusCode)
	}
	var out struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if out.ErrCode != 0 {
		return fmt.Errorf("企微 errcode=%d: %s", out.ErrCode, out.ErrMsg)
	}
	return nil
}

// acquire 按群限频；拿不到配额直接返回错误（调用方记 failed）。
func (s *wecomSender) acquire(groupID uint) error {
	deadline := time.Now().Add(wecomMaxWait)
	for {
		s.mu.Lock()
		next := s.last[groupID].Add(wecomMinInterval)
		now := time.Now()
		if now.After(next) {
			s.last[groupID] = now
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()
		if time.Now().After(deadline) {
			return fmt.Errorf("群 %d 限频等待超时（企微 20 条/分钟）", groupID)
		}
		time.Sleep(time.Until(next))
	}
}
