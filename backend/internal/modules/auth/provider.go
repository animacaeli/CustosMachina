// IM 扫码登录插件抽象（FR2.1）。接口按企微/钉钉/飞书三家通用能力设计（D10），
// MVP 实现企业微信（2026-09-21 定，T3 关闭）与本地联调用 mock。
package auth

import (
	"context"
	"errors"
	"fmt"
)

var ErrProviderNotConfigured = errors.New("IM 提供商未启用，请先在系统管理中配置")

// IMUser 扫码授权后拿到的 IM 侧身份。
type IMUser struct {
	IMUserID string
	IMName   string
}

// IdentityProvider IM 身份提供商插件契约。
// AuthorizeURL 返回扫码授权页地址（前端渲染为二维码）；
// ExchangeCode 用回调 code 换 IM 用户身份；
// Verify 凭证连通性测试（setup 向导 / 配置页用）。
type IdentityProvider interface {
	Name() string
	AuthorizeURL(redirectURI, state string) (string, error)
	ExchangeCode(ctx context.Context, code string) (*IMUser, error)
	Verify(ctx context.Context) error
}

// ProviderConfig 一个提供商的已解密配置。
type ProviderConfig struct {
	Provider string
	Enabled  bool
}

// providerRegistry 运行时注册表：Name → 工厂。新插件 init() 里注册即可。
var providerRegistry = map[string]func() IdentityProvider{}

func registerProvider(p func() IdentityProvider) {
	providerRegistry[p().Name()] = p
}

// NewProvider 按名构造插件实例（配置由 service 注入）。
func NewProvider(name string) (IdentityProvider, error) {
	f, ok := providerRegistry[name]
	if !ok {
		return nil, fmt.Errorf("未知 IM 提供商: %s", name)
	}
	return f(), nil
}

// ProviderNames 列出全部已注册插件（固定顺序）。
func ProviderNames() []string {
	return []string{"wecom", "dingtalk", "feishu", "mock"}
}
