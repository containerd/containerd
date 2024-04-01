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

package io

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	runtime "k8s.io/cri-api/pkg/apis/runtime/v1"

	cioutil "github.com/containerd/containerd/v2/pkg/ioutil"
)

func TestRedirectLogs(t *testing.T) {
	// defaultBufSize is even number
	const maxLen = defaultBufSize * 4
	for desc, test := range map[string]struct {
		input   string
		stream  StreamType
		maxLen  int
		tag     []runtime.LogTag
		content []string
	}{
		"stdout log": {
			input:  "test stdout log 1\ntest stdout log 2\n",
			stream: Stdout,
			maxLen: maxLen,
			tag: []runtime.LogTag{
				runtime.LogTagFull,
				runtime.LogTagFull,
			},
			content: []string{
				"test stdout log 1",
				"test stdout log 2",
			},
		},
		"stderr log": {
			input:  "test stderr log 1\ntest stderr log 2\n",
			stream: Stderr,
			maxLen: maxLen,
			tag: []runtime.LogTag{
				runtime.LogTagFull,
				runtime.LogTagFull,
			},
			content: []string{
				"test stderr log 1",
				"test stderr log 2",
			},
		},
		"log ends without newline": {
			input:  "test stderr log 1\ntest stderr log 2",
			stream: Stderr,
			maxLen: maxLen,
			tag: []runtime.LogTag{
				runtime.LogTagFull,
				runtime.LogTagPartial,
			},
			content: []string{
				"test stderr log 1",
				"test stderr log 2",
			},
		},
		"log length equal to buffer size": {
			input:  strings.Repeat("a", defaultBufSize) + "\n" + strings.Repeat("a", defaultBufSize) + "\n",
			stream: Stdout,
			maxLen: maxLen,
			tag: []runtime.LogTag{
				runtime.LogTagFull,
				runtime.LogTagFull,
			},
			content: []string{
				strings.Repeat("a", defaultBufSize),
				strings.Repeat("a", defaultBufSize),
			},
		},
		"log length longer than buffer size": {
			input:  strings.Repeat("a", defaultBufSize*2+10) + "\n" + strings.Repeat("a", defaultBufSize*2+20) + "\n",
			stream: Stdout,
			maxLen: maxLen,
			tag: []runtime.LogTag{
				runtime.LogTagFull,
				runtime.LogTagFull,
			},
			content: []string{
				strings.Repeat("a", defaultBufSize*2+10),
				strings.Repeat("a", defaultBufSize*2+20),
			},
		},
		"log length equal to max length": {
			input:  strings.Repeat("a", maxLen) + "\n" + strings.Repeat("a", maxLen) + "\n",
			stream: Stdout,
			maxLen: maxLen,
			tag: []runtime.LogTag{
				runtime.LogTagFull,
				runtime.LogTagFull,
			},
			content: []string{
				strings.Repeat("a", maxLen),
				strings.Repeat("a", maxLen),
			},
		},
		"log length exceed max length by 1": {
			input:  strings.Repeat("a", maxLen+1) + "\n" + strings.Repeat("a", maxLen+1) + "\n",
			stream: Stdout,
			maxLen: maxLen,
			tag: []runtime.LogTag{
				runtime.LogTagPartial,
				runtime.LogTagFull,
				runtime.LogTagPartial,
				runtime.LogTagFull,
			},
			content: []string{
				strings.Repeat("a", maxLen),
				"a",
				strings.Repeat("a", maxLen),
				"a",
			},
		},
		"log length longer than max length": {
			input:  strings.Repeat("a", maxLen*2) + "\n" + strings.Repeat("a", maxLen*2+1) + "\n",
			stream: Stdout,
			maxLen: maxLen,
			tag: []runtime.LogTag{
				runtime.LogTagPartial,
				runtime.LogTagFull,
				runtime.LogTagPartial,
				runtime.LogTagPartial,
				runtime.LogTagFull,
			},
			content: []string{
				strings.Repeat("a", maxLen),
				strings.Repeat("a", maxLen),
				strings.Repeat("a", maxLen),
				strings.Repeat("a", maxLen),
				"a",
			},
		},
		"max length shorter than buffer size": {
			input:  strings.Repeat("a", defaultBufSize*3/2+10) + "\n" + strings.Repeat("a", defaultBufSize*3/2+20) + "\n",
			stream: Stdout,
			maxLen: defaultBufSize / 2,
			tag: []runtime.LogTag{
				runtime.LogTagPartial,
				runtime.LogTagPartial,
				runtime.LogTagPartial,
				runtime.LogTagFull,
				runtime.LogTagPartial,
				runtime.LogTagPartial,
				runtime.LogTagPartial,
				runtime.LogTagFull,
			},
			content: []string{
				strings.Repeat("a", defaultBufSize*1/2),
				strings.Repeat("a", defaultBufSize*1/2),
				strings.Repeat("a", defaultBufSize*1/2),
				strings.Repeat("a", 10),
				strings.Repeat("a", defaultBufSize*1/2),
				strings.Repeat("a", defaultBufSize*1/2),
				strings.Repeat("a", defaultBufSize*1/2),
				strings.Repeat("a", 20),
			},
		},
		"log length longer than max length, and (maxLen % defaultBufSize != 0)": {
			input:  strings.Repeat("a", defaultBufSize*2+10) + "\n" + strings.Repeat("a", defaultBufSize*2+20) + "\n",
			stream: Stdout,
			maxLen: defaultBufSize * 3 / 2,
			tag: []runtime.LogTag{
				runtime.LogTagPartial,
				runtime.LogTagFull,
				runtime.LogTagPartial,
				runtime.LogTagFull,
			},
			content: []string{
				strings.Repeat("a", defaultBufSize*3/2),
				strings.Repeat("a", defaultBufSize*1/2+10),
				strings.Repeat("a", defaultBufSize*3/2),
				strings.Repeat("a", defaultBufSize*1/2+20),
			},
		},
		"no limit if max length is 0": {
			input:  strings.Repeat("a", defaultBufSize*10+10) + "\n" + strings.Repeat("a", defaultBufSize*10+20) + "\n",
			stream: Stdout,
			maxLen: 0,
			tag: []runtime.LogTag{
				runtime.LogTagFull,
				runtime.LogTagFull,
			},
			content: []string{
				strings.Repeat("a", defaultBufSize*10+10),
				strings.Repeat("a", defaultBufSize*10+20),
			},
		},
		"no limit if max length is negative": {
			input:  strings.Repeat("a", defaultBufSize*10+10) + "\n" + strings.Repeat("a", defaultBufSize*10+20) + "\n",
			stream: Stdout,
			maxLen: -1,
			tag: []runtime.LogTag{
				runtime.LogTagFull,
				runtime.LogTagFull,
			},
			content: []string{
				strings.Repeat("a", defaultBufSize*10+10),
				strings.Repeat("a", defaultBufSize*10+20),
			},
		},
		"log length longer than buffer size with tailing \\r\\n": {
			input:  strings.Repeat("a", defaultBufSize-1) + "\r\n" + strings.Repeat("a", defaultBufSize-1) + "\r\n",
			stream: Stdout,
			maxLen: -1,
			tag: []runtime.LogTag{
				runtime.LogTagFull,
				runtime.LogTagFull,
			},
			content: []string{
				strings.Repeat("a", defaultBufSize-1),
				strings.Repeat("a", defaultBufSize-1),
			},
		},
	} {
		t.Run(desc, func(t *testing.T) {
			rc := io.NopCloser(strings.NewReader(test.input))
			buf := bytes.NewBuffer(nil)
			wc := cioutil.NewNopWriteCloser(buf)
			redirectLogs("test-path", rc, wc, test.stream, test.maxLen)
			output := buf.String()
			lines := strings.Split(output, "\n")
			lines = lines[:len(lines)-1] // Discard empty string after last \n
			assert.Len(t, lines, len(test.content))
			for i := range lines {
				fields := strings.SplitN(lines[i], string([]byte{delimiter}), 4)
				require.Len(t, fields, 4)
				_, err := time.Parse(timestampFormat, fields[0])
				assert.NoError(t, err)
				assert.EqualValues(t, test.stream, fields[1])
				assert.Equal(t, string(test.tag[i]), fields[2])
				assert.Equal(t, test.content[i], fields[3])
			}
		})
	}
}
