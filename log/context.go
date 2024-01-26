/*
   Copyright The containerd Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package log

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
)

var (
	// G is an alias for GetLogger.
	//
	// We may want to define this locally to a package to get package tagged log
	// messages.
	G = GetLogger

	// L is an alias for the standard logger.
	L = logrus.NewEntry(logrus.StandardLogger())
)

type loggerKey struct{}

// Fields type to pass to "WithFields".
type Fields = logrus.Fields

// RFC3339NanoFixed is [time.RFC3339Nano] with nanoseconds padded using
// zeros to ensure the formatted time is always the same number of
// characters.
const RFC3339NanoFixed = "2006-01-02T15:04:05.000000000Z07:00"

// Level is a logging level.
type Level = logrus.Level

// Supported log levels.
const (
	// TraceLevel level. Designates finer-grained informational events
	// than [DebugLevel].
	TraceLevel Level = logrus.TraceLevel

	// DebugLevel level. Usually only enabled when debugging. Very verbose
	// logging.
	DebugLevel Level = logrus.DebugLevel

	// InfoLevel level. General operational entries about what's going on
	// inside the application.
	InfoLevel Level = logrus.InfoLevel
)

// SetLevel sets log level globally. It returns an error if the given
// level is not supported.
//
// level can be one of:
//
//   - "trace" ([TraceLevel])
//   - "debug" ([DebugLevel])
//   - "info" ([InfoLevel])
func SetLevel(level string) error {
	lvl, err := logrus.ParseLevel(level)
	if err != nil {
		return err
	}

	logrus.SetLevel(lvl)
	return nil
}

// GetLevel returns the current log level.
func GetLevel() Level {
	return logrus.GetLevel()
}

// Supported log output formats.
const (
	// TextFormat represents the text logging format.
	TextFormat = "text"

	// JSONFormat represents the JSON logging format.
	JSONFormat = "json"
)

// SetFormat sets the log output format ([TextFormat] or [JSONFormat]).
func SetFormat(format string) error {
	switch format {
	case TextFormat:
		logrus.SetFormatter(&logrus.TextFormatter{
			TimestampFormat: RFC3339NanoFixed,
			FullTimestamp:   true,
		})
		return nil
	case JSONFormat:
		logrus.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: RFC3339NanoFixed,
		})
		return nil
	default:
		return fmt.Errorf("unknown log format: %s", format)
	}
}

// WithLogger returns a new context with the provided logger. Use in
// combination with logger.WithField(s) for great effect.
func WithLogger(ctx context.Context, logger *logrus.Entry) context.Context {
	return context.WithValue(ctx, loggerKey{}, logger.WithContext(ctx))
}

// GetLogger retrieves the current logger from the context. If no logger is
// available, the default logger is returned.
func GetLogger(ctx context.Context) *logrus.Entry {
	logger := ctx.Value(loggerKey{})

	if logger == nil {
		return L.WithContext(ctx)
	}

	return logger.(*logrus.Entry)
}
