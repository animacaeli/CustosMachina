package ci

import "github.com/google/wire"

// Set 本模块的 wire ProviderSet（NewPoller 的 cleanup 由 wire 聚合为停机钩子）。
var Set = wire.NewSet(
	NewService,
	NewPoller,
	NewHandler,
)
