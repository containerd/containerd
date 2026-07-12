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

package testsuite

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/snapshots"
)

// ---------------------------------------------------------------------------
// Regression checks for historical layering bugs.
// ---------------------------------------------------------------------------

// fsCheckLayerFileUpdate updates a single file across layers.
// See https://github.com/docker/docker/issues/21555
func fsCheckLayerFileUpdate(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	l1 := []fsChange{
		fsMkdir("/etc", 0700),
		fsCreateFile("/etc/hosts", []byte("mydomain 10.0.0.1"), 0644),
		fsCreateFile("/etc/profile", []byte("PATH=/usr/bin"), 0644),
	}
	l2 := []fsChange{
		fsCreateFile("/etc/hosts", []byte("mydomain 10.0.0.2"), 0644),
		fsCreateFile("/etc/profile", []byte("PATH=/usr/bin"), 0666),
		fsMkdir("/root", 0700),
		fsCreateFile("/root/.bashrc", []byte("PATH=/usr/sbin:/usr/bin"), 0644),
	}

	// Run repeatedly to catch timestamp-related regressions.
	var sleepTime time.Duration
	for range 5 {
		time.Sleep(sleepTime)
		if err := fsCheckSnapshots(ctx, sn, work, l1, l2); err != nil {
			t.Fatalf("check snapshots: %+v", err)
		}
		next := time.Now()
		sleepTime = time.Unix(next.Unix()+1, 0).Sub(next)
	}
}

// fsCheckRemoveDirectoryInLowerLayer removes and recreates a directory.
// See https://github.com/docker/docker/issues/25244
func fsCheckRemoveDirectoryInLowerLayer(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	l1 := []fsChange{
		fsMkdir("/lib", 0700),
		fsCreateFile("/lib/hidden", []byte{}, 0644),
	}
	// Removing then recreating /lib hides the lower-layer content beneath it.
	l2 := []fsChange{
		fsRemove("/lib"),
		fsMkdir("/lib", 0700),
		fsCreateFile("/lib/not-hidden", []byte{}, 0644),
	}
	l3 := []fsChange{
		fsCreateFile("/lib/newfile", []byte{}, 0644),
	}

	if err := fsCheckSnapshots(ctx, sn, work, l1, l2, l3); err != nil {
		t.Fatalf("check snapshots: %+v", err)
	}
}

// fsCheckChown propagates ownership changes across layers.
// See https://github.com/docker/docker/issues/20240, 24913, 28391
func fsCheckChown(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	if runtime.GOOS == "windows" {
		t.Skip("Chown is not supported on Windows")
	}
	l1 := []fsChange{
		fsMkdir("/opt", 0700),
		fsMkdir("/opt/a", 0700),
		fsMkdir("/opt/a/b", 0700),
		fsCreateFile("/opt/a/b/file.txt", []byte("hello"), 0644),
	}
	l2 := []fsChange{
		fsChown("/opt", 1, 1),
		fsChown("/opt/a", 1, 1),
		fsChown("/opt/a/b", 1, 1),
		fsChown("/opt/a/b/file.txt", 1, 1),
	}

	if err := fsCheckSnapshots(ctx, sn, work, l1, l2); err != nil {
		t.Fatalf("check snapshots: %+v", err)
	}
}

// fsCheckDirectoryPermissionOnCommit tracks directory permissions across layers.
// See https://github.com/docker/docker/issues/27298
func fsCheckDirectoryPermissionOnCommit(ctx context.Context, t *testing.T, sn snapshots.Snapshotter, work string) {
	if runtime.GOOS == "windows" {
		t.Skip("Chown is not supported on WCOW")
	}
	l1 := []fsChange{
		fsMkdir("/dir1", 0700),
		fsMkdir("/dir2", 0700),
		fsMkdir("/dir3", 0700),
		fsMkdir("/dir4", 0700),
		fsCreateFile("/dir4/f1", []byte("..."), 0644),
		fsMkdir("/dir5", 0700),
		fsCreateFile("/dir5/f1", []byte("..."), 0644),
		fsChown("/dir1", 1, 1),
		fsChown("/dir2", 1, 1),
		fsChown("/dir3", 1, 1),
		fsChown("/dir5", 1, 1),
		fsChown("/dir5/f1", 1, 1),
	}
	l2 := []fsChange{
		fsChown("/dir2", 0, 0),
		fsRemove("/dir3"),
		fsChown("/dir4", 1, 1),
		fsChown("/dir4/f1", 1, 1),
	}
	l3 := []fsChange{
		fsMkdir("/dir3", 0700),
		fsChown("/dir3", 1, 1),
		fsRemove("/dir5"),
		fsMkdir("/dir5", 0700),
		fsChown("/dir5", 1, 1),
	}

	if err := fsCheckSnapshots(ctx, sn, work, l1, l2, l3); err != nil {
		t.Fatalf("check snapshots: %+v", err)
	}
}
