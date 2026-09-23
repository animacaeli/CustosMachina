// Package logger 全局日志门面（第三阶段 M0 引入 zap）。
// 约定：业务代码一律 import 本包（logger.Info/Warn/Error...），
// 禁止散落直接 import zap 或标准库 log，避免多套日志并存。
package logger

import (
	"io"
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var std *zap.SugaredLogger

// Options 日志初始化参数。
type Options struct {
	Level      string // debug / info / warn / error
	Dir        string // 日志目录；空 = 只输出到 stderr，不写文件
	MaxSize    int    // 单文件 MB（lumberjack）
	MaxBackups int
	MaxAge     int // 保留天数
}

// Init 初始化全局 logger（进程内只应调用一次）。返回的 io.Writer 供 gin 等框架复用。
func Init(o Options) io.Writer {
	lvl := parseLevel(o.Level)

	console := zapcore.NewConsoleEncoder(zap.NewDevelopmentEncoderConfig())
	cores := []zapcore.Core{zapcore.NewCore(console, zapcore.Lock(os.Stderr), lvl)}

	var fileWriter io.Writer = io.Discard
	if o.Dir != "" {
		_ = os.MkdirAll(o.Dir, 0o755)
		lj := &lumberjack.Logger{
			Filename:   filepath.Join(o.Dir, "custos.log"),
			MaxSize:    orDefault(o.MaxSize, 50),
			MaxBackups: orDefault(o.MaxBackups, 5),
			MaxAge:     orDefault(o.MaxAge, 14),
			Compress:   true,
		}
		// 文件走 JSON，便于后续接入日志采集；控制台保持人类可读。
		jsonEnc := zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
		cores = append(cores, zapcore.NewCore(jsonEnc, zapcore.AddSync(lj), lvl))
		fileWriter = lj
	}

	std = zap.New(zapcore.NewTee(cores...)).Sugar()
	return fileWriter
}

// Sync 冲刷缓冲；进程退出前调用。
func Sync() {
	if std != nil {
		_ = std.Sync()
	}
}

// L 拿当前 SugaredLogger。Init 之前调用会退化到只写 stderr 的默认实例，避免 nil panic。
func L() *zap.SugaredLogger {
	if std == nil {
		std = zap.NewNop().Sugar()
	}
	return std
}

func Debugf(format string, args ...any) { L().Debugf(format, args...) }
func Infof(format string, args ...any)  { L().Infof(format, args...) }
func Warnf(format string, args ...any)  { L().Warnf(format, args...) }
func Errorf(format string, args ...any) { L().Errorf(format, args...) }
func Error(args ...any)                 { L().Error(args...) }
func Fatalf(format string, args ...any) { L().Fatalf(format, args...) }

func parseLevel(s string) zapcore.Level {
	switch s {
	case "debug":
		return zapcore.DebugLevel
	case "warn":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

func orDefault(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}
