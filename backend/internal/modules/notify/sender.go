package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// 群机器人限频按群统一闸门（企微 20 条/分钟最严，其他厂商同样安全）。
const (
	minSendInterval = 3 * time.Second
	maxSendWait     = 30 * time.Second
)

type provider string

const (
	provWecom    provider = "wecom"
	provDingtalk provider = "dingtalk"
	provFeishu   provider = "feishu"
	provUnknown  provider = "unknown"
)

// detectProvider 按 webhook 主机名识别企业 IM 厂商——各家群机器人的消息格式不同：
//   - 企微 qyapi.weixin.qq.com：markdown
//   - 钉钉 oapi.dingtalk.com：markdown（若机器人开了加签需在 URL 里带 sign，由用户自行拼好）
//   - 飞书 open.feishu.cn：text（自定义机器人 markdown 需走卡片，text 覆盖通用场景）
//
// 用 url.Parse 比对 Host，而非子串包含——Contains 会被查询参数伪造
// （https://evil.com/?x=qyapi.weixin.qq.com 会被误判，v0.12.0 审计中等项）。
func detectProvider(webhook string) provider {
	u, err := url.Parse(webhook)
	if err != nil {
		return provUnknown
	}
	switch u.Hostname() {
	case "qyapi.weixin.qq.com":
		return provWecom
	case "oapi.dingtalk.com":
		return provDingtalk
	case "open.feishu.cn":
		return provFeishu
	default:
		return provUnknown
	}
}

type sender struct {
	client *http.Client

	mu   sync.Mutex
	last map[uint]time.Time // groupID → 上次发送时间
}

func newSender() *sender {
	return &sender{
		client: &http.Client{Timeout: 10 * time.Second},
		last:   map[uint]time.Time{},
	}
}

// Send 按厂商格式推送（title/content 为通用语义）。
func (s *sender) Send(ctx context.Context, groupID uint, webhook, title, content string) error {
	if err := s.acquire(groupID); err != nil {
		return err
	}
	text := "**" + title + "**\n" + content
	if len(text) > 4000 {
		text = text[:4000]
	}
	var payload []byte
	switch detectProvider(webhook) {
	case provWecom:
		payload, _ = json.Marshal(map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]string{"content": text},
		})
	case provDingtalk:
		payload, _ = json.Marshal(map[string]any{
			"msgtype":  "markdown",
			"markdown": map[string]string{"title": title, "text": text},
		})
	case provFeishu:
		payload, _ = json.Marshal(map[string]any{
			"msg_type": "text",
			"content":  map[string]string{"text": title + "\n" + content},
		})
	default:
		return fmt.Errorf("无法识别 webhook 所属 IM（支持企微/钉钉/飞书）")
	}
	return s.post(ctx, groupID, webhook, payload)
}

func newJSONRequest(ctx context.Context, url string, payload []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	return req, nil
}

func (s *sender) post(ctx context.Context, groupID uint, webhook string, payload []byte) error {
	req, err := newJSONRequest(ctx, webhook, payload)
	if err != nil {
		return err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webhook 返回 %d", resp.StatusCode)
	}
	var out struct {
		// 企微 errcode/errmsg；钉钉 errcode/errmsg；飞书 code/msg
		ErrCode int    `json:"errcode"`
		Code    int    `json:"code"`
		ErrMsg  string `json:"errmsg"`
		Msg     string `json:"msg"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return err
	}
	if out.ErrCode != 0 {
		return fmt.Errorf("IM errcode=%d: %s", out.ErrCode, out.ErrMsg)
	}
	if out.Code != 0 {
		return fmt.Errorf("IM code=%d: %s", out.Code, out.Msg)
	}
	return nil
}

// acquire 按群限频；拿不到配额直接返回错误（调用方记 failed）。
func (s *sender) acquire(groupID uint) error {
	deadline := time.Now().Add(maxSendWait)
	for {
		s.mu.Lock()
		next := s.last[groupID].Add(minSendInterval)
		now := time.Now()
		if now.After(next) {
			s.last[groupID] = now
			s.mu.Unlock()
			return nil
		}
		s.mu.Unlock()
		if time.Now().After(deadline) {
			return fmt.Errorf("群 %d 限频等待超时（20 条/分钟）", groupID)
		}
		time.Sleep(time.Until(next))
	}
}
