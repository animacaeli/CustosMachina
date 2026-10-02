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
// 注意：官方不存在 wwlogin/sso/qrConnect 路径（404 页面不存在）；
// 内嵌面板官方走 @wecom/jssdk createWWLoginPanel，此处用 sso/login 整页版
// 由前端 iframe 加载，扫码确认后 iframe 内 302 回 redirect_uri，同样免二次扫码。
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

// sso/login 授权页：前端 iframe 加载，用户用企微扫页内二维码后
// 直接进入确认页（iframe 随后被 302 到 redirect_uri），避免"扫码打开网页、
// 网页里再显示一个二维码"的二次扫码问题。
// sso/login 是同一授权流程的整页版本，供独立浏览器窗口打开时使用。
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

// LoginPanel 前端 @wecom/jssdk createWWLoginPanel 入参（扫码直达确认页，
// 免二次扫码——sso/login 整页版扫码后会先落到网页授权页）。
func (w *WeComProvider) LoginPanel(redirectURI, state string) (*LoginPanelParams, error) {
	if w.cfg.CorpID == "" || w.cfg.AgentID == "" {
		return nil, errors.New("企微配置不完整（缺少 corpid / agentid）")
	}
	return &LoginPanelParams{
		WwLoginType: "CorpApp",
		AppID:       w.cfg.CorpID,
		AgentID:     w.cfg.AgentID,
		RedirectURI: redirectURI,
		State:       state,
	}, nil
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
	return &IMUser{IMUserID: resp.UserID, IMName: w.userName(ctx, token, resp.UserID)}, nil
}

// userName 拉成员姓名（中文显示名）作为平台侧显示名。
// 需应用有通讯录读取权限；无权限或用户不在可见范围时降级返回 userid，
// 不阻塞登录。60xxx 之外常见 48002/60111 均属"拿不到详情"，静默降级。
func (w *WeComProvider) userName(ctx context.Context, token, userid string) string {
	var resp struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
		Name    string `json:"name"`
	}
	if err := wecomGet(ctx, "https://qyapi.weixin.qq.com/cgi-bin/user/get",
		map[string]string{"access_token": token, "userid": userid}, &resp); err != nil {
		return ""
	}
	if resp.ErrCode != 0 || resp.Name == "" {
		return ""
	}
	return resp.Name
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
