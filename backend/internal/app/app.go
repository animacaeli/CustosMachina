// Package app 是组装根（composition root）：聚合所有模块的 wire ProviderSet，
// 并提供跨模块的基础对象（数据库、模块列表）。新增模块只需：实现 server.Module、
// 导出 Set、在下面追加一行 —— 其余无需改动。
package app

import (
	"github.com/google/wire"
	"gorm.io/gorm"

	"github.com/custos-machina/backend/internal/config"
	"github.com/custos-machina/backend/internal/modules/auth"
	"github.com/custos-machina/backend/internal/modules/health"
	"github.com/custos-machina/backend/internal/modules/identity"
	"github.com/custos-machina/backend/internal/modules/rbac"
	"github.com/custos-machina/backend/internal/modules/resources"
	"github.com/custos-machina/backend/internal/modules/setup"
	"github.com/custos-machina/backend/internal/pkg/database"
	jwtpkg "github.com/custos-machina/backend/internal/pkg/jwt"
	"github.com/custos-machina/backend/internal/server"
)

// ProvideDB 打开数据库并迁移全部模块的模型（模型清单随模块在此登记）。
func ProvideDB(cfg *config.Config) (*gorm.DB, func(), error) {
	models := identity.Models()
	models = append(models, resources.Models()...)
	db, err := database.Open(&cfg.Database, models)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	return db, cleanup, nil
}

// ProvideModules 汇总所有模块为 server.Modules。
// 追加新模块时在此加一行 wire.Struct 不能自动完成，显式列出保证可读。
func ProvideModules(
	health *health.Handler,
	auth *auth.Handler,
	setup *setup.Handler,
	identity *identity.Handler,
	rbac *rbac.Handler,
	resources *resources.Handler,
) server.Modules {
	return server.Modules{health, auth, setup, identity, rbac, resources}
}

// infraSet 基础设施：配置、JWT、数据库。
// 凭据加密器 *crypto.Cipher 由 auth.Set 提供（缺主密钥时为 nil，使用处报明确错误）。
var infraSet = wire.NewSet(
	config.Load,
	jwtpkg.NewManager,
	ProvideDB,
)

// moduleSet 业务模块（新模块在此追加）。
var moduleSet = wire.NewSet(
	health.Set,
	auth.Set,
	setup.Set,
	identity.Set,
	rbac.Set,
	resources.Set,
	ProvideModules,
)

// Set 完整组装集合。
var Set = wire.NewSet(
	infraSet,
	moduleSet,
	server.New,
	server.NewEngine,
)
