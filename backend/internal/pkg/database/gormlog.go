// gormlog.go 自定义 gorm 日志实现：直接路由 zap（v0.12.3 复核 P0——
// 此前的 writerAdapter.Write 分流是死代码：gorm 经 Printf 输出且 SQL 的
// Trace 行不带任何级别标记，字符串嗅探永远匹配不到，SQL 错误全落 INFO）。
package database

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm/logger"

	applog "github.com/custos-machina/backend/internal/pkg/logger"
)

// slowSQLMs 慢查询阈值（与旧 Config.SlowThreshold 一致）。
const slowSQLMs = 200

// gormLogger 实现 gorm 的 logger.Interface：Trace 按结果分级直连 zap。
type gormLogger struct{}

// LogMode 固定级别（分级由 Trace 自行裁定），返回自身保持 Trace 覆写不丢。
func (gormLogger) LogMode(logger.LogLevel) logger.Interface { return gormLogger{} }

func (gormLogger) Info(_ context.Context, msg string, args ...any) {
	applog.Infof("[gorm] "+msg, args...)
}

func (gormLogger) Warn(_ context.Context, msg string, args ...any) {
	applog.Warnf("[gorm] "+msg, args...)
}

func (gormLogger) Error(_ context.Context, msg string, args ...any) {
	applog.Errorf("[gorm] "+msg, args...)
}

func (gormLogger) Trace(_ context.Context, begin time.Time, fc func() (string, int64), err error) {
	sql, rows := fc()
	routeSQLLog(err, time.Since(begin), rows, sql)
}

// routeSQLLog 按结果分级（纯函数便于测试）：错误→Error、慢查询→Warn、
// 其余→Info。record not found 是正常业务分支，降为 Info。
func routeSQLLog(err error, elapsed time.Duration, rows int64, sql string) {
	ms := float64(elapsed.Microseconds()) / 1000
	switch {
	case err != nil && !errors.Is(err, logger.ErrRecordNotFound):
		applog.Errorf("[gorm] SQL 失败: %v | %.1fms | rows=%d | %s", err, ms, rows, sql)
	case err != nil: // ErrRecordNotFound：业务分支
		applog.Debugf("[gorm] miss | %.1fms | %s", ms, sql)
	case elapsed > slowSQLMs*time.Millisecond:
		applog.Warnf("[gorm] slow %.1fms | rows=%d | %s", ms, rows, sql)
	default:
		applog.Infof("[gorm] %.1fms | rows=%d | %s", ms, rows, sql)
	}
}
