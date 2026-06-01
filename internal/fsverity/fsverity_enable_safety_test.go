//go:build linux

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

package fsverity

import (
	"os"
	"path/filepath"
	"testing"
)

// Characterization test for the contract: Enable must close the file descriptor
// it opens before returning. This test must PASS both before and after any
// refactor; it currently FAILS on unpatched code, which is the reproduction of
// the fd-leak bug.
//
// Background:
//   Enable (fsverity_linux.go:97) does `f, err := os.Open(path)` and never closes
//   f -- neither on the FS_IOC_ENABLE_VERITY error path (line 126) nor on the
//   success path (line 129). The sibling IsEnabled (lines 76-95) shows the
//   intended pattern: `f, _ := os.Open(path); defer f.Close()`.
//
// Faithful, recommended-usage reproduction (no artificial concurrency, no root,
// no fsverity-capable filesystem required):
//   The production content-store hot path calls Enable exactly once per committed
//   blob -- plugins/content/local/writer.go:165-168 -- and, per its own comment
//   ("errors should only be logged and not returned"), tolerates a returned error
//   by logging a warning. On a filesystem without fsverity support the ioctl fails
//   and Enable returns that error, but the descriptor opened on line 98 has already
//   leaked. The success path leaks the same descriptor, so observing the error path
//   is a faithful in-production reproduction of the identical leak.
//
// Note on GC: we deliberately do NOT call runtime.GC(). *os.File carries a
// finalizer that would eventually close a leaked fd, so the leak is not unbounded
// forever -- but Go's GC is driven by heap growth, not fd pressure, so a daemon
// committing many blobs accumulates open fds between GC cycles. This test measures
// that accumulation within a single commit burst, which is the real production
// failure mode (e.g. a node pulling many multi-layer images at once exhausting
// RLIMIT_NOFILE).

func openFDCount(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatalf("read /proc/self/fd: %v", err)
	}
	return len(ents)
}

func TestEnableSafety_NoFDLeak(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blob")
	if err := os.WriteFile(path, []byte("blob-contents"), 0o644); err != nil {
		t.Fatalf("write test blob: %v", err)
	}

	// Mirror a content-store commit burst: each committed blob calls Enable once.
	// The return value is intentionally ignored, exactly like writer.go which only
	// logs on error.
	const commits = 64

	before := openFDCount(t)
	for range commits {
		_ = Enable(path)
	}
	after := openFDCount(t)

	// A correct Enable closes every descriptor it opens, so the open-fd count must
	// stay flat regardless of how many times it is called. Small slack covers
	// unrelated runtime descriptors.
	const slack = 8
	if grew := after - before; grew > slack {
		t.Fatalf("fd leak: open fd count grew by %d across %d Enable calls "+
			"(before=%d after=%d); Enable opens a descriptor per call and never closes it",
			grew, commits, before, after)
	}
}
