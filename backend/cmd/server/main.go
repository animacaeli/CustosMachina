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

	// 服务错误经 channel 交主流程处理：后台 goroutine 内 Fatalf 会 os.Exit(1)
	// 跳过全部 defer（连接池关闭、日志 Sync），v0.12.0 审计中等项
	runErr := make(chan error, 1)
	go func() {
		if err := srv.Run(); err != nil {
			runErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-runErr:
		logger.Errorf("HTTP 服务退出: %v", err)
	case <-quit:
	}

	if err := srv.GracefulShutdown(); err != nil {
		logger.Errorf("优雅关闭失败: %v", err)
	}
}
