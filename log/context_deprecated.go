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

	"github.com/containerd/log"
)

// G is a shorthand for [GetLogger].
var G = log.G

// L is an alias for the standard logger.
var L = log.L

// Fields type to pass to "WithFields".
type Fields = log.Fields

// Entry is a logging entry.
type Entry = log.Entry

// RFC3339NanoFixed is [time.RFC3339Nano] with nanoseconds padded using
// zeros to ensure the formatted time is always the same number of
// characters.
const RFC3339NanoFixed = log.RFC3339NanoFixed

// Level is a logging level.
type Level = log.Level

// Supported log levels.
const (
	// TraceLevel level.
	TraceLevel Level = log.TraceLevel

	// DebugLevel level.
	DebugLevel Level = log.DebugLevel

	// InfoLevel level.
	InfoLevel Level = log.InfoLevel

	// WarnLevel level.
	WarnLevel Level = log.WarnLevel

	// ErrorLevel level
	ErrorLevel Level = log.ErrorLevel

	// FatalLevel level.
	FatalLevel Level = log.FatalLevel

	// PanicLevel level.
	PanicLevel Level = log.PanicLevel
)

// SetLevel sets log level globally. It returns an error if the given
// level is not supported.
func SetLevel(level string) error {
	return log.SetLevel(level)
}

// GetLevel returns the current log level.
func GetLevel() log.Level {
	return log.GetLevel()
}

// OutputFormat specifies a log output format.
type OutputFormat = log.OutputFormat

// Supported log output formats.
const (
	// TextFormat represents the text logging format.
	TextFormat log.OutputFormat = "text"

	// JSONFormat represents the JSON logging format.
	JSONFormat log.OutputFormat = "json"
)

// SetFormat sets the log output format.
func SetFormat(format OutputFormat) error {
	return log.SetFormat(format)
}

// WithLogger returns a new context with the provided logger. Use in
// combination with logger.WithField(s) for great effect.
func WithLogger(ctx context.Context, logger *log.Entry) context.Context {
	return log.WithLogger(ctx, logger)
}

// GetLogger retrieves the current logger from the context. If no logger is
// available, the default logger is returned.
func GetLogger(ctx context.Context) *log.Entry {
	return log.GetLogger(ctx)
}
