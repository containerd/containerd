/*
Copyright 2019-2021 Intel Corporation

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
	"fmt"
	stdlog "log"
	"strings"
)

// Logger is the logging interface for goresctl
type Logger interface {
	Debugf(format string, v ...interface{})
	Infof(format string, v ...interface{})
	Warnf(format string, v ...interface{})
	Errorf(format string, v ...interface{})
	Panicf(format string, v ...interface{})
	Fatalf(format string, v ...interface{})
}

type logger struct {
	*stdlog.Logger
}

// NewLoggerWrapper wraps an implementation of the golang standard intreface
// into a goresctl specific compatible logger interface
func NewLoggerWrapper(l *stdlog.Logger) Logger {
	return &logger{Logger: l}
}

func (l *logger) Debugf(format string, v ...interface{}) {
	l.Logger.Printf("DEBUG: "+format, v...)
}

func (l *logger) Infof(format string, v ...interface{}) {
	l.Logger.Printf("INFO: "+format, v...)
}

func (l *logger) Warnf(format string, v ...interface{}) {
	l.Logger.Printf("WARN: "+format, v...)
}

func (l *logger) Errorf(format string, v ...interface{}) {
	l.Logger.Printf("ERROR: "+format, v...)
}

func (l *logger) Panicf(format string, v ...interface{}) {
	l.Logger.Panicf(format, v...)
}

func (l *logger) Fatalf(format string, v ...interface{}) {
	l.Logger.Fatalf(format, v...)
}

func InfoBlock(l Logger, heading, linePrefix, format string, v ...interface{}) {
	l.Infof("%s", heading)

	lines := strings.Split(fmt.Sprintf(format, v...), "\n")
	for _, line := range lines {
		l.Infof("%s%s", linePrefix, line)
	}
}

func DebugBlock(l Logger, heading, linePrefix, format string, v ...interface{}) {
	l.Debugf("%s", heading)

	lines := strings.Split(fmt.Sprintf(format, v...), "\n")
	for _, line := range lines {
		l.Debugf("%s%s", linePrefix, line)
	}
}
