package log

import "go.uber.org/zap/zapcore"

// LogLevel represents a log severity level. Use the package variables as an
// enum.
type LogLevel zapcore.Level

var (
	LevelDebug  = LogLevel(zapcore.DebugLevel)
	LevelInfo   = LogLevel(zapcore.InfoLevel)
	LevelWarn   = LogLevel(zapcore.WarnLevel)
	LevelError  = LogLevel(zapcore.ErrorLevel)
	LevelDPanic = LogLevel(zapcore.DPanicLevel)
	LevelPanic  = LogLevel(zapcore.PanicLevel)
	LevelFatal  = LogLevel(zapcore.FatalLevel)
)

// LevelFromString parses a string-based level and returns the corresponding
// LogLevel.
//
// Supported strings are: DEBUG, INFO, WARN, ERROR, DPANIC, PANIC, FATAL, and
// their lower-case forms.
//
// The returned LogLevel must be discarded if error is not nil.
func LevelFromString(level string) (LogLevel, error) {
	lvl := zapcore.InfoLevel // zero value
	err := lvl.Set(level)
	return LogLevel(lvl), err
}
