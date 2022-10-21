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

package cio

import (
	"fmt"
	"net/url"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	prefix    string
	urlPrefix string
)

func init() {
	if runtime.GOOS == "windows" {
		prefix = "C:"
		urlPrefix = "/C:"
	}
}

func TestBinaryIOArgs(t *testing.T) {
	res, err := BinaryIO(prefix+"/file.bin", map[string]string{"id": "1"})("")
	require.NoError(t, err)
	expected := fmt.Sprintf("binary://%s/file.bin?id=1", urlPrefix)
	assert.Equal(t, expected, res.Config().Stdout)
	assert.Equal(t, expected, res.Config().Stderr)
}

func TestBinaryIOAbsolutePath(t *testing.T) {
	res, err := BinaryIO(prefix+"/full/path/bin", nil)("!")
	require.NoError(t, err)

	// Test parse back
	parsed, err := url.Parse(res.Config().Stdout)
	require.NoError(t, err)
	assert.Equal(t, "binary", parsed.Scheme)
	assert.Equal(t, urlPrefix+"/full/path/bin", parsed.Path)
}

func TestBinaryIOFailOnRelativePath(t *testing.T) {
	_, err := BinaryIO("./bin", nil)("!")
	assert.Error(t, err, "absolute path needed")
}

func TestLogFileAbsolutePath(t *testing.T) {
	res, err := LogFile(prefix + "/full/path/file.txt")("!")
	require.NoError(t, err)
	expected := fmt.Sprintf("file://%s/full/path/file.txt", urlPrefix)
	assert.Equal(t, expected, res.Config().Stdout)
	assert.Equal(t, expected, res.Config().Stderr)

	// Test parse back
	parsed, err := url.Parse(res.Config().Stdout)
	assert.NoError(t, err)
	assert.Equal(t, "file", parsed.Scheme)
	assert.Equal(t, urlPrefix+"/full/path/file.txt", parsed.Path)
}

func TestLogFileFailOnRelativePath(t *testing.T) {
	_, err := LogFile("./file.txt")("!")
	assert.Error(t, err, "absolute path needed")
}

type LogURIGeneratorTestCase struct {
	// Arbitrary scheme string (e.g. "binary")
	scheme string
	// Path to executable/file: (e.g. "/some/path/to/bin.exe")
	path string
	// Extra query args expected to be in the URL (e.g. "id=123")
	args map[string]string
	// What the test case is expecting as an output (e.g. "binary:///some/path/to/bin.exe?id=123")
	expected string
	// Error string to be expected:
	err string
}

func baseTestLogURIGenerator(t *testing.T, testCases []LogURIGeneratorTestCase) {
	for _, tc := range testCases {
		uri, err := LogURIGenerator(tc.scheme, tc.path, tc.args)
		if tc.err != "" {
			assert.ErrorContains(t, err, tc.err)
			continue
		}
		assert.NoError(t, err)
		assert.Equal(t, tc.expected, uri.String())
	}
}
