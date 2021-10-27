package log

import (
	tracer "github.com/ipfs/go-log/tracer"
	lwriter "github.com/ipfs/go-log/writer"
	"os"

	opentrace "github.com/opentracing/opentracing-go"

	log2 "github.com/ipfs/go-log/v2"
)

func init() {
	SetupLogging()
}

// Logging environment variables
const (
	envTracingFile = "GOLOG_TRACING_FILE" // /path/to/file
)

func SetupLogging() {
	// We're importing V2. Given that we setup logging on init, we should be
	// fine skipping the rest of the initialization.

	// TracerPlugins are instantiated after this, so use loggable tracer
	// by default, if a TracerPlugin is added it will override this
	lgblRecorder := tracer.NewLoggableRecorder()
	lgblTracer := tracer.New(lgblRecorder)
	opentrace.SetGlobalTracer(lgblTracer)

	if tracingfp := os.Getenv(envTracingFile); len(tracingfp) > 0 {
		f, err := os.Create(tracingfp)
		if err != nil {
			log.Error("failed to create tracing file: %s", tracingfp)
		} else {
			lwriter.WriterGroup.AddWriter(f)
		}
	}
}

// SetDebugLogging calls SetAllLoggers with logging.DEBUG
func SetDebugLogging() {
	log2.SetDebugLogging()
}

// SetAllLoggers changes the logging level of all loggers to lvl
func SetAllLoggers(lvl LogLevel) {
	log2.SetAllLoggers(lvl)
}

// SetLogLevel changes the log level of a specific subsystem
// name=="*" changes all subsystems
func SetLogLevel(name, level string) error {
	return log2.SetLogLevel(name, level)
}

// SetLogLevelRegex sets all loggers to level `l` that match expression `e`.
// An error is returned if `e` fails to compile.
func SetLogLevelRegex(e, l string) error {
	return log2.SetLogLevelRegex(e, l)
}

// GetSubsystems returns a slice containing the
// names of the current loggers
func GetSubsystems() []string {
	return log2.GetSubsystems()
}
