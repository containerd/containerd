//go:build !windows

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
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/containerd/fifo"
	"github.com/stretchr/testify/assert"
)

func TestOpenFifos(t *testing.T) {
	scenarios := []*FIFOSet{
		{
			Config: Config{
				Stdin:  "",
				Stdout: filepath.Join("This/does/not/exist", "test-stdout"),
				Stderr: filepath.Join("This/does/not/exist", "test-stderr"),
			},
		},
		{
			Config: Config{
				Stdin:  filepath.Join("This/does/not/exist", "test-stdin"),
				Stdout: "",
				Stderr: filepath.Join("This/does/not/exist", "test-stderr"),
			},
		},
		{
			Config: Config{
				Stdin:  "",
				Stdout: "",
				Stderr: filepath.Join("This/does/not/exist", "test-stderr"),
			},
		},
	}
	for _, scenario := range scenarios {
		_, err := openFifos(context.Background(), scenario)
		assert.Error(t, err, scenario)
	}
}

// TestOpenFifosWithTerminal tests openFifos should not open stderr if terminal
// is set.
func TestOpenFifosWithTerminal(t *testing.T) {
	var ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	ioFifoDir := t.TempDir()

	cfg := Config{
		Stdout: filepath.Join(ioFifoDir, "test-stdout"),
		Stderr: filepath.Join(ioFifoDir, "test-stderr"),
	}

	// Without terminal, pipes.Stderr should not be nil
	{
		p, err := openFifos(ctx, NewFIFOSet(cfg, nil))
		if err != nil {
			t.Fatalf("unexpected error during openFifos: %v", err)
		}

		if p.Stderr == nil {
			t.Fatalf("unexpected empty stderr pipe")
		}
	}

	// With terminal, pipes.Stderr should be nil
	{
		cfg.Terminal = true
		p, err := openFifos(ctx, NewFIFOSet(cfg, nil))
		if err != nil {
			t.Fatalf("unexpected error during openFifos: %v", err)
		}

		if p.Stderr != nil {
			t.Fatalf("unexpected stderr pipe")
		}
	}
}

func assertHasPrefix(t *testing.T, s, prefix string) {
	t.Helper()
	if !strings.HasPrefix(s, prefix) {
		t.Fatalf("expected %s to start with %s", s, prefix)
	}
}

func TestNewFIFOSetInDir(t *testing.T) {
	root := t.TempDir()

	fifos, err := NewFIFOSetInDir(root, "theid", true)
	assert.NoError(t, err)

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

	assert.Equal(t, fifos.Config, expected.Config)

	files, err := os.ReadDir(root)
	assert.NoError(t, err)
	assert.Len(t, files, 1)

	assert.Nil(t, fifos.Close())
	files, err = os.ReadDir(root)
	assert.NoError(t, err)
	assert.Len(t, files, 0)
}

func TestNewAttach(t *testing.T) {
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
	assert.NoError(t, err)

	attachedFifos, err := attacher(fifos)
	assert.NoError(t, err)
	defer attachedFifos.Close()

	producers := setupFIFOProducers(t, attachedFifos.Config())
	initProducers(t, producers, expectedStdout, expectedStderr)

	actualStdin, err := io.ReadAll(producers.Stdin)
	assert.NoError(t, err)

	attachedFifos.Wait()
	attachedFifos.Cancel()
	assert.Nil(t, attachedFifos.Close())

	assert.Equal(t, expectedStdout, stdout.String())
	assert.Equal(t, expectedStderr, stderr.String())
	assert.Equal(t, expectedStdin, string(actualStdin))
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
	assert.NoError(t, err)

	pipes.Stdout, err = fifo.OpenFifo(ctx, fifos.Stdout, syscall.O_WRONLY, 0)
	assert.NoError(t, err)

	pipes.Stderr, err = fifo.OpenFifo(ctx, fifos.Stderr, syscall.O_WRONLY, 0)
	assert.NoError(t, err)

	return pipes
}

func initProducers(t *testing.T, producers producers, stdout, stderr string) {
	_, err := producers.Stdout.Write([]byte(stdout))
	assert.NoError(t, err)
	assert.Nil(t, producers.Stdout.Close())

	_, err = producers.Stderr.Write([]byte(stderr))
	assert.NoError(t, err)
	assert.Nil(t, producers.Stderr.Close())
}

func TestLogURIGenerator(t *testing.T) {
	baseTestLogURIGenerator(t, []LogURIGeneratorTestCase{
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
			err:    "must be absolute",
		},
		{
			scheme: "binary",
			path:   "C:\\path\\to\\binary",
			// NOTE: Windows paths should not be parse-able outside of Windows:
			err: "must be absolute",
		},
	})
}
