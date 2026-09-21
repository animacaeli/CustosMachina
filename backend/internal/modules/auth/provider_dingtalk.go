package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// 钉钉扫码登录（OAuth2，https://open.dingtalk.com/document/org-guide/obtain-org-user-personal-information）。
// 授权页 → code 换 userAccessToken → 拉取用户信息（unionId 作为 IM 用户标识）。
func init() { registerProvider(func() IdentityProvider { return &DingTalkProvider{} }) }

// DingTalkConfig 钉钉凭证（AES 加密落库 JSON）。
type DingTalkConfig struct {
	ClientKey    string `json:"clientKey"`    // 应用的 AppKey / ClientId
	ClientSecret string `json:"clientSecret"` // AppSecret
}

type DingTalkProvider struct{ cfg DingTalkConfig }

func (d *DingTalkProvider) Name() string { return "dingtalk" }

func (d *DingTalkProvider) Configure(cfg DingTalkConfig) { d.cfg = cfg }

func (d *DingTalkProvider) AuthorizeURL(redirectURI, state string) (string, error) {
	if d.cfg.ClientKey == "" {
		return "", errors.New("钉钉配置不完整（缺少 ClientKey）")
	}
	q := url.Values{}
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("client_id", d.cfg.ClientKey)
	q.Set("scope", "openid")
	q.Set("state", state)
	q.Set("prompt", "consent")
	return "https://login.dingtalk.com/oauth2/auth?" + q.Encode(), nil
}

func (d *DingTalkProvider) ExchangeCode(ctx context.Context, code string) (*IMUser, error) {
	token, err := d.userAccessToken(ctx, code)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.dingtalk.com/v1.0/contact/users/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var me struct {
		UnionID string `json:"unionId"`
		Nick    string `json:"nick"`
	}
	if err := doJSON(ctx, req, &me); err != nil {
		return nil, err
	}
	if me.UnionID == "" {
		return nil, errors.New("钉钉返回缺少 unionId")
	}
	return &IMUser{IMUserID: me.UnionID, IMName: me.Nick}, nil
}

func (d *DingTalkProvider) Verify(ctx context.Context) error {
	// 无独立健康检查接口：用无效 code 走一次换取流程，凭错误码区分凭证问题
	_, err := d.userAccessToken(ctx, "verify-probe")
	if err == nil {
		return nil
	}
	// 凭证错误（invalid_client）才是配置问题；code 无效说明凭证通过了校验
	var e *apiError
	if errors.As(err, &e) && e.Status != 0 {
		return nil
	}
	return err
}

func (d *DingTalkProvider) userAccessToken(ctx context.Context, code string) (string, error) {
	if d.cfg.ClientKey == "" || d.cfg.ClientSecret == "" {
		return "", errors.New("钉钉配置不完整（缺少 ClientKey / ClientSecret）")
	}
	body, _ := json.Marshal(map[string]string{
		"clientId":     d.cfg.ClientKey,
		"clientSecret": d.cfg.ClientSecret,
		"code":         code,
		"grantType":    "authorization_code",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.dingtalk.com/v1.0/oauth2/userAccessToken", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	var resp struct {
		ExpireIn    int    `json:"expireIn"`
		AccessToken string `json:"accessToken"`
	}
	if err := doJSON(ctx, req, &resp); err != nil {
		return "", err
	}
	if resp.AccessToken == "" {
		return "", errors.New("钉钉未返回 accessToken")
	}
	return resp.AccessToken, nil
}

// apiError / doJSON：三家 provider 共用的 JSON HTTP 小工具。
type apiError struct {
	Status int
	Body   string
}

func (e *apiError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Body) }

func doJSON(ctx context.Context, req *http.Request, out any) error {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("请求 IM 接口失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return &apiError{Status: resp.StatusCode, Body: string(body)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("解析响应失败: %w (%.200s)", err, body)
	}
	return nil
}
