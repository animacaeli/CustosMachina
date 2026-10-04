package ai

import "github.com/google/wire"

// Set 本模块的 wire ProviderSet。
var Set = wire.NewSet(
	NewService,
	NewHandler,
	NewDigestService,
	NewChatService,
	NewChatHandler,
)
