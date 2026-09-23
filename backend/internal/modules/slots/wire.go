package slots

import "github.com/google/wire"

// Set 本模块的 wire ProviderSet。
var Set = wire.NewSet(
	NewService,
	NewSweeper,
	NewHandler,
)
