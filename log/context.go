package log

import (
	"context"

	"github.com/sirupsen/logrus"
)

var (
	// G is an alias for GetLogger.
	//
	// We may want to define this locally to a package to get package tagged log
	// messages.
	G = GetLogger

	// L is an alias for the the standard logger.
	L = logrus.NewEntry(logrus.StandardLogger())
)

type (
	loggerKey struct{}
)

// TraceLevel is the log level for tracing. Trace level is lower than debug level,
// and is usually used to trace detailed behavior of the program.
const TraceLevel = logrus.Level(uint32(logrus.DebugLevel + 1))

// ParseLevel takes a string level and returns the Logrus log level constant.
// It supports trace level.
func ParseLevel(lvl string) (logrus.Level, error) {
	if lvl == "trace" {
		return TraceLevel, nil
	}
	return logrus.ParseLevel(lvl)
}

// WithLogger returns a new context with the provided logger. Use in
// combination with logger.WithField(s) for great effect.
func WithLogger(ctx context.Context, logger *logrus.Entry) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger)
}

// GetLogger retrieves the current logger from the context. If no logger is
// available, the default logger is returned.
func GetLogger(ctx context.Context) *logrus.Entry {
	logger := ctx.Value(loggerKey{})

	if logger == nil {
		return L
	}

	return logger.(*logrus.Entry)
}

// Trace logs a message at level Trace with the log entry passed-in.
func Trace(e *logrus.Entry, args ...interface{}) {
	if e.Level >= TraceLevel {
		e.Debug(args...)
	}
}

// Tracef logs a message at level Trace with the log entry passed-in.
func Tracef(e *logrus.Entry, format string, args ...interface{}) {
	if e.Level >= TraceLevel {
		e.Debugf(format, args...)
	}
}
