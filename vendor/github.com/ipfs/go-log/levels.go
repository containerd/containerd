package log

import (
	log2 "github.com/ipfs/go-log/v2"
)

// LogLevel represents a log severity level. Use the package variables as an
// enum.
type LogLevel = log2.LogLevel

var (
	LevelDebug  = log2.LevelDebug
	LevelInfo   = log2.LevelInfo
	LevelWarn   = log2.LevelWarn
	LevelError  = log2.LevelError
	LevelDPanic = log2.LevelDPanic
	LevelPanic  = log2.LevelPanic
	LevelFatal  = log2.LevelFatal
)

// LevelFromString parses a string-based level and returns the corresponding
// LogLevel.
//
// Supported strings are: DEBUG, INFO, WARN, ERROR, DPANIC, PANIC, FATAL, and
// their lower-case forms.
//
// The returned LogLevel must be discarded if error is not nil.
func LevelFromString(level string) (LogLevel, error) {
	return log2.LevelFromString(level)
}
