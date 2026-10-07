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

// routeSQLLog 按结果分级：错误→Error、慢查询→Warn、其余→Info。
// record not found 是正常业务分支，降为 Debug。分级判定收敛在
// sqlLogLevel（纯函数），测试直接绑实现断言（v0.12.4 复核：测试内
// 复刻闭包是弱断言——实现改了测试照样绿）。
func sqlLogLevel(err error, elapsed time.Duration) string {
	switch {
	case err != nil && !errors.Is(err, logger.ErrRecordNotFound):
		return "error"
	case err != nil: // ErrRecordNotFound：业务分支
		return "miss"
	case elapsed > slowSQLMs*time.Millisecond:
		return "warn"
	default:
		return "info"
	}
}

func routeSQLLog(err error, elapsed time.Duration, rows int64, sql string) {
	ms := float64(elapsed.Microseconds()) / 1000
	switch sqlLogLevel(err, elapsed) {
	case "error":
		applog.Errorf("[gorm] SQL 失败: %v | %.1fms | rows=%d | %s", err, ms, rows, sql)
	case "miss":
		applog.Debugf("[gorm] miss | %.1fms | %s", ms, sql)
	case "warn":
		applog.Warnf("[gorm] slow %.1fms | rows=%d | %s", ms, rows, sql)
	default:
		applog.Infof("[gorm] %.1fms | rows=%d | %s", ms, rows, sql)
	}
}
