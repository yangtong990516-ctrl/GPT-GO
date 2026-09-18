// Package util holds the unified logger (CONTRACT §5). Every module must call
// util.Logger() instead of instantiating its own logger.
package util

import (
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	loggerOnce sync.Once
	logger     *zap.Logger
)

// InitLogger configures the global logger from the given level and JSON flag.
// It must be called once before any util.Logger() use (typically from main).
// level is one of: debug, info, warn, error (case-insensitive); unknown values
// fall back to info. json selects JSON (structured) output over console.
func InitLogger(level string, json bool) {
	loggerOnce.Do(func() {
		lvl := parseLevel(level)
		var cfg zap.Config
		if json {
			cfg = zap.NewProductionConfig()
		} else {
			cfg = zap.NewDevelopmentConfig()
			cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		}
		cfg.Level = zap.NewAtomicLevelAt(lvl)
		l, err := cfg.Build()
		if err != nil {
			// Fall back to a no-op logger rather than panicking at startup.
			l = zap.NewNop()
		}
		logger = l
	})
}

// Logger returns the global logger. If InitLogger was not called (e.g. tests),
// it returns a production logger at info level.
func Logger() *zap.Logger {
	loggerOnce.Do(func() {
		cfg := zap.NewProductionConfig()
		cfg.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
		l, err := cfg.Build()
		if err != nil {
			l = zap.NewNop()
		}
		logger = l
	})
	return logger
}

// parseLevel maps a config string to a zapcore.Level (unknown → info).
func parseLevel(s string) zapcore.Level {
	switch s {
	case "debug":
		return zapcore.DebugLevel
	case "info":
		return zapcore.InfoLevel
	case "warn", "warning":
		return zapcore.WarnLevel
	case "error":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}
