package ci

import (
	"testing"
)

/**
 * CIProvider 通用契约套件（独立审核第 3 批 P4；v0.12.15 纠偏——注释与
 * 签名对齐真实接口：CIProvider 只有 Name/Status/Log，平台只读 CI 状态，
 * 构建由仓库 webhook 驱动，没有 TriggerBuild/PollStatus）。
 * 本套件断言 Name 契约（通用可测部分）；Status/Log 的行为契约需要真实
 * BuildRef 与 fake server，由各引擎自己的测试覆盖（参考 TestJenkinsStatusAndLog、
 * gitea_test.go、gitee_test.go）——新引擎适配器必须提供同等的 fake-server
 * 行为测试。用法：provider_test.go 里调 `RunCIProviderContract(t, myProvider)`。
 */

// RunCIProviderContract 断言 CIProvider 的通用契约：
//  1. Name() 非空且不含空格/路径分隔符——Name 用作 DB 枚举值与 webhook 路径段
func RunCIProviderContract(t *testing.T, p CIProvider) {
	t.Helper()

	t.Run("name", func(t *testing.T) {
		name := p.Name()
		if name == "" {
			t.Error("Name() 不得为空")
		}
		for _, r := range name {
			if r == ' ' || r == '/' || r == '\\' {
				t.Errorf("Name() 含路径分隔符或空格 %q", name)
			}
		}
	})
}

// 内置 gitea 引擎跑一遍契约（真实环境不可达时 Skip——至少验证套件自身可运行）。
func TestBuiltinGiteaContract(t *testing.T) {
	p := newGiteaClient("http://localhost:0", "", "")
	RunCIProviderContract(t, p)
}
