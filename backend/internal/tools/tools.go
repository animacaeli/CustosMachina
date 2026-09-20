//go:build tools

// Package tools 固定开发期工具依赖（wire），防止 go mod tidy 丢失。
package tools

import (
	_ "github.com/google/wire/cmd/wire"
)
