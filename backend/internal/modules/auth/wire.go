package auth

import "github.com/google/wire"

var Set = wire.NewSet(
	NewAuthService,
	NewHandler,
	ProvideAuthMiddleware,
)
