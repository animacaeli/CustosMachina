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
// wecom 需要 DB 配置；mock 免配置直接可用（仅限本地联调）。
func (s *AuthService) activeProvider(ctx context.Context) (IdentityProvider, error) {
	name := s.cfg.IM.Provider
	p, err := NewProvider(name)
	if err != nil {
		return nil, err
	}
	if w, ok := p.(*WeComProvider); ok {
		stored, err := s.bindings.GetProviderConfig(ctx, name)
		if err != nil || !stored.Enabled {
			return nil, ErrProviderNotConfigured
		}
		plain, err := s.cipher.Decrypt(stored.CredentialsEncrypted)
		if err != nil {
			return nil, fmt.Errorf("解密企微凭证失败: %w", err)
		}
		var wc WeComConfig
		if err := json.Unmarshal([]byte(plain), &wc); err != nil {
			return nil, fmt.Errorf("企微凭证格式错误: %w", err)
		}
		w.Configure(wc)
	}
	return p, nil
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
	return s.IssueToken(u)
}

// FrontendCallbackURL 回调成功后带 token 重定向回前端扫码页。
func (s *AuthService) FrontendCallbackURL(token string) string {
	u, _ := url.Parse(strings.TrimRight(s.cfg.IM.FrontendURL, "/") + "/auth/qrcode-login")
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()
	return u.String()
}

// --- 提供商配置管理（admin） ---

// SaveWeComConfig 加密落库企微凭证（admin 配置页）。
func (s *AuthService) SaveWeComConfig(ctx context.Context, cfg WeComConfig, enabled bool) error {
	if s.cipher == nil {
		return cryptopkg.ErrNoMasterKey
	}
	plain, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	enc, err := s.cipher.Encrypt(string(plain))
	if err != nil {
		return err
	}
	return s.bindings.SaveProviderConfig(ctx, &identity.IMProviderConfig{
		Provider: "wecom", CredentialsEncrypted: enc, Enabled: enabled,
	})
}

// WeComStatus 企微配置状态（不回传 secret 明文）。
func (s *AuthService) WeComStatus(ctx context.Context) (map[string]any, error) {
	stored, err := s.bindings.GetProviderConfig(ctx, "wecom")
	if err != nil {
		return map[string]any{"provider": "wecom", "configured": false, "enabled": false}, nil
	}
	corpID := ""
	if plain, err := s.cipher.Decrypt(stored.CredentialsEncrypted); err == nil {
		var wc WeComConfig
		if json.Unmarshal([]byte(plain), &wc) == nil {
			corpID = wc.CorpID
		}
	}
	return map[string]any{
		"provider": "wecom", "configured": true, "enabled": stored.Enabled, "corpId": corpID,
	}, nil
}

// VerifyWeComConfig 验证给定凭证（保存前测试）或已存凭证。
func (s *AuthService) VerifyWeComConfig(ctx context.Context, cfg *WeComConfig) error {
	w := &WeComProvider{}
	if cfg != nil {
		w.Configure(*cfg)
	} else {
		p, err := s.activeProvider(ctx)
		if err != nil {
			return err
		}
		var ok bool
		if w, ok = p.(*WeComProvider); !ok {
			return errors.New("当前激活的提供商不是企微")
		}
	}
	return w.Verify(ctx)
}
