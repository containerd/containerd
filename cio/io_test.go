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
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/containerd/fifo"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/gotestyourself/gotestyourself/assert"
	is "github.com/gotestyourself/gotestyourself/assert/cmp"
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

	root, err := ioutil.TempDir("", "test-new-fifo-set")
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

	files, err := ioutil.ReadDir(root)
	assert.NilError(t, err)
	assert.Check(t, is.Len(files, 1))

	assert.NilError(t, fifos.Close())
	files, err = ioutil.ReadDir(root)
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

	io, err := attacher(fifos)
	assert.NilError(t, err)
	defer io.Close()

	producers := setupFIFOProducers(t, io.Config())
	initProducers(t, producers, expectedStdout, expectedStderr)

	actualStdin, err := ioutil.ReadAll(producers.Stdin)
	assert.NilError(t, err)

	io.Wait()
	io.Cancel()
	assert.NilError(t, io.Close())

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
