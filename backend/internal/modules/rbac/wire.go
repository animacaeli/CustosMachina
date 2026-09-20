package rbac

import (
	"github.com/gin-gonic/gin"

	"github.com/google/wire"
)

var Set = wire.NewSet(
	NewEnforcer,
	NewService,
	NewHandler,
	ProvideMiddleware,
	wire.Struct(new(MiddlewareDeps), "*"),
)

// ProvideMiddleware 输出 gin.HandlerFunc，由 server.NewEngine 作为 authz 中间件消费。
func ProvideMiddleware(deps MiddlewareDeps) gin.HandlerFunc { return NewMiddleware(deps) }
