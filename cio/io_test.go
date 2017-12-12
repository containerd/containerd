// +build !windows

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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	defer os.RemoveAll(root)

	fifos, err := NewFIFOSetInDir(root, "theid", true)
	require.NoError(t, err)

	assertHasPrefix(t, fifos.Stdin, root)
	assertHasPrefix(t, fifos.Stdout, root)
	assertHasPrefix(t, fifos.Stderr, root)
	assert.Equal(t, "theid-stdin", filepath.Base(fifos.Stdin))
	assert.Equal(t, "theid-stdout", filepath.Base(fifos.Stdout))
	assert.Equal(t, "theid-stderr", filepath.Base(fifos.Stderr))
	assert.Equal(t, true, fifos.Terminal)

	files, err := ioutil.ReadDir(root)
	require.NoError(t, err)
	assert.Len(t, files, 1)

	require.NoError(t, fifos.Close())
	files, err = ioutil.ReadDir(root)
	require.NoError(t, err)
	assert.Len(t, files, 0)
}

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
	require.NoError(t, err)

	io, err := attacher(fifos)
	require.NoError(t, err)
	defer io.Close()

	producers := setupFIFOProducers(t, io.Config())
	initProducers(t, producers, expectedStdout, expectedStderr)

	actualStdin, err := ioutil.ReadAll(producers.Stdin)
	require.NoError(t, err)

	io.Cancel()
	io.Wait()
	assert.NoError(t, io.Close())

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
	require.NoError(t, err)

	pipes.Stdout, err = fifo.OpenFifo(ctx, fifos.Stdout, syscall.O_WRONLY, 0)
	require.NoError(t, err)

	pipes.Stderr, err = fifo.OpenFifo(ctx, fifos.Stderr, syscall.O_WRONLY, 0)
	require.NoError(t, err)

	return pipes
}

func initProducers(t *testing.T, producers producers, stdout, stderr string) {
	_, err := producers.Stdout.Write([]byte(stdout))
	require.NoError(t, err)
	require.NoError(t, producers.Stdout.Close())

	_, err = producers.Stderr.Write([]byte(stderr))
	require.NoError(t, err)
	require.NoError(t, producers.Stderr.Close())
}
