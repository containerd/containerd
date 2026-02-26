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

package process

import (
	"context"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/stdio"
)

type mockRIO struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
}

func (m *mockRIO) Stdin() io.WriteCloser { return m.stdin }
func (m *mockRIO) Stdout() io.ReadCloser { return m.stdout }
func (m *mockRIO) Stderr() io.ReadCloser { return m.stderr }
func (m *mockRIO) Close() error          { return nil }
func (m *mockRIO) Set(*exec.Cmd)         {}

func FuzzCopyPipes(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		ff := fuzz.NewConsumer(data)

		tmpDir := t.TempDir()

		p1, _ := ff.GetString()
		if p1 == "" {
			p1 = "stdout"
		}
		stdoutPath := filepath.Join(tmpDir, p1)

		p2, _ := ff.GetString()
		if p2 == "" {
			p2 = "stderr"
		}
		stderrPath := filepath.Join(tmpDir, p2)

		p3, _ := ff.GetString()
		if p3 == "" {
			p3 = "stdin"
		}
		stdinPath := filepath.Join(tmpDir, p3)

		// Create dummy files
		_ = os.MkdirAll(filepath.Dir(stdoutPath), 0700)
		_ = os.WriteFile(stdoutPath, nil, 0600)
		_ = os.MkdirAll(filepath.Dir(stderrPath), 0700)
		_ = os.WriteFile(stderrPath, nil, 0600)
		_ = os.MkdirAll(filepath.Dir(stdinPath), 0700)
		_ = os.WriteFile(stdinPath, nil, 0600)

		// Mock RIO using Pipes
		stdoutR, stdoutW := io.Pipe()
		stderrR, stderrW := io.Pipe()
		stdinR, stdinW := io.Pipe()

		rio := &mockRIO{
			stdin:  stdinW,
			stdout: stdoutR,
			stderr: stderrR,
		}

		stdoutData, _ := ff.GetBytes()
		stderrData, _ := ff.GetBytes()

		// Provide data to rio and close them promptly
		go func() {
			stdoutW.Write(stdoutData)
			stdoutW.Close()
		}()
		go func() {
			stderrW.Write(stderrData)
			stderrW.Close()
		}()
		go func() {
			io.Copy(io.Discard, stdinR)
			stdinR.Close()
		}()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var wg sync.WaitGroup
		var cwg sync.WaitGroup

		_ = copyPipes(ctx, rio, stdinPath, stdoutPath, stderrPath, &wg, &cwg)
		cwg.Wait()

		// Wait for copy goroutines to finish
		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
		}

		// Ensure everything is closed
		stdoutW.Close()
		stderrW.Close()
		stdinW.Close()
		stdoutR.Close()
		stderrR.Close()
		stdinR.Close()
	})
}

func FuzzCreateIO(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		ff := fuzz.NewConsumer(data)

		id, _ := ff.GetString()
		uid, _ := ff.GetInt()
		gid, _ := ff.GetInt()

		var std stdio.Stdio
		if err := ff.GenerateStruct(&std); err != nil {
			return
		}

		tmpDir := t.TempDir()

		// Create paths within tmpDir for any relative paths generated
		if std.Stdin != "" && !filepath.IsAbs(std.Stdin) {
			std.Stdin = filepath.Join(tmpDir, std.Stdin)
		}
		if std.Stdout != "" && !filepath.IsAbs(std.Stdout) {
			std.Stdout = filepath.Join(tmpDir, std.Stdout)
		}
		if std.Stderr != "" && !filepath.IsAbs(std.Stderr) {
			std.Stderr = filepath.Join(tmpDir, std.Stderr)
		}

		// Ensure we don't accidentally execute random binaries from fuzzer input in NewBinaryIO
		u, err := url.Parse(std.Stdout)
		if err == nil && u.Scheme == "binary" {
			if _, err := os.Stat("/bin/true"); err == nil {
				u.Path = "/bin/true"
				std.Stdout = u.String()
			} else {
				// If /bin/true is not available, just use a dummy path
				u.Path = "/dev/null"
				std.Stdout = u.String()
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		// Set namespace for NewBinaryIO
		ctx = namespaces.WithNamespace(ctx, "fuzz")

		pio, err := createIO(ctx, id, uid, gid, std)
		if err == nil && pio != nil {
			pio.Close()
		}
	})
}
