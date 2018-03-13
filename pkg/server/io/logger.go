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

package io

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"time"

	"github.com/sirupsen/logrus"
	runtime "k8s.io/kubernetes/pkg/kubelet/apis/cri/runtime/v1alpha2"

	cioutil "github.com/containerd/cri/pkg/ioutil"
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
	bufSize = pipeBufSize - len(timestampFormat) - len(Stdout) - len(runtime.LogTagPartial) - 3 /*3 delimiter*/ - 1 /*eol*/
)

// NewDiscardLogger creates logger which discards all the input.
func NewDiscardLogger() io.WriteCloser {
	return cioutil.NewNopWriteCloser(ioutil.Discard)
}

// NewCRILogger returns a write closer which redirect container log into
// log file, and decorate the log line into CRI defined format.
func NewCRILogger(path string, stream StreamType) (io.WriteCloser, error) {
	logrus.Debugf("Start writing log file %q", path)
	prc, pwc := io.Pipe()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0640)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %v", err)
	}
	go redirectLogs(path, prc, f, stream)
	return pwc, nil
}

func redirectLogs(path string, rc io.ReadCloser, wc io.WriteCloser, stream StreamType) {
	defer rc.Close()
	defer wc.Close()
	streamBytes := []byte(stream)
	delimiterBytes := []byte{delimiter}
	partialBytes := []byte(runtime.LogTagPartial)
	fullBytes := []byte(runtime.LogTagFull)
	r := bufio.NewReaderSize(rc, bufSize)
	for {
		lineBytes, isPrefix, err := r.ReadLine()
		if err == io.EOF {
			logrus.Debugf("Finish redirecting log file %q", path)
			return
		}
		if err != nil {
			logrus.WithError(err).Errorf("An error occurred when redirecting log file %q", path)
			return
		}
		tagBytes := fullBytes
		if isPrefix {
			tagBytes = partialBytes
		}
		timestampBytes := time.Now().AppendFormat(nil, time.RFC3339Nano)
		data := bytes.Join([][]byte{timestampBytes, streamBytes, tagBytes, lineBytes}, delimiterBytes)
		data = append(data, eol)
		if _, err := wc.Write(data); err != nil {
			logrus.WithError(err).Errorf("Fail to write %q log to log file %q", stream, path)
		}
		// Continue on write error to drain the input.
	}
}
