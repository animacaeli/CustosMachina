//go:build wireinject
// +build wireinject

package main

import (
	"github.com/google/wire"

	"github.com/custos-machina/backend/internal/app"
	"github.com/custos-machina/backend/internal/server"
)

// InitializeServer 由 wire 生成到 wire_gen.go（go generate ./...）。
func InitializeServer() (*server.Server, func(), error) {
	wire.Build(app.Set)
	return nil, nil, nil
}
