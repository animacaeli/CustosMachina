package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/custos-machina/backend/internal/modules/identity"
	cryptopkg "github.com/custos-machina/backend/internal/pkg/crypto"
)

const qrStateTTL = 5 * time.Minute

// activeProvider 返回当前激活的 IM 插件实例（凭证已注入）。
// 选择规则：env 指定 mock（本地联调）→ 否则取 im_provider_configs 中唯一启用行；
// 多行启用时按 provider 名字母序取第一（配置页应保证只启用一家，FR1.4）。
func (s *AuthService) activeProvider(ctx context.Context) (IdentityProvider, error) {
	if s.cfg.IM.Provider == "mock" {
		return NewProvider("mock")
	}
	rows, err := s.bindings.ListProviderConfigs(ctx)
	if err != nil {
		return nil, err
	}
	var stored *identity.IMProviderConfig
	for i := range rows {
		if rows[i].Enabled {
			stored = &rows[i]
			break
		}
	}
	if stored == nil {
		return nil, ErrProviderNotConfigured
	}
	return s.buildProvider(stored)
}

// buildProvider 从加密配置行构建插件实例。
func (s *AuthService) buildProvider(stored *identity.IMProviderConfig) (IdentityProvider, error) {
	p, err := NewProvider(stored.Provider)
	if err != nil {
		return nil, err
	}
	plain, err := s.cipher.Decrypt(stored.CredentialsEncrypted)
	if err != nil {
		return nil, fmt.Errorf("解密 %s 凭证失败: %w", stored.Provider, err)
	}
	switch v := p.(type) {
	case *WeComProvider:
		var c WeComConfig
		if err := json.Unmarshal([]byte(plain), &c); err != nil {
			return nil, fmt.Errorf("企微凭证格式错误: %w", err)
		}
		v.Configure(c)
	case *DingTalkProvider:
		var c DingTalkConfig
		if err := json.Unmarshal([]byte(plain), &c); err != nil {
			return nil, fmt.Errorf("钉钉凭证格式错误: %w", err)
		}
		v.Configure(c)
	case *FeishuProvider:
		var c FeishuConfig
		if err := json.Unmarshal([]byte(plain), &c); err != nil {
			return nil, fmt.Errorf("飞书凭证格式错误: %w", err)
		}
		v.Configure(c)
	}
	return p, nil
}

// SaveIMProviderConfig 通用：按 provider 存 JSON 配置（加密）并设启用位。
// 启用某家时其余家自动禁用（单选语义）。
func (s *AuthService) SaveIMProviderConfig(ctx context.Context, provider, cfgJSON string, enabled bool) error {
	if _, err := NewProvider(provider); err != nil {
		return err
	}
	if s.cipher == nil {
		return cryptopkg.ErrNoMasterKey
	}
	enc, err := s.cipher.Encrypt(cfgJSON)
	if err != nil {
		return err
	}
	if enabled {
		// 关掉其他家
		rows, _ := s.bindings.ListProviderConfigs(ctx)
		for _, r := range rows {
			if r.Provider != provider && r.Enabled {
				r.Enabled = false
				_ = s.bindings.SaveProviderConfig(ctx, &r)
			}
		}
	}
	return s.bindings.SaveProviderConfig(ctx, &identity.IMProviderConfig{
		Provider: provider, CredentialsEncrypted: enc, Enabled: enabled,
	})
}

// IMProviderStatus 所有提供商配置状态（配置页/setup 用；不含密文）。
func (s *AuthService) IMProviderStatus(ctx context.Context) []map[string]any {
	rows, _ := s.bindings.ListProviderConfigs(ctx)
	byName := map[string]identity.IMProviderConfig{}
	for _, r := range rows {
		byName[r.Provider] = r
	}
	out := []map[string]any{}
	for _, name := range ProviderNames() {
		if name == "mock" {
			continue
		}
		entry := map[string]any{"provider": name, "configured": false, "enabled": false}
		if r, ok := byName[name]; ok {
			entry["configured"] = true
			entry["enabled"] = r.Enabled
		}
		out = append(out, entry)
	}
	return out
}

// redirectURI 企微回调地址：{PublicURL}/api/auth/qrlogin/callback。
func (s *AuthService) redirectURI() (string, error) {
	if s.cfg.IM.PublicURL == "" {
		return "", errors.New("未配置 CUSTOS_IM_PUBLIC_URL（企微回调需公网 HTTPS 地址）")
	}
	return strings.TrimRight(s.cfg.IM.PublicURL, "/") + "/api/auth/qrlogin/callback", nil
}

// QRLoginURL 生成扫码授权页地址 + 一次性 state。
func (s *AuthService) QRLoginURL(ctx context.Context) (string, error) {
	p, err := s.activeProvider(ctx)
	if err != nil {
		return "", err
	}
	uri, err := s.redirectURI()
	if err != nil {
		return "", err
	}
	state := make([]byte, 16)
	if _, err := rand.Read(state); err != nil {
		return "", err
	}
	stateStr := hex.EncodeToString(state)
	s.qrStates.Store(stateStr, time.Now().Add(qrStateTTL))
	return p.AuthorizeURL(uri, stateStr)
}

func (s *AuthService) consumeState(state string) bool {
	v, ok := s.qrStates.LoadAndDelete(state)
	if !ok {
		return false
	}
	exp, _ := v.(time.Time)
	return time.Now().Before(exp)
}

// HandleQRCallback 扫码回调：code 换 IM 身份 → 绑定查找 →（无则 JIT 注册 guest）→ 签发 token。
func (s *AuthService) HandleQRCallback(ctx context.Context, code, state string) (*LoginResult, error) {
	if !s.consumeState(state) {
		return nil, errors.New("state 无效或已过期，请刷新二维码")
	}
	p, err := s.activeProvider(ctx)
	if err != nil {
		return nil, err
	}
	imUser, err := p.ExchangeCode(ctx, code)
	if err != nil {
		return nil, err
	}

	binding, err := s.bindings.GetBinding(ctx, p.Name(), imUser.IMUserID)
	var u *identity.User
	if err == nil {
		u, err = s.users.GetByID(ctx, binding.UserID)
		if err != nil {
			return nil, fmt.Errorf("绑定的用户不存在: %w", err)
		}
	} else {
		// JIT 注册（FR2.2）：首次扫码自动建用户，默认 guest
		display := imUser.IMName
		if display == "" {
			display = imUser.IMUserID
		}
		u = &identity.User{DisplayName: display, Roles: "guest"}
		if err := s.users.Create(ctx, u); err != nil {
			return nil, fmt.Errorf("JIT 注册失败: %w", err)
		}
		if err := s.bindings.SaveBinding(ctx, &identity.UserIMBinding{
			UserID: u.ID, Provider: p.Name(), IMUserID: imUser.IMUserID, IMName: imUser.IMName,
		}); err != nil {
			return nil, fmt.Errorf("保存 IM 绑定失败: %w", err)
		}
	}
	if u.Status == identity.StatusDisabled {
		return nil, ErrUserDisabled
	}
	return s.IssueTokenPair(ctx, u)
}

// FrontendCallbackURL 回调成功后带双 token 重定向回前端扫码页。
// refresh 放 URL fragment（# 后），不进服务器日志与 Referer。
func (s *AuthService) FrontendCallbackURL(accessToken, refreshToken string) string {
	base := strings.TrimRight(s.cfg.IM.FrontendURL, "/") + "/auth/qrcode-login"
	u, _ := url.Parse(base)
	q := u.Query()
	q.Set("token", accessToken)
	u.RawQuery = q.Encode()
	return u.String() + "#refresh=" + refreshToken
}

// --- 提供商配置管理（admin / setup）---

// VerifyProviderConfig 验证给定凭证 JSON（保存前测试）或已存凭证。
func (s *AuthService) VerifyProviderConfig(ctx context.Context, provider, cfgJSON string) error {
	var p IdentityProvider
	var err error
	if cfgJSON != "" {
		p, err = NewProvider(provider)
		if err != nil {
			return err
		}
		switch v := p.(type) {
		case *WeComProvider:
			c := WeComConfig{}
			_ = json.Unmarshal([]byte(cfgJSON), &c)
			v.Configure(c)
		case *DingTalkProvider:
			c := DingTalkConfig{}
			_ = json.Unmarshal([]byte(cfgJSON), &c)
			v.Configure(c)
		case *FeishuProvider:
			c := FeishuConfig{}
			_ = json.Unmarshal([]byte(cfgJSON), &c)
			v.Configure(c)
		}
	} else {
		stored, err2 := s.bindings.GetProviderConfig(ctx, provider)
		if err2 != nil || !stored.Enabled {
			return ErrProviderNotConfigured
		}
		p, err = s.buildProvider(stored)
		if err != nil {
			return err
		}
	}
	return p.Verify(ctx)
}
