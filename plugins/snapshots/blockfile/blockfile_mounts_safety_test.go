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

package blockfile

// Characterization + faithful-reproduction tests for P2-6.
//
// (*snapshotter).mounts aliases o.options and appends the per-snapshot ro/rw
// flag in place (blockfile.go:426-434). When o.options has spare capacity, that
// append writes into the backing array shared with o.options, so successive or
// concurrent mounts() calls clobber each other's returned options — a data race
// plus ro/rw flag cross-contamination.
//
// mounts() is the common funnel of the exported Mounts/Prepare/View handlers,
// which the snapshots gRPC service drives concurrently with no lock; these tests
// exercise that real function directly.
//
// All tests below MUST stay green after the fix (decouple from o.options via
// slices.Clone). The two reproduction tests fail on the pre-fix code:
//   - NoCrossContamination: deterministic, fails in normal mode.
//   - ConcurrentNoRace:      fails under -race (DATA RACE).

import (
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
)

// spareCapOptions reproduces the exact slice shape NewSnapshotter produces when
// the operator configures e.g. `mount_options = ["discard","nobarrier"]` without
// "loop": NewSnapshotter appends "loop" (blockfile.go:164-165), growing the slice
// to len=3,cap=4 — one spare slot at index 3 that mounts()'s append writes into
// in place. The default config (["loop"], len=1/cap=1) has no spare capacity and
// is unaffected, which is why this hazard is config-gated.
func spareCapOptions(t *testing.T) []string {
	t.Helper()
	opts := []string{"discard", "nobarrier"}
	if !slices.Contains(opts, "loop") {
		opts = append(opts, "loop")
	}
	if cap(opts) <= len(opts) {
		// The runtime did not over-allocate on this build; force the
		// production-observed len=3/cap=4 shape so the precondition holds.
		grown := make([]string, len(opts), len(opts)+1)
		copy(grown, opts)
		opts = grown
	}
	return opts
}

// TestBlockfileMountsSafety_KindFlagAndBaseOptions locks the observable contract
// of a single mounts() call: base options are preserved and the trailing flag
// matches the snapshot kind (View => ro, Active => rw). Holds before and after
// the fix.
func TestBlockfileMountsSafety_KindFlagAndBaseOptions(t *testing.T) {
	cases := []struct {
		name     string
		kind     snapshots.Kind
		wantFlag string
	}{
		{"view-is-ro", snapshots.KindView, "ro"},
		{"active-is-rw", snapshots.KindActive, "rw"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := &snapshotter{root: t.TempDir(), fsType: "ext4", options: spareCapOptions(t)}
			mounts := o.mounts(storage.Snapshot{Kind: tc.kind, ID: "42"})
			if len(mounts) != 1 {
				t.Fatalf("expected 1 mount, got %d", len(mounts))
			}
			opts := mounts[0].Options
			for _, base := range []string{"discard", "nobarrier", "loop"} {
				if !slices.Contains(opts, base) {
					t.Errorf("base option %q missing from %v", base, opts)
				}
			}
			if got := opts[len(opts)-1]; got != tc.wantFlag {
				t.Errorf("last option = %q, want %q (opts=%v)", got, tc.wantFlag, opts)
			}
			if mounts[0].Type != "ext4" {
				t.Errorf("mount type = %q, want ext4", mounts[0].Type)
			}
		})
	}
}

// TestBlockfileMountsSafety_NoCrossContamination is a deterministic reproduction
// of the ro/rw cross-contamination. A read-only View mount must stay read-only
// even after a subsequent read-write (Active) mount is built from the same
// snapshotter. Pre-fix the second append writes the shared backing slot, flipping
// the already-returned View mount's flag to "rw". Fails in normal mode pre-fix.
func TestBlockfileMountsSafety_NoCrossContamination(t *testing.T) {
	o := &snapshotter{root: t.TempDir(), fsType: "ext4", options: spareCapOptions(t)}

	view := o.mounts(storage.Snapshot{Kind: snapshots.KindView, ID: "view"})
	if got := view[0].Options[len(view[0].Options)-1]; got != "ro" {
		t.Fatalf("precondition: view mount last option = %q, want ro", got)
	}

	// A later read-write mount from the same snapshotter must not mutate the
	// View mount that was already handed out.
	_ = o.mounts(storage.Snapshot{Kind: snapshots.KindActive, ID: "active"})

	if got := view[0].Options[len(view[0].Options)-1]; got != "ro" {
		t.Errorf("View mount flag corrupted to %q after a subsequent Active mount; "+
			"mounts() aliased o.options and appended the flag in place (P2-6)", got)
	}
}

// TestBlockfileMountsSafety_ConcurrentNoRace reproduces the data race under
// -race: many goroutines call mounts() concurrently (mixed kinds), all appending
// the flag into the spare slot of the shared o.options backing array. The gRPC
// snapshots service drives Mounts/Prepare/View concurrently with no lock, so this
// matches the production topology. Fails under -race pre-fix (DATA RACE).
func TestBlockfileMountsSafety_ConcurrentNoRace(t *testing.T) {
	o := &snapshotter{root: t.TempDir(), fsType: "ext4", options: spareCapOptions(t)}

	const goroutines = 64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := range goroutines {
		kind := snapshots.KindView
		if i%2 == 0 {
			kind = snapshots.KindActive
		}
		go func(k snapshots.Kind, id string) {
			defer wg.Done()
			m := o.mounts(storage.Snapshot{Kind: k, ID: id})
			_ = m[0].Options[len(m[0].Options)-1]
		}(kind, "snap"+strconv.Itoa(i))
	}
	wg.Wait()
}
