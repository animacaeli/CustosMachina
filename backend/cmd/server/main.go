// CustosMachina 后端入口。依赖组装全部由 wire 完成（见 wire_gen.go）。
package main

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/custos-machina/backend/internal/config"
	"github.com/custos-machina/backend/internal/pkg/logger"
)

//go:generate go run github.com/google/wire/cmd/wire

func main() {
	// 日志必须先于一切初始化（wire 内部也会读配置，这里单独 Load 一次只为拿日志段）。
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	logger.Init(logger.Options{
		Level:      cfg.Log.Level,
		Dir:        cfg.Log.Dir,
		MaxSize:    cfg.Log.MaxSizeMB,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAge:     cfg.Log.MaxAgeDays,
	})
	defer logger.Sync()

	srv, cleanup, err := InitializeServer()
	if err != nil {
		logger.Fatalf("初始化失败: %v", err)
	}
	defer cleanup()

	go func() {
		if err := srv.Run(); err != nil {
			logger.Fatalf("%v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	if err := srv.GracefulShutdown(); err != nil {
		logger.Errorf("优雅关闭失败: %v", err)
	}
}
