package auth

import (
	"context"
	"errors"
	"fmt"
)

// MockProvider 本地联调用的假 IM 提供商：无需公网回调。
// 通过 CUSTOS_IM_PROVIDER=mock 启用；AuthorizeURL 返回一个本地演示地址，
// ExchangeCode 接受任意非空 code，IM userid = "mock-" + code（不同 code 模拟不同人）。
// 仅为打通链路与 UI 开发，生产环境绝不应启用。
func init() { registerProvider(func() IdentityProvider { return &MockProvider{} }) }

type MockProvider struct{}

func (m *MockProvider) Name() string { return "mock" }

func (m *MockProvider) AuthorizeURL(redirectURI, state string) (string, error) {
	return fmt.Sprintf("%s?provider=mock&code=mock-user&state=%s", redirectURI, state), nil
}

func (m *MockProvider) ExchangeCode(_ context.Context, code string) (*IMUser, error) {
	if code == "" {
		return nil, errors.New("code 为空")
	}
	return &IMUser{IMUserID: "mock-" + code, IMName: "Mock 用户 " + code}, nil
}

func (m *MockProvider) Verify(_ context.Context) error { return nil }
