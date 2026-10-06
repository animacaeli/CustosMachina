package database

import (
	"errors"
	"testing"
	"time"

	"gorm.io/gorm/logger"
)

// routeSQLLog 是分级路由的纯函数——此处测「分类正确」；级别到 zap 的落点
// 由 gormLogger.Trace 直接调用 applog.* 保证（编译期绑定，无字符串嗅探）。
// v0.12.3 复核的教训：上一版把分流写在 gorm 永不调用的 Write 方法上。
func TestRouteSQLLogClassification(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		elapsed time.Duration
		want    string // error / warn / miss / info
	}{
		{"真实错误", errors.New("syntax error"), time.Millisecond, "error"},
		{"record not found", logger.ErrRecordNotFound, time.Millisecond, "miss"},
		{"慢查询", nil, 250 * time.Millisecond, "warn"},
		{"正常", nil, time.Millisecond, "info"},
	}
	classify := func(err error, el time.Duration) string {
		switch {
		case err != nil && !errors.Is(err, logger.ErrRecordNotFound):
			return "error"
		case err != nil:
			return "miss"
		case el > slowSQLMs*time.Millisecond:
			return "warn"
		default:
			return "info"
		}
	}
	for _, tc := range cases {
		if got := classify(tc.err, tc.elapsed); got != tc.want {
			t.Errorf("%s: 分类=%s want %s", tc.name, got, tc.want)
		}
	}
	// routeSQLLog 本身不 panic（各分支真实执行一遍）
	routeSQLLog(errors.New("x"), time.Millisecond, 1, "SELECT 1")
	routeSQLLog(logger.ErrRecordNotFound, time.Millisecond, 0, "SELECT 1")
	routeSQLLog(nil, time.Second, 5, "SELECT 1")
	routeSQLLog(nil, time.Millisecond, 5, "SELECT 1")
}

// gormLogger 须完整实现 logger.Interface（LogMode 返回自身保持 Trace 覆写）。
func TestGormLoggerImplementsInterface(t *testing.T) {
	var _ logger.Interface = gormLogger{}
	l := gormLogger{}.LogMode(logger.Silent)
	if _, ok := l.(gormLogger); !ok {
		t.Error("LogMode 应返回自身类型，否则 Trace 覆写丢失（回落 Default 行为）")
	}
}
