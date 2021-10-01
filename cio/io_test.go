//go:build !windows
// +build !windows

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
	"bytes"
	"context"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/containerd/fifo"
	"github.com/google/go-cmp/cmp/cmpopts"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func assertHasPrefix(t *testing.T, s, prefix string) {
	t.Helper()
	if !strings.HasPrefix(s, prefix) {
		t.Fatalf("expected %s to start with %s", s, prefix)
	}
}

func TestNewFIFOSetInDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("NewFIFOSetInDir has different behaviour on windows")
	}

	root, err := os.MkdirTemp("", "test-new-fifo-set")
	assert.NilError(t, err)
	defer os.RemoveAll(root)

	fifos, err := NewFIFOSetInDir(root, "theid", true)
	assert.NilError(t, err)

	dir := filepath.Dir(fifos.Stdin)
	assertHasPrefix(t, dir, root)
	expected := &FIFOSet{
		Config: Config{
			Stdin:    filepath.Join(dir, "theid-stdin"),
			Stdout:   filepath.Join(dir, "theid-stdout"),
			Stderr:   filepath.Join(dir, "theid-stderr"),
			Terminal: true,
		},
	}
	assert.Assert(t, is.DeepEqual(fifos, expected, cmpFIFOSet))

	files, err := os.ReadDir(root)
	assert.NilError(t, err)
	assert.Check(t, is.Len(files, 1))

	assert.NilError(t, fifos.Close())
	files, err = os.ReadDir(root)
	assert.NilError(t, err)
	assert.Check(t, is.Len(files, 0))
}

var cmpFIFOSet = cmpopts.IgnoreUnexported(FIFOSet{})

func TestNewAttach(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("setupFIFOProducers not yet implemented on windows")
	}
	var (
		expectedStdin  = "this is the stdin"
		expectedStdout = "this is the stdout"
		expectedStderr = "this is the stderr"
		stdin          = bytes.NewBufferString(expectedStdin)
		stdout         = new(bytes.Buffer)
		stderr         = new(bytes.Buffer)
	)

	withBytesBuffers := func(streams *Streams) {
		*streams = Streams{Stdin: stdin, Stdout: stdout, Stderr: stderr}
	}
	attacher := NewAttach(withBytesBuffers)

	fifos, err := NewFIFOSetInDir("", "theid", false)
	assert.NilError(t, err)

	attachedFifos, err := attacher(fifos)
	assert.NilError(t, err)
	defer attachedFifos.Close()

	producers := setupFIFOProducers(t, attachedFifos.Config())
	initProducers(t, producers, expectedStdout, expectedStderr)

	actualStdin, err := io.ReadAll(producers.Stdin)
	assert.NilError(t, err)

	attachedFifos.Wait()
	attachedFifos.Cancel()
	assert.NilError(t, attachedFifos.Close())

	assert.Check(t, is.Equal(expectedStdout, stdout.String()))
	assert.Check(t, is.Equal(expectedStderr, stderr.String()))
	assert.Check(t, is.Equal(expectedStdin, string(actualStdin)))
}

type producers struct {
	Stdin  io.ReadCloser
	Stdout io.WriteCloser
	Stderr io.WriteCloser
}

func setupFIFOProducers(t *testing.T, fifos Config) producers {
	var (
		err   error
		pipes producers
		ctx   = context.Background()
	)

	pipes.Stdin, err = fifo.OpenFifo(ctx, fifos.Stdin, syscall.O_RDONLY, 0)
	assert.NilError(t, err)

	pipes.Stdout, err = fifo.OpenFifo(ctx, fifos.Stdout, syscall.O_WRONLY, 0)
	assert.NilError(t, err)

	pipes.Stderr, err = fifo.OpenFifo(ctx, fifos.Stderr, syscall.O_WRONLY, 0)
	assert.NilError(t, err)

	return pipes
}

func initProducers(t *testing.T, producers producers, stdout, stderr string) {
	_, err := producers.Stdout.Write([]byte(stdout))
	assert.NilError(t, err)
	assert.NilError(t, producers.Stdout.Close())

	_, err = producers.Stderr.Write([]byte(stderr))
	assert.NilError(t, err)
	assert.NilError(t, producers.Stderr.Close())
}

func TestBinaryIOArgs(t *testing.T) {
	res, err := BinaryIO("/file.bin", map[string]string{"id": "1"})("")
	assert.NilError(t, err)
	assert.Equal(t, "binary:///file.bin?id=1", res.Config().Stdout)
	assert.Equal(t, "binary:///file.bin?id=1", res.Config().Stderr)
}

func TestBinaryIOAbsolutePath(t *testing.T) {
	res, err := BinaryIO("/full/path/bin", nil)("!")
	assert.NilError(t, err)

	// Test parse back
	parsed, err := url.Parse(res.Config().Stdout)
	assert.NilError(t, err)
	assert.Equal(t, "binary", parsed.Scheme)
	assert.Equal(t, "/full/path/bin", parsed.Path)
}

func TestBinaryIOFailOnRelativePath(t *testing.T) {
	_, err := BinaryIO("./bin", nil)("!")
	assert.Error(t, err, "absolute path needed")
}

func TestLogFileAbsolutePath(t *testing.T) {
	res, err := LogFile("/full/path/file.txt")("!")
	assert.NilError(t, err)
	assert.Equal(t, "file:///full/path/file.txt", res.Config().Stdout)
	assert.Equal(t, "file:///full/path/file.txt", res.Config().Stderr)

	// Test parse back
	parsed, err := url.Parse(res.Config().Stdout)
	assert.NilError(t, err)
	assert.Equal(t, "file", parsed.Scheme)
	assert.Equal(t, "/full/path/file.txt", parsed.Path)
}

func TestLogFileFailOnRelativePath(t *testing.T) {
	_, err := LogFile("./file.txt")("!")
	assert.Error(t, err, "absolute path needed")
}

func TestLogURIGenerator(t *testing.T) {
	for _, tc := range []struct {
		scheme   string
		path     string
		args     map[string]string
		expected string
		err      string
	}{
		{
			scheme:   "fifo",
			path:     "/full/path/pipe.fifo",
			expected: "fifo:///full/path/pipe.fifo",
		},
		{
			scheme: "file",
			path:   "/full/path/file.txt",
			args: map[string]string{
				"maxSize": "100MB",
			},
			expected: "file:///full/path/file.txt?maxSize=100MB",
		},
		{
			scheme: "binary",
			path:   "/full/path/bin",
			args: map[string]string{
				"id": "testing",
			},
			expected: "binary:///full/path/bin?id=testing",
		},
		{
			scheme: "unknown",
			path:   "nowhere",
			err:    "absolute path needed",
		},
	} {
		uri, err := LogURIGenerator(tc.scheme, tc.path, tc.args)
		if err != nil {
			assert.Error(t, err, tc.err)
			continue
		}
		assert.Equal(t, tc.expected, uri.String())
	}
}
