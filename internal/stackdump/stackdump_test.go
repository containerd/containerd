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

package stackdump

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestDump(t *testing.T) {
	buf := Dump()
	if len(buf) == 0 {
		t.Fatal("Dump() returned no data")
	}
	got := string(buf)
	if !strings.Contains(got, "goroutine ") {
		t.Fatalf("Dump() does not look like a stack dump:\n%s", got)
	}
	// The dump covers all goroutines, so the calling test must appear in it.
	if !strings.Contains(got, "TestDump") {
		t.Fatalf("Dump() does not contain the calling goroutine:\n%s", got)
	}
}

// TestDumpGrowsBufferBeyondInitialSize parks enough goroutines that the dump
// cannot fit in the initial 16KiB buffer, covering the grow loop. A truncated
// dump is worse than useless during an incident, so this is the behavior that
// matters most in Dump.
func TestDumpGrowsBufferBeyondInitialSize(t *testing.T) {
	const parked = 400

	release := make(chan struct{})
	var running, done sync.WaitGroup
	running.Add(parked)
	done.Add(parked)
	for range parked {
		go func() {
			defer done.Done()
			running.Done()
			<-release
		}()
	}
	running.Wait()
	defer func() {
		close(release)
		done.Wait()
	}()

	buf := Dump()
	if len(buf) <= 16384 {
		t.Fatalf("Dump() returned %d bytes; expected the buffer to grow past the initial 16384", len(buf))
	}
	// Every parked goroutine must be present, not just enough to overflow.
	if got := strings.Count(string(buf), "TestDumpGrowsBufferBeyondInitialSize.func"); got < parked {
		t.Fatalf("Dump() contains %d parked goroutines; want at least %d (dump was truncated)", got, parked)
	}
	if n := runtime.NumGoroutine(); n < parked {
		t.Fatalf("only %d goroutines running; test did not set up as expected", n)
	}
}

// setTempDir points os.TempDir at dir for the duration of the test.
//
// os.TempDir consults TMPDIR on unix and TMP/TEMP on Windows, but Windows routes
// through GetTempPath2, which ignores both for a process running as SYSTEM. Skip
// rather than assert against a directory the override never reached.
func setTempDir(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("TMPDIR", dir)
	t.Setenv("TMP", dir)
	t.Setenv("TEMP", dir)
	if got := os.TempDir(); got != dir {
		t.Skipf("os.TempDir() is %q, not the %q this test set", got, dir)
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	setTempDir(t, dir)

	want := []byte("goroutine 1 [running]:\nmain.main()\n")
	name, err := WriteFile("test-prefix", want)
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	wantName := filepath.Join(dir, fmt.Sprintf("test-prefix.%d.stacks.log", os.Getpid()))
	if name != wantName {
		t.Fatalf("WriteFile() = %q; want %q", name, wantName)
	}
	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("failed to read back %s: %v", name, err)
	}
	if string(got) != string(want) {
		t.Fatalf("file contents = %q; want %q", got, want)
	}
}

// TestWriteFileOverwrites covers a second dump in the same process: the file is
// named per-pid, so it must be replaced rather than appended to or left stale.
func TestWriteFileOverwrites(t *testing.T) {
	setTempDir(t, t.TempDir())

	if _, err := WriteFile("test-prefix", []byte("first dump, the longer one")); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	want := []byte("second")
	name, err := WriteFile("test-prefix", want)
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("failed to read back %s: %v", name, err)
	}
	if string(got) != string(want) {
		t.Fatalf("file contents = %q; want %q", got, want)
	}
}

// TestWriteFileDoesNotFollowSymlink covers a symlink planted at the dump's
// predictable path in a shared temp directory. Writing through it would let an
// unprivileged user choose a file for a root process to truncate.
func TestWriteFileDoesNotFollowSymlink(t *testing.T) {
	dir := t.TempDir()
	setTempDir(t, dir)

	victim := filepath.Join(t.TempDir(), "victim")
	const sentinel = "untouched"
	if err := os.WriteFile(victim, []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	target := filepath.Join(dir, fmt.Sprintf("test-prefix.%d.stacks.log", os.Getpid()))
	if err := os.Symlink(victim, target); err != nil {
		t.Skipf("cannot create symlinks here: %v", err)
	}

	name, err := WriteFile("test-prefix", []byte("dump"))
	if err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	got, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sentinel {
		t.Fatalf("wrote through the symlink: target now contains %q", got)
	}
	// The dump must still land at the documented path.
	if b, err := os.ReadFile(name); err != nil || string(b) != "dump" {
		t.Fatalf("dump not written to %s: %q, %v", name, b, err)
	}
}

func TestWriteFileReturnsErrorOnUnwritableDir(t *testing.T) {
	// Point the temp dir at a path that cannot hold files so the caller's
	// error handling is exercised rather than a silent failure.
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	setTempDir(t, notADir)

	name, err := WriteFile("test-prefix", []byte("dump"))
	if err == nil {
		t.Fatalf("WriteFile() = %q, nil; want an error", name)
	}
	if name != "" {
		t.Fatalf("WriteFile() returned path %q alongside an error; want \"\"", name)
	}
}
