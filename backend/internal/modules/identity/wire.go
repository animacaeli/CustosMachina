package identity

import "github.com/google/wire"

// Set 本模块的 wire ProviderSet。上层 app 聚合时引用。
var Set = wire.NewSet(
	NewUserRepository,
	NewUserService,
	NewHandler,
)
