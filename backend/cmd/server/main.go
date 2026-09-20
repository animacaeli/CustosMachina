// CustosMachina 后端入口。依赖组装全部由 wire 完成（见 wire_gen.go）。
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

//go:generate go run github.com/google/wire/cmd/wire

func main() {
	srv, cleanup, err := InitializeServer()
	if err != nil {
		log.Fatalf("初始化失败: %v", err)
	}
	defer cleanup()

	go func() {
		if err := srv.Run(); err != nil {
			log.Fatalf("%v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	if err := srv.GracefulShutdown(); err != nil {
		log.Printf("优雅关闭失败: %v", err)
	}
}
