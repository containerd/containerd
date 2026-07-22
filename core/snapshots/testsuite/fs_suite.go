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

// Package testsuite provides a snapshot test suite that inspects snapshots
// through the fs.FS interface instead of kernel mounts.
//
// SnapshotterFSSuite runs the same logical checks as SnapshotterSuite but:
//   - Reads committed and view snapshots via fsview.FSMounts as an fs.FS,
//     never calling mount.All or any mount syscall.
//   - Describes each layer's changes once, then applies them both as a layered
//     snapshot and as a flattened snapshot, comparing the two fs.FS views.
//
// The suite itself is root-agnostic. The erofs snapshotter runs it rootless on
// any platform, since layers are built as EROFS images. The overlay and native
// snapshotters write to real directories and therefore need privileges for
// operations like chown and whiteout creation; their callers add the
// appropriate root requirement.
package testsuite

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/log/logtest"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/namespaces"
)

// fsSuiteConfig holds configuration for the fs.FS test suite.
type fsSuiteConfig struct {
	skipTests map[string]bool
}

// FSSuiteOpt configures SnapshotterFSSuite.
type FSSuiteOpt func(*fsSuiteConfig)

// FSSuiteSkipTests skips the named tests.
func FSSuiteSkipTests(names ...string) FSSuiteOpt {
	return func(o *fsSuiteConfig) {
		for _, n := range names {
			o.skipTests[n] = true
		}
	}
}

// SnapshotterFSSuite runs the fs.FS-based snapshot test suite against the
// snapshotter produced by snapshotterFn. snapshotterFn matches the signature
// used by SnapshotterSuite so a factory can be shared between both suites.
func SnapshotterFSSuite(t *testing.T, name string, snapshotterFn SnapshotterFunc, opts ...FSSuiteOpt) {
	t.Helper()

	// Zero the umask so directory-backed snapshotters (overlay, native) write
	// the exact permissions requested; the erofs path is unaffected as it sets
	// modes directly in the image.
	restore := clearMask()
	t.Cleanup(restore)

	o := &fsSuiteConfig{skipTests: make(map[string]bool)}
	for _, opt := range opts {
		opt(o)
	}

	makeT := func(testName string, fn func(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string)) func(*testing.T) {
		return func(t *testing.T) {
			t.Helper()
			if o.skipTests[testName] {
				t.Skipf("test %q skipped for snapshotter %q", testName, name)
			}
			fsMakeTest(name, snapshotterFn, fn)(t)
		}
	}

	t.Run("Basic", makeT("Basic", fsCheckSnapshotterBasic))
	t.Run("StatActive", makeT("StatActive", fsCheckSnapshotterStatActive))
	t.Run("StatCommitted", makeT("StatCommitted", fsCheckSnapshotterStatCommitted))
	t.Run("TransitivityTest", makeT("TransitivityTest", fsCheckSnapshotterTransitivity))
	t.Run("PrepareViewFailingTest", makeT("PrepareViewFailingTest", fsCheckSnapshotterPrepareView))
	t.Run("Update", makeT("Update", fsCheckUpdate))
	t.Run("Remove", makeT("Remove", fsCheckRemove))
	t.Run("Walk", makeT("Walk", fsCheckWalk))

	t.Run("LayerFileUpdate", makeT("LayerFileUpdate", fsCheckLayerFileUpdate))
	t.Run("RemoveDirectoryInLowerLayer", makeT("RemoveDirectoryInLowerLayer", fsCheckRemoveDirectoryInLowerLayer))
	t.Run("Chown", makeT("Chown", fsCheckChown))
	t.Run("DirectoryPermissionOnCommit", makeT("DirectoryPermissionOnCommit", fsCheckDirectoryPermissionOnCommit))
	t.Run("RemoveIntermediateSnapshot", makeT("RemoveIntermediateSnapshot", fsCheckRemoveIntermediateSnapshot))
	t.Run("DeletedFilesInChildSnapshot", makeT("DeletedFilesInChildSnapshot", fsCheckDeletedFilesInChildSnapshot))
	t.Run("MoveFileFromLowerLayer", makeT("MoveFileFromLowerLayer", fsCheckFileFromLowerLayer))

	t.Run("ViewReadonly", makeT("ViewReadonly", fsCheckSnapshotterViewReadonly))

	t.Run("StatInWalk", makeT("StatInWalk", fsCheckStatInWalk))
	t.Run("CloseTwice", makeT("CloseTwice", fsCloseTwice))
	t.Run("RootPermission", makeT("RootPermission", fsCheckRootPermission))

	t.Run("Rename", makeT("Rename", fsCheckRename(name)))
	t.Run("128Layers", makeT("128Layers", fsCheck128Layers(name)))
}

// fsMakeTest sets up a temporary root and work directory, constructs the
// snapshotter, and runs fn. It mirrors makeTest but omits the mount manager.
func fsMakeTest(
	snapshotter string,
	snapshotterFn SnapshotterFunc,
	fn func(ctx context.Context, t *testing.T, snapshotter snapshots.Snapshotter, work string),
) func(t *testing.T) {
	return func(t *testing.T) {
		t.Parallel()

		ctx := logtest.WithT(context.Background(), t)
		ctx = namespaces.WithNamespace(ctx, "fssuite")

		tmpDir, err := os.MkdirTemp("", "fs-suite-"+snapshotter+"-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		root := filepath.Join(tmpDir, "root")
		if err := os.MkdirAll(root, 0777); err != nil {
			t.Fatal(err)
		}

		sn, cleanup, err := snapshotterFn(ctx, root)
		if err != nil {
			t.Fatalf("Failed to initialize snapshotter: %+v", err)
		}
		defer func() {
			if cleanup != nil {
				if err := cleanup(); err != nil {
					t.Errorf("Cleanup failed: %v", err)
				}
			}
		}()

		work := filepath.Join(tmpDir, "work")
		if err := os.MkdirAll(work, 0777); err != nil {
			t.Fatal(err)
		}

		fn(ctx, t, sn, work)
	}
}

// fsOpt is the standard gc-root label option used across the suite.
var fsOpt = snapshots.WithLabels(map[string]string{
	"containerd.io/gc.root": time.Now().UTC().Format(time.RFC3339),
})
