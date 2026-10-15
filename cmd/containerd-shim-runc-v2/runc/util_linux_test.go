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

package runc

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/console"
	"github.com/containerd/fifo"
	runcC "github.com/containerd/go-runc"
)

func TestExitStatusFile(t *testing.T) {
	now := time.Now().Round(0).UTC()

	for _, tc := range []struct {
		name     string
		input    runcC.Exit
		corrupt  func(*testing.T, string)
		expected runcC.Exit
		hasErr   bool
	}{
		{
			name:   "invalid pid",
			input:  runcC.Exit{Pid: -1, Timestamp: time.Now(), Status: 1},
			hasErr: true,
		},
		{
			name:   "invalid status",
			input:  runcC.Exit{Pid: 10, Timestamp: time.Now(), Status: -1},
			hasErr: true,
		},
		{
			name:   "empty timestamp",
			input:  runcC.Exit{Pid: 10, Status: 0},
			hasErr: true,
		},
		{
			name:     "no error",
			input:    runcC.Exit{Pid: 10, Timestamp: now, Status: 0},
			expected: runcC.Exit{Pid: 10, Timestamp: now, Status: 0},
		},
		{
			name:  "file corrupted",
			input: runcC.Exit{Pid: 10, Timestamp: now, Status: 0},
			corrupt: func(t *testing.T, bundlePath string) {
				target := filepath.Join(bundlePath, exitStatusFileName)
				err := os.WriteFile(target, []byte("{oops:...}"), 0600)
				if err != nil {
					t.Fatalf("failed to corrupt file: %v", err)
				}
			},
			hasErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			if err := WriteExitStatus(tmpDir, tc.input); err != nil {
				t.Fatalf("failed to write exit status: %v", err)
			}

			if tc.corrupt != nil {
				tc.corrupt(t, tmpDir)
			}

			got, err := ReadExitStatus(tmpDir)
			if tc.hasErr {
				if err == nil {
					t.Fatal("expected error but got nil")
				}
				return
			}
			if !reflect.DeepEqual(tc.expected, got) {
				t.Fatalf("expected %v, but got %v", tc.expected, got)
			}
		})
	}
}

func TestCopyConsoleWithEmptyStdin(t *testing.T) {
	p, err := NewPlatform()
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	tmpDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	stdinPath := filepath.Join(tmpDir, "stdin")
	stdoutPath := filepath.Join(tmpDir, "stdout")

	stdinW, err := fifo.OpenFifo(ctx, stdinPath, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer stdinW.Close()

	stdoutR, err := fifo.OpenFifo(ctx, stdoutPath, syscall.O_RDONLY|syscall.O_CREAT|syscall.O_NONBLOCK, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer stdoutR.Close()

	var stdout bytes.Buffer
	stdoutDone := make(chan error, 1)
	go func() {
		_, err := stdout.ReadFrom(stdoutR)
		_ = stdoutR.Close()
		stdoutDone <- err
	}()

	cons, slavePath, err := console.NewPty()
	if err != nil {
		t.Fatal(err)
	}

	slave, err := os.OpenFile(slavePath, os.O_RDWR, 0)
	if err != nil {
		cons.Close()
		t.Fatal(err)
	}

	cmd := exec.CommandContext(ctx, "seq", "-s", " ", "100000")
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
		Ctty:    0,
	}

	var wg sync.WaitGroup
	epollCons, err := p.CopyConsole(ctx, cons, "exec", stdinPath, stdoutPath, "", &wg)
	if err != nil {
		slave.Close()
		cons.Close()
		t.Fatal(err)
	}
	defer epollCons.Close()

	if err := cmd.Start(); err != nil {
		slave.Close()
		t.Fatal(err)
	}
	slave.Close()

	if err := stdinW.Close(); err != nil {
		t.Fatal(err)
	}

	waitErr := cmd.Wait()
	if err := p.ShutdownConsole(ctx, epollCons); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	<-stdoutDone

	const expectedSuffix = "99999 100000"
	outStr := stdout.String()
	suffix := outStr
	if len(suffix) > 20 {
		suffix = suffix[len(suffix)-20:]
	}
	if waitErr != nil {
		t.Fatalf("process failed: %v (waitStatus=%v), last 20 chars: %q", waitErr, cmd.ProcessState.Sys(), suffix)
	}
	if !strings.Contains(outStr, expectedSuffix) {
		t.Fatalf("output does not contain %q, last 20 chars: %q", expectedSuffix, suffix)
	}
}
