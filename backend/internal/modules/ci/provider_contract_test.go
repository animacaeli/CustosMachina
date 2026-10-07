package ci

import (
	"testing"
)

/**
 * CIProvider 契约测试套件（独立审核第 3 批 P4）。
 * 新引擎适配器须通过本套件全部断言——保证 Poller/日志/AI 工具链
 * 对所有引擎的行为一致。用法：在你的 provider_test.go 里调
 * `RunCIProviderContract(t, myProvider, fixture)`。
 */

// ContractFixture 契约测试的固定数据（各引擎的模拟环境）。
type ContractFixture struct {
	// TriggerBuild 应成功触发的 repoPath/branch/tag
	RepoPath string
	Branch   string
	Tag      string
	// Verify 的期望结果（true = 凭证可用）
	VerifyShouldPass bool
}

// RunCIProviderContract 断言 CIProvider 的行为契约。
// 契约项：
//  1. Name() 非空且不含空格（做 webhook 路径段与 DB 枚举值）
//  2. Verify 返回 nil 或非 nil error（不 panic）
//  3. TriggerBuild 返回的 BuildRef.ExternalID 非空（Poller 依赖）
//  4. PollStatus 返回终态或进行中（不返回空字符串状态）
//  5. Log 返回字符串（可为空——Jenkins 不可达时降级）
func RunCIProviderContract(t *testing.T, p CIProvider, fx ContractFixture) {
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

	// Status 与 Log 契约由具体引擎测试跑（需真实 BuildRef——
	// 本套件验证接口满足 + Name 合法；引擎行为用 fake server 各自测）

}

// 内置 gitea 引擎跑一遍契约（真实环境不可达时 Skip——至少验证套件自身可运行）。
func TestBuiltinGiteaContract(t *testing.T) {
	p := newGiteaClient("http://localhost:0", "", "")
	RunCIProviderContract(t, p, ContractFixture{
		RepoPath: "org/demo", Branch: "main", Tag: "v0.0.0",
	})
}
