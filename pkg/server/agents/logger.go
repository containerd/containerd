/*
Copyright 2017 The Kubernetes Authors.

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

package agents

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"time"

	"github.com/golang/glog"
)

const (
	// delimiter used in CRI logging format.
	delimiter = ' '
	// eof is end-of-line.
	eol = '\n'
	// timestampFormat is the timestamp format used in CRI logging format.
	timestampFormat = time.RFC3339Nano
	// pipeBufSize is the system PIPE_BUF size, on linux it is 4096 bytes.
	// POSIX.1 says that write less than PIPE_BUF is atmoic.
	pipeBufSize = 4096
	// bufSize is the size of the read buffer.
	bufSize = pipeBufSize - len(timestampFormat) - len(Stdout) - 2 /*2 delimiter*/ - 1 /*eol*/
)

// sandboxLogger is the log agent used for sandbox.
// It discards sandbox all output for now.
type sandboxLogger struct {
	rc io.ReadCloser
}

func (*agentFactory) NewSandboxLogger(rc io.ReadCloser) Agent {
	return &sandboxLogger{rc: rc}
}

func (s *sandboxLogger) Start() error {
	go func() {
		// Discard the output for now.
		io.Copy(ioutil.Discard, s.rc) // nolint: errcheck
		s.rc.Close()
	}()
	return nil
}

// containerLogger is the log agent used for container.
// It redirect container log into CRI log file, and decorate the log
// line into CRI defined format.
type containerLogger struct {
	path   string
	stream StreamType
	rc     io.ReadCloser
}

func (*agentFactory) NewContainerLogger(path string, stream StreamType, rc io.ReadCloser) Agent {
	return &containerLogger{
		path:   path,
		stream: stream,
		rc:     rc,
	}
}

func (c *containerLogger) Start() error {
	glog.V(4).Infof("Start reading log file %q", c.path)
	wc, err := os.OpenFile(c.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return fmt.Errorf("failed to open log file %q: %v", c.path, err)
	}
	go c.redirectLogs(wc)
	return nil
}

func (c *containerLogger) redirectLogs(wc io.WriteCloser) {
	defer c.rc.Close()
	defer wc.Close()
	streamBytes := []byte(c.stream)
	delimiterBytes := []byte{delimiter}
	r := bufio.NewReaderSize(c.rc, bufSize)
	for {
		// TODO(random-liu): Better define CRI log format, and escape newline in log.
		lineBytes, _, err := r.ReadLine()
		if err == io.EOF {
			glog.V(4).Infof("Finish redirecting log file %q", c.path)
			return
		}
		if err != nil {
			glog.Errorf("An error occurred when redirecting log file %q: %v", c.path, err)
			return
		}
		timestampBytes := time.Now().AppendFormat(nil, time.RFC3339Nano)
		data := bytes.Join([][]byte{timestampBytes, streamBytes, lineBytes}, delimiterBytes)
		data = append(data, eol)
		if _, err := wc.Write(data); err != nil {
			glog.Errorf("Fail to write log line %q: %v", data, err)
		}
		// Continue on write error to drain the input.
	}
}
