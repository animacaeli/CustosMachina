// Package database 提供多方言（sqlite / mysql / postgres）的 gorm 连接。
// 注意：模型中避免使用方言特有能力；JSON 字段以字符串落库（决策 D12）。
package database

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/custos-machina/backend/internal/config"
)

// Open 按配置建立数据库连接并执行自动迁移。
func Open(cfg *config.Database, models []any) (*gorm.DB, error) {
	var dialector gorm.Dialector
	switch cfg.Driver {
	case "sqlite":
		if dir := filepath.Dir(cfg.DSN); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, fmt.Errorf("创建数据目录失败: %w", err)
			}
		}
		dialector = sqlite.Open(cfg.DSN)
	case "mysql":
		dialector = mysql.Open(cfg.DSN)
	case "postgres":
		dialector = postgres.Open(cfg.DSN)
	default:
		return nil, fmt.Errorf("不支持的数据库驱动: %s", cfg.Driver)
	}

	// 记录未找到是正常业务分支，不落日志；慢查询与真实错误仍告警
	gl := logger.New(log.New(os.Stderr, "\r\n", log.LstdFlags), logger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  logger.Warn,
		IgnoreRecordNotFoundError: true,
		Colorful:                  true,
	})
	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: gl,
	})
	if err != nil {
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	if len(models) > 0 {
		if err := db.AutoMigrate(models...); err != nil {
			return nil, fmt.Errorf("自动迁移失败: %w", err)
		}
	}
	return db, nil
}
