package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 企业微信自建应用扫码登录（https://developer.work.weixin.qq.com/document/path/98152）。
// 授权页：https://login.work.weixin.qq.com/wwlogin/sso/login
// login_type 合法值：CorpApp（企业自建/代开发应用，即我们）/ ServiceApp（服务商应用）
// code 换身份：getuserinfo（需应用 access_token，由 corpid+secret 换取，2h 有效缓存）。
func init() { registerProvider(func() IdentityProvider { return &WeComProvider{} }) }

// WeComConfig 企微提供商凭证（AES 加密落库的 JSON 结构）。
type WeComConfig struct {
	CorpID  string `json:"corpId"`
	AgentID string `json:"agentId"`
	Secret  string `json:"secret"`
}

type WeComProvider struct {
	cfg WeComConfig

	mu          sync.Mutex
	accessToken string
	tokenExpire time.Time
}

func (w *WeComProvider) Name() string { return "wecom" }

// Configure 注入解密后的凭证（service 层负责加解密）。
func (w *WeComProvider) Configure(cfg WeComConfig) { w.cfg = cfg }

func (w *WeComProvider) AuthorizeURL(redirectURI, state string) (string, error) {
	if w.cfg.CorpID == "" || w.cfg.AgentID == "" {
		return "", errors.New("企微配置不完整（缺少 corpid / agentid）")
	}
	q := url.Values{}
	q.Set("login_type", "CorpApp")
	q.Set("appid", w.cfg.CorpID)
	q.Set("agentid", w.cfg.AgentID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	return "https://login.work.weixin.qq.com/wwlogin/sso/login?" + q.Encode(), nil
}

// getAccessToken 应用 access_token，进程内缓存至过期前 5 分钟。
func (w *WeComProvider) getAccessToken(ctx context.Context) (string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.accessToken != "" && time.Now().Before(w.tokenExpire) {
		return w.accessToken, nil
	}
	if w.cfg.CorpID == "" || w.cfg.Secret == "" {
		return "", errors.New("企微配置不完整（缺少 corpid / secret）")
	}
	var resp struct {
		ErrCode     int    `json:"errcode"`
		ErrMsg      string `json:"errmsg"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := wecomGet(ctx, "https://qyapi.weixin.qq.com/cgi-bin/gettoken",
		map[string]string{"corpid": w.cfg.CorpID, "corpsecret": w.cfg.Secret}, &resp); err != nil {
		return "", err
	}
	if resp.ErrCode != 0 {
		return "", fmt.Errorf("获取 access_token 失败: %d %s", resp.ErrCode, resp.ErrMsg)
	}
	w.accessToken = resp.AccessToken
	w.tokenExpire = time.Now().Add(time.Duration(resp.ExpiresIn-300) * time.Second)
	return w.accessToken, nil
}

func (w *WeComProvider) ExchangeCode(ctx context.Context, code string) (*IMUser, error) {
	token, err := w.getAccessToken(ctx)
	if err != nil {
		return nil, err
	}
	var resp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		UserID  string `json:"userid"`
	}
	if err := wecomGet(ctx, "https://qyapi.weixin.qq.com/cgi-bin/auth/getuserinfo",
		map[string]string{"access_token": token, "code": code}, &resp); err != nil {
		return nil, err
	}
	if resp.ErrCode != 0 {
		return nil, fmt.Errorf("code 换取用户失败: %d %s", resp.ErrCode, resp.ErrMsg)
	}
	if resp.UserID == "" {
		return nil, errors.New("非企业成员（缺少 userid），无法登录")
	}
	return &IMUser{IMUserID: resp.UserID}, nil
}

// Verify 用 gettoken 验证凭证连通性。
func (w *WeComProvider) Verify(ctx context.Context) error {
	w.mu.Lock()
	w.accessToken = ""
	w.tokenExpire = time.Time{}
	w.mu.Unlock()
	_, err := w.getAccessToken(ctx)
	return err
}

func wecomGet(ctx context.Context, rawURL string, params map[string]string, out any) error {
	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("请求企微接口失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if ct := resp.Header.Get("Content-Type"); strings.Contains(ct, "text/plain") {
		// 企微部分接口返回非 JSON content-type 但 body 是 JSON
		_ = ct
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("解析企微响应失败: %w (body: %.200s)", err, body)
	}
	return nil
}
