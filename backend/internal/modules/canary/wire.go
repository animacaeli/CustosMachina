package canary

import "github.com/google/wire"

// Set 本模块的 wire ProviderSet（resources.Service 作为 SSHRunner 注入）。
var Set = wire.NewSet(
	NewService,
	NewHandler,
	// SSHRunner → *resources.Service 的 Bind 在 app.moduleSet 里完成
	//（Bind 要求同 set 内有提供者，resources.Set 与本 Set 同级注册）。
)
