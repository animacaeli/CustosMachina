package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// 飞书网页应用授权登录（https://open.feishu.cn/document/common-capabilities/sso/api/get-user-info）。
// 授权页 → code 换 user_access_token → 获取用户信息（open_id 作为 IM 用户标识）。
func init() { registerProvider(func() IdentityProvider { return &FeishuProvider{} }) }

// FeishuConfig 飞书凭证（AES 加密落库 JSON）。
type FeishuConfig struct {
	AppID     string `json:"appId"`
	AppSecret string `json:"appSecret"`
}

type FeishuProvider struct{ cfg FeishuConfig }

func (f *FeishuProvider) Name() string { return "feishu" }

func (f *FeishuProvider) Configure(cfg FeishuConfig) { f.cfg = cfg }

func (f *FeishuProvider) AuthorizeURL(redirectURI, state string) (string, error) {
	if f.cfg.AppID == "" {
		return "", errors.New("飞书配置不完整（缺少 App ID）")
	}
	q := url.Values{}
	q.Set("client_id", f.cfg.AppID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("state", state)
	return "https://passport.feishu.cn/suite/passport/oauth/authorize?" + q.Encode(), nil
}

func (f *FeishuProvider) ExchangeCode(ctx context.Context, code string) (*IMUser, error) {
	token, err := f.userAccessToken(ctx, code)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://open.feishu.cn/open-apis/authen/v1/user_info", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	var resp struct {
		Code int `json:"code"`
		Data struct {
			OpenID string `json:"open_id"`
			Name   string `json:"name"`
		} `json:"data"`
	}
	if err := doJSON(ctx, req, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 0 || resp.Data.OpenID == "" {
		return nil, fmt.Errorf("飞书获取用户信息失败: code=%d", resp.Code)
	}
	return &IMUser{IMUserID: resp.Data.OpenID, IMName: resp.Data.Name}, nil
}

// Verify 用 app_access_token 接口验证凭证。
func (f *FeishuProvider) Verify(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{
		"app_id":     f.cfg.AppID,
		"app_secret": f.cfg.AppSecret,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://open.feishu.cn/open-apis/auth/v3/app_access_token/internal", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	var resp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := doJSON(ctx, req, &resp); err != nil {
		return err
	}
	if resp.Code != 0 {
		return fmt.Errorf("飞书凭证校验失败: %d %s", resp.Code, resp.Msg)
	}
	return nil
}

func (f *FeishuProvider) userAccessToken(ctx context.Context, code string) (string, error) {
	if f.cfg.AppID == "" || f.cfg.AppSecret == "" {
		return "", errors.New("飞书配置不完整（缺少 App ID / App Secret）")
	}
	body, _ := json.Marshal(map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     f.cfg.AppID,
		"client_secret": f.cfg.AppSecret,
		"code":          code,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://open.feishu.cn/open-apis/authen/v2/oauth/token", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	var resp struct {
		Code            int    `json:"code"`
		AccessTokenUser string `json:"access_token"`
		TokenType       string `json:"token_type"`
	}
	if err := doJSON(ctx, req, &resp); err != nil {
		return "", err
	}
	if resp.AccessTokenUser == "" {
		return "", fmt.Errorf("飞书未返回 user_access_token (code=%d)", resp.Code)
	}
	return resp.AccessTokenUser, nil
}
