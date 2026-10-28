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

package overlay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/diff"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/metadata"
	"github.com/containerd/containerd/v2/core/mount"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"github.com/containerd/containerd/v2/core/snapshots/testsuite"
	"github.com/containerd/containerd/v2/core/unpack"
	"github.com/containerd/containerd/v2/internal/userns"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	"github.com/containerd/containerd/v2/pkg/testutil"
	"github.com/containerd/containerd/v2/plugins/content/local"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay/overlayutils"
	"github.com/containerd/errdefs"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/sync/semaphore"
)

func newSnapshotterWithOpts(opts ...Opt) testsuite.SnapshotterFunc {
	return func(ctx context.Context, root string) (snapshots.Snapshotter, func() error, error) {
		snapshotter, err := NewSnapshotter(root, opts...)
		if err != nil {
			return nil, nil, err
		}

		return snapshotter, func() error { return snapshotter.Close() }, nil
	}
}

// NewSnapshotter must not auto-append "index=off" when the configuration
// already carries an index option: mount options are last-wins in the
// kernel, so the appended "index=off" would silently override a configured
// "index=on" — and combined with "nfs_export=on" it makes every overlay
// mount fail with EINVAL (nfs_export=on conflicts with an explicit
// index=off).
func TestOverlayConfiguredIndexNotOverridden(t *testing.T) {
	if !supportsIndex() {
		t.Skip("kernel does not expose the overlay index parameter")
	}
	sn, err := NewSnapshotter(t.TempDir(), WithMountOptions([]string{"index=on", "nfs_export=on"}))
	if err != nil {
		t.Fatal(err)
	}
	defer sn.Close()
	options := sn.(*snapshotter).options
	for _, opt := range options {
		if opt == "index=off" {
			t.Fatalf("configured index option overridden by auto-appended index=off (options: %v)", options)
		}
	}
}

func TestOverlay(t *testing.T) {
	testutil.RequiresRoot(t)
	optTestCases := map[string][]Opt{
		"no opt": nil,
		// default in init()
		"AsynchronousRemove": {AsynchronousRemove},
		// idmapped mounts enabled
		"WithRemapIDs": {WithRemapIDs},
	}

	for optsName, opts := range optTestCases {
		t.Run(optsName, func(t *testing.T) {
			newSnapshotter := newSnapshotterWithOpts(opts...)
			testsuite.SnapshotterSuite(t, "overlayfs", newSnapshotter)
			t.Run("TestOverlayRemappedBind", func(t *testing.T) {
				testOverlayRemappedBind(t, newSnapshotter)
			})
			t.Run("TestOverlayRemappedActive", func(t *testing.T) {
				testOverlayRemappedActive(t, newSnapshotter)
			})
			t.Run("TestOverlayRemappedInvalidMappings", func(t *testing.T) {
				testOverlayRemappedInvalidMapping(t, newSnapshotter)
			})
			t.Run("TestOverlayMounts", func(t *testing.T) {
				testOverlayMounts(t, newSnapshotter)
			})
			t.Run("TestOverlayCommit", func(t *testing.T) {
				testOverlayCommit(t, newSnapshotter)
			})
			t.Run("TestOverlayOverlayMount", func(t *testing.T) {
				testOverlayOverlayMount(t, newSnapshotter)
			})
			t.Run("TestOverlayOverlayRead", func(t *testing.T) {
				testOverlayOverlayRead(t, newSnapshotter)
			})
			t.Run("TestOverlayView", func(t *testing.T) {
				testOverlayView(t, newSnapshotterWithOpts(append(opts, WithMountOptions([]string{"volatile"}))...))
			})
		})
	}
}

func testOverlayMounts(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	mounts, err := o.Prepare(ctx, "/tmp/test", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 {
		t.Errorf("should only have 1 mount but received %d", len(mounts))
	}
	m := mounts[0]
	if m.Type != "bind" {
		t.Errorf("mount type should be bind but received %q", m.Type)
	}
	expected := filepath.Join(root, "snapshots", "1", "fs")
	if m.Source != expected {
		t.Errorf("expected source %q but received %q", expected, m.Source)
	}
	if m.Options[0] != "rw" {
		t.Errorf("expected mount option rw but received %q", m.Options[0])
	}
	if m.Options[1] != "rbind" {
		t.Errorf("expected mount option rbind but received %q", m.Options[1])
	}
}

func testOverlayCommit(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	key := "/tmp/test"
	mounts, err := o.Prepare(ctx, key, "")
	if err != nil {
		t.Fatal(err)
	}
	m := mounts[0]
	if err := os.WriteFile(filepath.Join(m.Source, "foo"), []byte("hi"), 0660); err != nil {
		t.Fatal(err)
	}
	if err := o.Commit(ctx, "base", key); err != nil {
		t.Fatal(err)
	}

	activeParent := "active-parent"
	if _, err := o.Prepare(ctx, activeParent, ""); err != nil {
		t.Fatal(err)
	}

	// test rebase
	testCases := []struct {
		oldParent string
		newParent string
		expError  bool
	}{
		{
			oldParent: "",
			newParent: "",
			expError:  false,
		},
		{
			oldParent: "",
			newParent: "base",
			expError:  false,
		},
		{
			oldParent: "base",
			newParent: "base",
			expError:  false,
		},
		{
			oldParent: "base",
			newParent: "",
			expError:  false,
		},
		{
			oldParent: "base",
			newParent: "new",
			expError:  true,
		},
		{
			oldParent: "",
			newParent: activeParent,
			expError:  true,
		},
	}
	for i, tc := range testCases {
		key := fmt.Sprintf("/tmp/test-%d", i)
		name := fmt.Sprintf("test-%d", i)
		_, err := o.Prepare(ctx, key, tc.oldParent)
		if err != nil {
			t.Fatal(err)
		}
		if err := o.Commit(ctx, name, key, snapshots.WithParent(tc.newParent)); err != nil {
			if !tc.expError {
				t.Fatal(err)
			}
			t.Logf("expected error received: %v", err)
		} else if tc.expError {
			t.Fatal("expected error but commit succeeded")
		}
	}
}

func testOverlayOverlayMount(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	key := "/tmp/test"
	if _, err = o.Prepare(ctx, key, ""); err != nil {
		t.Fatal(err)
	}
	if err := o.Commit(ctx, "base", key); err != nil {
		t.Fatal(err)
	}
	var mounts []mount.Mount
	if mounts, err = o.Prepare(ctx, "/tmp/layer2", "base"); err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 {
		t.Errorf("should only have 1 mount but received %d", len(mounts))
	}
	m := mounts[0]
	if m.Type != "overlay" {
		t.Errorf("mount type should be overlay but received %q", m.Type)
	}
	if m.Source != "overlay" {
		t.Errorf("expected source %q but received %q", "overlay", m.Source)
	}
	var (
		expected []string
		bp       = getBasePath(ctx, o, root, "/tmp/layer2")
		work     = "workdir=" + filepath.Join(bp, "work")
		upper    = "upperdir=" + filepath.Join(bp, "fs")
		lower    = "lowerdir=" + getParents(ctx, o, root, "/tmp/layer2")[0]
	)

	expected = append(expected, []string{
		work,
		upper,
		lower,
	}...)

	if supportsIndex() {
		expected = append(expected, "index=off")
	}
	if userxattr, err := overlayutils.NeedsUserXAttr(root); err != nil {
		t.Fatal(err)
	} else if userxattr {
		expected = append(expected, "userxattr")
	}

	for i, v := range expected {
		if m.Options[i] != v {
			t.Errorf("expected %q but received %q", v, m.Options[i])
		}
	}
}

func testOverlayRemappedBind(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	for _, test := range []struct {
		idMap        userns.IDMap
		snapOpt      func(idMap userns.IDMap) snapshots.Opt
		expUID       uint32
		expGID       uint32
		expUIDMntOpt string
		expGIDMntOpt string
	}{
		{
			idMap: userns.IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      666,
						Size:        65536,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      666,
						Size:        65536,
					},
				},
			},
			snapOpt: func(idMap userns.IDMap) snapshots.Opt {
				return containerd.WithRemapperLabels(
					idMap.UidMap[0].ContainerID, idMap.UidMap[0].HostID,
					idMap.GidMap[0].ContainerID, idMap.GidMap[0].HostID,
					idMap.UidMap[0].Size,
				)
			},
			expUID:       666,
			expGID:       666,
			expUIDMntOpt: "uidmap=0:666:65536",
			expGIDMntOpt: "gidmap=0:666:65536",
		},
		{
			idMap: userns.IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      666,
						Size:        1000,
					},
					{
						ContainerID: 1000,
						HostID:      6666,
						Size:        64536,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      888,
						Size:        1000,
					},
					{
						ContainerID: 1000,
						HostID:      8888,
						Size:        64536,
					},
				},
			},
			snapOpt: func(idMap userns.IDMap) snapshots.Opt {
				return containerd.WithUserNSRemapperLabels(idMap.UidMap, idMap.GidMap)
			},
			expUID:       666,
			expGID:       888,
			expUIDMntOpt: "uidmap=0:666:1000,1000:6666:64536",
			expGIDMntOpt: "gidmap=0:888:1000,1000:8888:64536",
		},
	} {
		var (
			opts   []snapshots.Opt
			mounts []mount.Mount
		)

		ctx := context.TODO()
		root := t.TempDir()
		o, _, err := newSnapshotter(ctx, root)
		if err != nil {
			t.Fatal(err)
		}

		if sn, ok := o.(*snapshotter); !ok || !sn.remapIDs {
			t.Skip("overlayfs doesn't support idmapped mounts")
		}

		opts = append(opts, test.snapOpt(test.idMap))

		key := "/tmp/test"
		if mounts, err = o.Prepare(ctx, key, "", opts...); err != nil {
			t.Fatal(err)
		}

		bp := getBasePath(ctx, o, root, key)
		expected := []string{test.expUIDMntOpt, test.expGIDMntOpt, "rw", "rbind"}

		checkMountOpts := func() {
			if len(mounts) != 1 {
				t.Errorf("should only have 1 mount but received %d", len(mounts))
			}

			if len(mounts[0].Options) != len(expected) {
				t.Errorf("expected %d options, but received %d", len(expected), len(mounts[0].Options))
			}

			m := mounts[0]
			for i, v := range expected {
				if m.Options[i] != v {
					t.Errorf("mount option %q is not valid, expected %q", m.Options[i], v)
				}
			}

			st, err := os.Stat(filepath.Join(bp, "fs"))
			if err != nil {
				t.Errorf("failed to stat %s", filepath.Join(bp, "fs"))
			}

			if stat, ok := st.Sys().(*syscall.Stat_t); !ok {
				t.Errorf("incompatible types after stat call: *syscall.Stat_t expected")
			} else if stat.Uid != test.expUID || stat.Gid != test.expGID {
				t.Errorf("bad mapping: expected {uid: %d, gid: %d}; real {uid: %d, gid: %d}", test.expUID, test.expGID, int(stat.Uid), int(stat.Gid))
			}
		}
		checkMountOpts()

		expected[2] = "ro"
		if err = o.Commit(ctx, "base", key, opts...); err != nil {
			t.Fatal(err)
		}
		if mounts, err = o.View(ctx, key, "base", opts...); err != nil {
			t.Fatal(err)
		}
		bp = getBasePath(ctx, o, root, key)
		checkMountOpts()

		key = "/tmp/test1"
		if mounts, err = o.Prepare(ctx, key, ""); err != nil {
			t.Fatal(err)
		}

		bp = getBasePath(ctx, o, root, key)

		expected = expected[2:]
		expected[0] = "rw"

		test.expUID = 0
		test.expGID = 0

		checkMountOpts()
	}
}

func testOverlayRemappedActive(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	for _, test := range []struct {
		idMap        userns.IDMap
		snapOpt      func(idMap userns.IDMap) snapshots.Opt
		expUID       uint32
		expGID       uint32
		expUIDMntOpt string
		expGIDMntOpt string
	}{
		{
			idMap: userns.IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      666,
						Size:        65536,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      666,
						Size:        65536,
					},
				},
			},
			snapOpt: func(idMap userns.IDMap) snapshots.Opt {
				return containerd.WithRemapperLabels(
					idMap.UidMap[0].ContainerID, idMap.UidMap[0].HostID,
					idMap.GidMap[0].ContainerID, idMap.GidMap[0].HostID,
					idMap.UidMap[0].Size,
				)
			},
			expUID:       666,
			expGID:       666,
			expUIDMntOpt: "uidmap=0:666:65536",
			expGIDMntOpt: "gidmap=0:666:65536",
		},
		{
			idMap: userns.IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      666,
						Size:        1000,
					},
					{
						ContainerID: 1000,
						HostID:      6666,
						Size:        64536,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      888,
						Size:        1000,
					},
					{
						ContainerID: 1000,
						HostID:      8888,
						Size:        64536,
					},
				},
			},
			snapOpt: func(idMap userns.IDMap) snapshots.Opt {
				return containerd.WithUserNSRemapperLabels(idMap.UidMap, idMap.GidMap)
			},
			expUID:       666,
			expGID:       888,
			expUIDMntOpt: "uidmap=0:666:1000,1000:6666:64536",
			expGIDMntOpt: "gidmap=0:888:1000,1000:8888:64536",
		},
	} {
		var (
			opts   []snapshots.Opt
			mounts []mount.Mount
		)

		ctx := context.TODO()
		root := t.TempDir()
		o, _, err := newSnapshotter(ctx, root)
		if err != nil {
			t.Fatal(err)
		}

		if sn, ok := o.(*snapshotter); !ok || !sn.remapIDs {
			t.Skip("overlayfs doesn't support idmapped mounts")
		}

		opts = append(opts, test.snapOpt(test.idMap))

		key := "/tmp/test"
		if _, err = o.Prepare(ctx, key, "", opts...); err != nil {
			t.Fatal(err)
		}
		if err = o.Commit(ctx, "base", key, opts...); err != nil {
			t.Fatal(err)
		}
		if mounts, err = o.Prepare(ctx, key, "base", opts...); err != nil {
			t.Fatal(err)
		}

		if len(mounts) != 1 {
			t.Errorf("should only have 1 mount but received %d", len(mounts))
		}

		bp := getBasePath(ctx, o, root, key)
		expected := []string{
			test.expUIDMntOpt, test.expGIDMntOpt,
			fmt.Sprintf("workdir=%s", filepath.Join(bp, "work")),
			fmt.Sprintf("upperdir=%s", filepath.Join(bp, "fs")),
			fmt.Sprintf("lowerdir=%s", getParents(ctx, o, root, key)[0]),
		}

		m := mounts[0]
		for i, v := range expected {
			if m.Options[i] != v {
				t.Errorf("mount option %q is invalid, expected %q", m.Options[i], v)
			}
		}

		st, err := os.Stat(filepath.Join(bp, "fs"))
		if err != nil {
			t.Errorf("failed to stat %s", filepath.Join(bp, "fs"))
		}
		if stat, ok := st.Sys().(*syscall.Stat_t); !ok {
			t.Errorf("incompatible types after stat call: *syscall.Stat_t expected")
		} else if stat.Uid != test.expUID || stat.Gid != test.expGID {
			t.Errorf("bad mapping: expected {uid: %d, gid: %d}; received {uid: %d, gid: %d}", test.expUID, test.expGID, int(stat.Uid), int(stat.Gid))
		}
	}
}

func testOverlayRemappedInvalidMapping(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}

	if sn, ok := o.(*snapshotter); !ok || !sn.remapIDs {
		t.Skip("overlayfs doesn't support idmapped mounts")
	}

	key := "/tmp/test"
	for desc, opts := range map[string][]snapshots.Opt{
		"WithLabels: negative UID mapping must fail": {
			snapshots.WithLabels(map[string]string{
				snapshots.LabelSnapshotUIDMapping: "-1:-1:-2",
				snapshots.LabelSnapshotGIDMapping: "0:0:66666",
			}),
		},
		"WithLabels: negative GID mapping must fail": {
			snapshots.WithLabels(map[string]string{
				snapshots.LabelSnapshotUIDMapping: "0:0:66666",
				snapshots.LabelSnapshotGIDMapping: "-1:-1:-2",
			}),
		},
		"WithLabels: negative GID/UID mappings must fail": {
			snapshots.WithLabels(map[string]string{
				snapshots.LabelSnapshotUIDMapping: "-666:-666:-666",
				snapshots.LabelSnapshotGIDMapping: "-666:-666:-666",
			}),
		},
		"WithLabels: negative UID in multiple mappings must fail": {
			snapshots.WithLabels(map[string]string{
				snapshots.LabelSnapshotUIDMapping: "1:1:1,-1:-1:-2",
				snapshots.LabelSnapshotGIDMapping: "0:0:66666",
			}),
		},
		"WithLabels: negative GID in multiple mappings must fail": {
			snapshots.WithLabels(map[string]string{
				snapshots.LabelSnapshotUIDMapping: "0:0:66666",
				snapshots.LabelSnapshotGIDMapping: "-1:-1:-2,6:6:6",
			}),
		},
		"WithLabels: negative GID/UID in multiple mappings must fail": {
			snapshots.WithLabels(map[string]string{
				snapshots.LabelSnapshotUIDMapping: "-666:-666:-666,1:1:1",
				snapshots.LabelSnapshotGIDMapping: "-666:-666:-666,2:2:2",
			}),
		},
		"WithRemapperLabels: container ID (GID/UID) other than 0 must fail": {
			containerd.WithRemapperLabels(666, 666, 666, 666, 666),
		},
		"WithRemapperLabels: container ID (UID) other than 0 must fail": {
			containerd.WithRemapperLabels(666, 0, 0, 0, 65536),
		},
		"WithRemapperLabels: container ID (GID) other than 0 must fail": {
			containerd.WithRemapperLabels(0, 0, 666, 0, 4294967295),
		},
		"WithUserNSRemapperLabels: container ID (GID/UID) other than 0 must fail": {
			containerd.WithUserNSRemapperLabels(
				[]specs.LinuxIDMapping{{ContainerID: 666, HostID: 666, Size: 666}},
				[]specs.LinuxIDMapping{{ContainerID: 666, HostID: 666, Size: 666}},
			),
		},
		"WithUserNSRemapperLabels: container ID (UID) other than 0 must fail": {
			containerd.WithUserNSRemapperLabels(
				[]specs.LinuxIDMapping{{ContainerID: 666, HostID: 0, Size: 65536}},
				[]specs.LinuxIDMapping{{ContainerID: 0, HostID: 0, Size: 65536}},
			),
		},
		"WithUserNSRemapperLabels: container ID (GID) other than 0 must fail": {
			containerd.WithUserNSRemapperLabels(
				[]specs.LinuxIDMapping{{ContainerID: 0, HostID: 0, Size: 4294967295}},
				[]specs.LinuxIDMapping{{ContainerID: 666, HostID: 0, Size: 4294967295}},
			),
		},
	} {
		t.Log(desc)
		if _, err = o.Prepare(ctx, key, "", opts...); err == nil {
			t.Fatalf("snapshots with invalid mappings must fail")
		}
		// remove may fail, but it doesn't matter
		_ = o.Remove(ctx, key)
	}
}

func getBasePath(ctx context.Context, sn snapshots.Snapshotter, root, key string) string {
	o := sn.(*snapshotter)
	ctx, t, err := o.ms.TransactionContext(ctx, false)
	if err != nil {
		panic(err)
	}
	defer t.Rollback()

	s, err := storage.GetSnapshot(ctx, key)
	if err != nil {
		panic(err)
	}

	return filepath.Join(root, "snapshots", s.ID)
}

func getParents(ctx context.Context, sn snapshots.Snapshotter, root, key string) []string {
	o := sn.(*snapshotter)
	ctx, t, err := o.ms.TransactionContext(ctx, false)
	if err != nil {
		panic(err)
	}
	defer t.Rollback()
	s, err := storage.GetSnapshot(ctx, key)
	if err != nil {
		panic(err)
	}
	parents := make([]string, len(s.ParentIDs))
	for i := range s.ParentIDs {
		parents[i] = filepath.Join(root, "snapshots", s.ParentIDs[i], "fs")
	}
	return parents
}

func testOverlayOverlayRead(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	testutil.RequiresRoot(t)
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	key := "/tmp/test"
	mounts, err := o.Prepare(ctx, key, "")
	if err != nil {
		t.Fatal(err)
	}
	m := mounts[0]
	if err := os.WriteFile(filepath.Join(m.Source, "foo"), []byte("hi"), 0660); err != nil {
		t.Fatal(err)
	}
	if err := o.Commit(ctx, "base", key); err != nil {
		t.Fatal(err)
	}
	if mounts, err = o.Prepare(ctx, "/tmp/layer2", "base"); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "dest")
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	if err := mount.All(mounts, dest); err != nil {
		t.Fatal(err)
	}
	defer syscall.Unmount(dest, 0)
	data, err := os.ReadFile(filepath.Join(dest, "foo"))
	if err != nil {
		t.Fatal(err)
	}
	if e := string(data); e != "hi" {
		t.Fatalf("expected file contents hi but got %q", e)
	}
}

func testOverlayView(t *testing.T, newSnapshotter testsuite.SnapshotterFunc) {
	ctx := context.TODO()
	root := t.TempDir()
	o, _, err := newSnapshotter(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	key := "/tmp/base"
	mounts, err := o.Prepare(ctx, key, "")
	if err != nil {
		t.Fatal(err)
	}
	m := mounts[0]
	if err := os.WriteFile(filepath.Join(m.Source, "foo"), []byte("hi"), 0660); err != nil {
		t.Fatal(err)
	}
	if err := o.Commit(ctx, "base", key); err != nil {
		t.Fatal(err)
	}

	key = "/tmp/top"
	_, err = o.Prepare(ctx, key, "base")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(getParents(ctx, o, root, "/tmp/top")[0], "foo"), []byte("hi, again"), 0660); err != nil {
		t.Fatal(err)
	}
	if err := o.Commit(ctx, "top", key); err != nil {
		t.Fatal(err)
	}

	mounts, err = o.View(ctx, "/tmp/view1", "base")
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 {
		t.Fatalf("should only have 1 mount but received %d", len(mounts))
	}
	m = mounts[0]
	if m.Type != "bind" {
		t.Errorf("mount type should be bind but received %q", m.Type)
	}
	expected := getParents(ctx, o, root, "/tmp/view1")[0]
	if m.Source != expected {
		t.Errorf("expected source %q but received %q", expected, m.Source)
	}

	if m.Options[0] != "ro" {
		t.Errorf("expected mount option ro but received %q", m.Options[0])
	}
	if m.Options[1] != "rbind" {
		t.Errorf("expected mount option rbind but received %q", m.Options[1])
	}

	mounts, err = o.View(ctx, "/tmp/view2", "top")
	if err != nil {
		t.Fatal(err)
	}
	if len(mounts) != 1 {
		t.Fatalf("should only have 1 mount but received %d", len(mounts))
	}
	m = mounts[0]
	if m.Type != "overlay" {
		t.Errorf("mount type should be overlay but received %q", m.Type)
	}
	if m.Source != "overlay" {
		t.Errorf("mount source should be overlay but received %q", m.Source)
	}

	supportsIndex := supportsIndex()
	expectedOptions := 3
	if !supportsIndex {
		expectedOptions--
	}
	userxattr, err := overlayutils.NeedsUserXAttr(root)
	if err != nil {
		t.Fatal(err)
	}
	if userxattr {
		expectedOptions++
	}

	if len(m.Options) != expectedOptions {
		t.Errorf("expected %d additional mount option but got %d", expectedOptions, len(m.Options))
	}
	lowers := getParents(ctx, o, root, "/tmp/view2")

	expected = fmt.Sprintf("lowerdir=%s:%s", lowers[0], lowers[1])
	if m.Options[0] != expected {
		t.Errorf("expected option %q but received %q", expected, m.Options[0])
	}

	if m.Options[1] != "volatile" {
		t.Error("expected option first option to be provided option \"volatile\"")
	}
}

// --- layer content cache tests ---

const (
	cacheTestDiffID  = "sha256:0000000000000000000000000000000000000000000000000000000000000001"
	cacheTestChainID = "sha256:0000000000000000000000000000000000000000000000000000000000000002"
)

// extractionOpt builds the snapshot options the unpacker attaches to an
// image-layer extraction Prepare (the snapshot.ref target and the diffID).
func extractionOpt(target string, diffID digest.Digest) snapshots.Opt {
	return snapshots.WithLabels(map[string]string{
		snapshots.LabelSnapshotRef:    target,
		snapshots.LabelSnapshotDiffID: diffID.String(),
	})
}

// writeCacheLayer populates a committed layer in cacheDir's overlayfs
// snapshotter with the given chainID, diffID, parent, and a marker file, closes
// the cache snapshotter so its metadata.db lock is released, and returns the
// absolute path of the committed snapshot's fs directory.
func writeCacheLayer(t *testing.T, cacheDir, parent, chainID string, diffID digest.Digest, filename string, data []byte) string {
	t.Helper()
	ctx := namespaces.WithNamespace(context.Background(), "test")
	sn, err := NewSnapshotter(cacheDir)
	require.NoError(t, err)
	defer sn.Close()

	o := sn.(*snapshotter)
	key := "populate-" + chainID
	opt := extractionOpt(chainID, diffID)
	_, err = o.Prepare(ctx, key, parent, opt)
	require.NoError(t, err)

	id := snapshotID(t, ctx, o, key)
	fsDir := o.upperPath(id)
	require.NoError(t, os.WriteFile(filepath.Join(fsDir, filename), data, 0644))
	require.NoError(t, o.Commit(ctx, chainID, key, opt))
	return fsDir
}

// snapshotID returns the backend snapshot ID for key.
func snapshotID(t *testing.T, ctx context.Context, o *snapshotter, key string) string {
	t.Helper()
	id, _ := snapshotIDInfo(t, ctx, o, key)
	return id
}

// snapshotIDInfo returns a snapshot's id and info, including the labels the
// snapshotter keeps for itself.
func snapshotIDInfo(t *testing.T, ctx context.Context, o *snapshotter, key string) (string, snapshots.Info) {
	t.Helper()
	var (
		id   string
		info snapshots.Info
	)
	require.NoError(t, o.ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		var err error
		id, info, _, err = storage.GetInfo(ctx, key)
		return err
	}))
	return id, info
}

// newCacheSnapshotter creates an overlayfs snapshotter rooted in a temp dir
// with the given options and registers its cleanup.
func newCacheSnapshotter(t *testing.T, opts ...Opt) *snapshotter {
	t.Helper()
	sn, err := NewSnapshotter(t.TempDir(), opts...)
	require.NoError(t, err)
	t.Cleanup(func() { sn.Close() })
	return sn.(*snapshotter)
}

// requireCachedLayerSource asserts that the snapshot behind key records
// cachedDir as its layer's source and does not copy or link files into its
// local snapshots/<id>/fs directory.
func requireCachedLayerSource(t *testing.T, ctx context.Context, o *snapshotter, key, cachedDir string) {
	t.Helper()
	id, info := snapshotIDInfo(t, ctx, o, key)

	src, err := layerSourceFromInfo(info)
	require.NoError(t, err, "the cache hit must be recorded as a layer source")
	assert.Equal(t, layerSourceCache, src.Kind)
	assert.True(t, filepath.IsAbs(src.Ref), "the recorded ref should be absolute")
	assert.Equal(t, cachedDir, src.Ref)

	entries, err := os.ReadDir(o.upperPath(id))
	require.NoError(t, err)
	assert.Empty(t, entries, "a cached layer must not be linked or copied into the local snapshot directory")

	// The record resolves back to the cache entry, which is what gets mounted.
	path, resolved, err := o.resolveLayerDir(id, info)
	require.NoError(t, err)
	assert.Equal(t, cachedDir, path)
	assert.False(t, resolved.owned(), "a cached directory is not this snapshot's to write")
	assert.True(t, resolved.populated(), "a cached directory needs nothing applied into it")
}

// stageCacheHit runs a parentless extraction Prepare for target/diffID; see
// stageCacheHitFrom.
func stageCacheHit(t *testing.T, ctx context.Context, o *snapshotter, target string, diffID digest.Digest) string {
	t.Helper()
	return stageCacheHitFrom(t, ctx, o, "", target, diffID)
}

// stageCacheHitFrom runs an extraction Prepare for target/diffID on top of
// parent and asserts the cache staged the layer into the active snapshot. The
// Prepare succeeds and returns read-only mounts, which tells the unpacker to
// skip the fetch and the apply and still commit. It returns the extraction key
// so the caller can Commit it as the target chainID.
func stageCacheHitFrom(t *testing.T, ctx context.Context, o *snapshotter, parent, target string, diffID digest.Digest) string {
	t.Helper()
	key := "extract-1 " + target
	mounts, err := o.Prepare(ctx, key, parent, extractionOpt(target, diffID))
	require.NoError(t, err, "cache hit must stage without an error")
	require.NotEmpty(t, mounts, "a staged cache hit returns mounts")
	for _, m := range mounts {
		assert.True(t, m.ReadOnly(), "a staged cache hit returns read-only mounts")
	}
	return key
}

// TestCacheHit covers the happy path. An extraction Prepare whose layer is in
// the cache records the cache entry as the snapshot's layer source and returns
// read-only mounts. A subsequent Commit finalizes the target chainID.
func TestCacheHit(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	cacheDir := t.TempDir()
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID
	payload := []byte("cached layer")
	cachedDir := writeCacheLayer(t, cacheDir, "", target, diffID, "hello.txt", payload)

	var wantUsage snapshots.Usage
	func() {
		cacheSn, err := NewSnapshotter(cacheDir)
		require.NoError(t, err)
		defer cacheSn.Close()
		wantUsage, err = cacheSn.Usage(ctx, target)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, wantUsage.Size, int64(len(payload)))
		assert.GreaterOrEqual(t, wantUsage.Inodes, int64(2))
	}()

	o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

	key := stageCacheHit(t, ctx, o, target, diffID)

	// The layer is staged into the active snapshot. The target chainID is not
	// committed yet.
	_, err := o.Stat(ctx, target)
	assert.Error(t, err, "target chainID must not be committed before Commit")

	// The operator-owned cache directory is recorded. Nothing in the snapshot
	// copies or links to it.
	requireCachedLayerSource(t, ctx, o, key, cachedDir)

	// Active Usage reports the precomputed usage from the cache metadata.db.
	activeUsage, err := o.Usage(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, wantUsage, activeUsage)

	// Commit finalizes the snapshot as the target chainID and preserves Usage.
	require.NoError(t, o.Commit(ctx, target, key))
	info, err := o.Stat(ctx, target)
	require.NoError(t, err, "committed snapshot must exist under the target chainID")
	assert.Equal(t, snapshots.KindCommitted, info.Kind)
	assert.Equal(t, "", info.Parent)

	committedUsage, err := o.Usage(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, wantUsage, committedUsage)
}

// TestCacheMiss covers the cases that must NOT be served from the cache and
// instead create a normal active snapshot: cache disabled, layer absent, a
// label-less (container-rootfs) Prepare, and a View.
func TestCacheMiss(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID

	assertFellThrough := func(t *testing.T, o *snapshotter, mounts []mount.Mount, err error, wantRO bool) {
		t.Helper()
		require.NoError(t, err)
		assert.NotEmpty(t, mounts, "a miss must return normal active-snapshot mounts")
		for _, m := range mounts {
			assert.Equal(t, wantRO, m.ReadOnly())
		}
		_, err = o.Stat(ctx, target)
		assert.Error(t, err, "target chainID must not be committed on a miss")
	}

	t.Run("cache disabled", func(t *testing.T) {
		o := newCacheSnapshotter(t)
		mounts, err := o.Prepare(ctx, "extract-1 "+target, "", extractionOpt(target, diffID))
		assertFellThrough(t, o, mounts, err, false)
	})

	t.Run("layer absent", func(t *testing.T) {
		o := newCacheSnapshotter(t, WithLayerContentCaches(t.TempDir()))
		mounts, err := o.Prepare(ctx, "extract-1 "+target, "", extractionOpt(target, diffID))
		assertFellThrough(t, o, mounts, err, false)
	})

	t.Run("no extraction labels", func(t *testing.T) {
		cacheDir := t.TempDir()
		writeCacheLayer(t, cacheDir, "", target, diffID, "hello.txt", []byte("cached"))
		o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))
		mounts, err := o.Prepare(ctx, "container-rootfs", "")
		require.NoError(t, err)
		assert.NotEmpty(t, mounts)
		assert.False(t, mounts[0].ReadOnly())
	})

	t.Run("no extraction labels, parented", func(t *testing.T) {
		cacheDir := t.TempDir()
		parentChain := "sha256:0000000000000000000000000000000000000000000000000000000000000005"
		parentDir := writeCacheLayer(t, cacheDir, "", parentChain, diffID, "hello.txt", []byte("cached"))
		o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

		require.NoError(t, o.Commit(ctx, parentChain, stageCacheHit(t, ctx, o, parentChain, diffID)))

		key := "container-rootfs"
		mounts, err := o.Prepare(ctx, key, parentChain)
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		assert.False(t, mounts[0].ReadOnly(), "a Prepare without extraction labels must expose a writable overlay")
		assert.Contains(t, mounts[0].Options, "lowerdir="+parentDir, "parent lowerdir must resolve to the cache entry")
	})

	t.Run("view is never short-circuited", func(t *testing.T) {
		cacheDir := t.TempDir()
		writeCacheLayer(t, cacheDir, "", target, diffID, "hello.txt", []byte("cached"))
		o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))
		mounts, err := o.View(ctx, "view-1", "", extractionOpt(target, diffID))
		assertFellThrough(t, o, mounts, err, true)
	})
}

// TestCacheParentedStage covers a cached layer whose extraction Prepare carries
// a parent, which is what sequential unpacking does for every layer but the
// first. The cached layer is stacked as the top lower over the parent with no
// upperdir and marked read-only so the unpacker treats it as staged.
func TestCacheParentedStage(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	var (
		cacheDir     = t.TempDir()
		parentDiffID = digest.Digest(cacheTestDiffID)
		parentChain  = cacheTestChainID
		childDiffID  = digest.Digest("sha256:0000000000000000000000000000000000000000000000000000000000000003")
		childChain   = "sha256:0000000000000000000000000000000000000000000000000000000000000004"
		childData    = []byte("fake child layer")
	)
	parentDir := writeCacheLayer(t, cacheDir, "", parentChain, parentDiffID, "base.txt", []byte("fake parent layer"))
	childDir := writeCacheLayer(t, cacheDir, parentChain, childChain, childDiffID, "child.txt", childData)

	o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

	// First layer has no parent.
	require.NoError(t, o.Commit(ctx, parentChain, stageCacheHit(t, ctx, o, parentChain, parentDiffID)))

	// Second layer is prepared on top of it and served from the cache too.
	key := stageCacheHitFrom(t, ctx, o, parentChain, childChain, childDiffID)

	mounts, err := o.Mounts(ctx, key)
	require.NoError(t, err)
	require.Len(t, mounts, 1)
	assert.Equal(t, "overlay", mounts[0].Type)
	assert.Contains(t, mounts[0].Options, "lowerdir="+childDir+":"+parentDir,
		"both cached layers are in the lowerdir range, child on top")
	assert.NotContains(t, strings.Join(mounts[0].Options, ","), "upperdir=",
		"a staged snapshot does not have a writable layer")
	assert.True(t, mounts[0].ReadOnly(), "the unpacker detects staging on the mount")

	require.NoError(t, o.Commit(ctx, childChain, key))
	info, err := o.Stat(ctx, childChain)
	require.NoError(t, err)
	assert.Equal(t, snapshots.KindCommitted, info.Kind)
	assert.Equal(t, parentChain, info.Parent)

	requireCachedLayerSource(t, ctx, o, childChain, childDir)
	data, err := os.ReadFile(filepath.Join(childDir, "child.txt"))
	require.NoError(t, err)
	assert.Equal(t, childData, data, "the cached layer must not be written through")
}

// TestCacheLayerSourceOutlivesCommit covers that a committed snapshot still
// records where its layer directory is when Commit is called with labels.
func TestCacheLayerSourceOutlivesCommit(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	cacheDir := t.TempDir()
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID
	cachedDir := writeCacheLayer(t, cacheDir, "", target, diffID, "hello.txt", []byte("cached"))

	o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

	key := stageCacheHit(t, ctx, o, target, diffID)
	require.NoError(t, o.Commit(ctx, target, key, snapshots.WithLabels(map[string]string{
		snapshots.LabelSnapshotRef: target,
	})))

	requireCachedLayerSource(t, ctx, o, target, cachedDir)
	_, info := snapshotIDInfo(t, ctx, o, target)
	assert.Equal(t, target, info.Labels[snapshots.LabelSnapshotRef],
		"the caller's own labels must survive too")

	// A child stacks it as a lower from the cache, not from a path inside the
	// parent's snapshot directory.
	child := "sha256:0000000000000000000000000000000000000000000000000000000000000009"
	mounts, err := o.Prepare(ctx, child, target)
	require.NoError(t, err)
	require.Len(t, mounts, 1)
	assert.Contains(t, mounts[0].Options, "lowerdir="+cachedDir, "the parent is mounted from the cache")
}

// TestCacheLayerSourceSurvivesUpdate covers an Update that replaces a
// snapshot's labels wholesale.
func TestCacheLayerSourceSurvivesUpdate(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	cacheDir := t.TempDir()
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID
	cachedDir := writeCacheLayer(t, cacheDir, "", target, diffID, "hello.txt", []byte("cached"))

	o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

	require.NoError(t, o.Commit(ctx, target, stageCacheHit(t, ctx, o, target, diffID)))

	// A wholesale label replacement, carrying none of the snapshotter's own.
	_, err := o.Update(ctx, snapshots.Info{
		Name:   target,
		Labels: map[string]string{"example.com/mine": "yes"},
	})
	require.NoError(t, err)

	requireCachedLayerSource(t, ctx, o, target, cachedDir)
	_, info := snapshotIDInfo(t, ctx, o, target)
	assert.Equal(t, "yes", info.Labels["example.com/mine"], "the caller's update must still apply")

	// An attempt to clear them outright is ignored for the same reason.
	_, err = o.Update(ctx, snapshots.Info{
		Name:   target,
		Labels: map[string]string{layerSourceRefLabel: ""},
	}, "labels."+layerSourceRefLabel)
	require.NoError(t, err)
	requireCachedLayerSource(t, ctx, o, target, cachedDir)
}

// TestCacheStaleLayer covers a cache entry pruned after a snapshot was staged
// from it. Mounts must fail rather than falling back to an empty local
// directory.
func TestCacheStaleLayer(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	var (
		cacheDir     = t.TempDir()
		parentDiffID = digest.Digest(cacheTestDiffID)
		parentChain  = cacheTestChainID
		childDiffID  = digest.Digest("sha256:0000000000000000000000000000000000000000000000000000000000000003")
		childChain   = "sha256:0000000000000000000000000000000000000000000000000000000000000004"
	)
	writeCacheLayer(t, cacheDir, "", parentChain, parentDiffID, "base.txt", []byte("base"))
	childDir := writeCacheLayer(t, cacheDir, parentChain, childChain, childDiffID, "child.txt", []byte("child"))

	o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

	require.NoError(t, o.Commit(ctx, parentChain, stageCacheHit(t, ctx, o, parentChain, parentDiffID)))
	key := stageCacheHitFrom(t, ctx, o, parentChain, childChain, childDiffID)

	// The operator prunes the cache entry while the pull is in flight.
	require.NoError(t, os.RemoveAll(childDir))

	_, err := o.Mounts(ctx, key)
	require.Error(t, err, "an unresolvable layer source must not pass as populated content")
	assert.ErrorIs(t, err, os.ErrNotExist)

	t.Run("parentless", func(t *testing.T) {
		cacheDir := t.TempDir()
		diffID := digest.Digest(cacheTestDiffID)
		cachedDir := writeCacheLayer(t, cacheDir, "", cacheTestChainID, diffID, "base.txt", []byte("base"))
		o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

		key := stageCacheHit(t, ctx, o, cacheTestChainID, diffID)
		require.NoError(t, os.RemoveAll(cachedDir))

		mounts, err := o.Mounts(ctx, key)
		require.Error(t, err, "an unresolvable layer source must not pass as populated content")
		assert.ErrorIs(t, err, os.ErrNotExist)
		assert.Nil(t, mounts, "no writable fallback may be handed out")
	})
}

// TestCacheStaleLayerCommit covers a cache entry pruned between Prepare and
// Commit. Commit must fail rather than committing an unresolvable layer.
func TestCacheStaleLayerCommit(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	cacheDir := t.TempDir()
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID
	cachedDir := writeCacheLayer(t, cacheDir, "", target, diffID, "base.txt", []byte("base"))

	o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

	key := stageCacheHit(t, ctx, o, target, diffID)

	// The operator prunes the cache entry while the pull is in flight.
	require.NoError(t, os.RemoveAll(cachedDir))

	err := o.Commit(ctx, target, key)
	require.Error(t, err, "an unresolvable layer source must not be committed")
	assert.ErrorIs(t, err, os.ErrNotExist)

	_, err = o.Stat(ctx, target)
	assert.Error(t, err, "the layer must not be committed")
}

// TestCacheRemove covers removal of a cache-hit snapshot. Removal deletes the
// local snapshot directory and leaves the operator-owned cache directory
// untouched.
func TestCacheRemove(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	cacheDir := t.TempDir()
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID
	cachedDir := writeCacheLayer(t, cacheDir, "", target, diffID, "base.txt", []byte("base"))

	o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))

	key := stageCacheHit(t, ctx, o, target, diffID)
	require.NoError(t, o.Commit(ctx, target, key))

	snapDir := filepath.Dir(o.upperPath(snapshotID(t, ctx, o, target)))

	require.NoError(t, o.Remove(ctx, target))

	_, err := os.Stat(snapDir)
	assert.True(t, os.IsNotExist(err), "snapshot dir should be removed")
	_, err = os.Stat(filepath.Join(cachedDir, "base.txt"))
	require.NoError(t, err, "cache layer directory must be untouched by Remove")
}

// TestLinkedLayerDirIsNotOurs covers a local snapshots/<id>/fs that has been
// replaced with a symlink. Commit refuses it and Remove unlinks the symlink
// without touching the target directory.
func TestLinkedLayerDirIsNotOurs(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	o := newCacheSnapshotter(t)

	shared := filepath.Join(t.TempDir(), "shared-fs")
	require.NoError(t, os.MkdirAll(shared, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(shared, "secret"), []byte("not ours to touch"), 0644))

	key := "extract-1 linked"
	_, err := o.Prepare(ctx, key, "")
	require.NoError(t, err)
	id := snapshotID(t, ctx, o, key)
	require.NoError(t, os.RemoveAll(o.upperPath(id)))
	require.NoError(t, os.Symlink(shared, o.upperPath(id)))

	target := cacheTestChainID
	err = o.Commit(ctx, target, key)
	require.Error(t, err, "a linked layer directory must not be committed as the snapshot's own")
	assert.ErrorIs(t, err, errdefs.ErrFailedPrecondition)

	require.NoError(t, o.Remove(ctx, key))
	_, err = os.Lstat(o.upperPath(id))
	assert.ErrorIs(t, err, os.ErrNotExist, "the snapshot, and its link, are gone")
	data, err := os.ReadFile(filepath.Join(shared, "secret"))
	require.NoError(t, err)
	assert.Equal(t, "not ours to touch", string(data), "the link target must survive removal")
}

// TestCacheMultipleDirs covers the layer_content_caches search order:
// directories are searched in configured order, the first hit wins, later
// caches are reached when earlier ones lack the layer, and a miss in every
// cache falls through to a normal writable snapshot.
func TestCacheMultipleDirs(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID

	stagedDir := func(t *testing.T, o *snapshotter, key string) string {
		t.Helper()
		_, info := snapshotIDInfo(t, ctx, o, key)
		src, err := layerSourceFromInfo(info)
		require.NoError(t, err)
		return src.Ref
	}

	t.Run("first cache with the layer wins", func(t *testing.T) {
		first, second := t.TempDir(), t.TempDir()
		firstDir := writeCacheLayer(t, first, "", target, diffID, "first.txt", []byte("from first"))
		writeCacheLayer(t, second, "", target, diffID, "second.txt", []byte("from second"))

		o := newCacheSnapshotter(t, WithLayerContentCaches(first, second))
		key := stageCacheHit(t, ctx, o, target, diffID)
		assert.Equal(t, firstDir, stagedDir(t, o, key))
	})

	t.Run("falls through to a later cache", func(t *testing.T) {
		empty, populated := t.TempDir(), t.TempDir()
		secondDir := writeCacheLayer(t, populated, "", target, diffID, "second.txt", []byte("from second"))

		o := newCacheSnapshotter(t, WithLayerContentCaches(empty, populated))
		key := stageCacheHit(t, ctx, o, target, diffID)
		assert.Equal(t, secondDir, stagedDir(t, o, key))
	})

	t.Run("miss in every cache falls back to a writable snapshot", func(t *testing.T) {
		o := newCacheSnapshotter(t, WithLayerContentCaches(
			filepath.Join(t.TempDir(), "missing"), t.TempDir(), t.TempDir()))

		mounts, err := o.Prepare(ctx, "extract-1 "+target, "", extractionOpt(target, diffID))
		require.NoError(t, err)
		require.NotEmpty(t, mounts)
		for _, m := range mounts {
			assert.False(t, m.ReadOnly(), "a miss in every cache must return writable mounts")
		}
		_, err = o.Stat(ctx, target)
		assert.Error(t, err, "target chainID must not be committed on a miss")
	})
}

// TestCacheLayouts covers configuring layer_content_caches with a snapshots
// subdirectory (<root>/snapshots) or a containerd root directory
// (<root>/io.containerd.snapshotter.v1.overlayfs).
func TestCacheLayouts(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID

	t.Run("snapshots subdirectory path", func(t *testing.T) {
		cacheRoot := t.TempDir()
		cachedDir := writeCacheLayer(t, cacheRoot, "", target, diffID, "hello.txt", []byte("cached"))
		snapshotsPath := filepath.Join(cacheRoot, "snapshots")

		for _, tc := range []struct {
			name string
			path string
		}{
			{"clean", snapshotsPath},
			{"trailing_slash", snapshotsPath + "/"},
			{"trailing_dot", snapshotsPath + "/."},
		} {
			t.Run(tc.name, func(t *testing.T) {
				o := newCacheSnapshotter(t, WithLayerContentCaches(tc.path))
				key := stageCacheHit(t, ctx, o, target, diffID)
				requireCachedLayerSource(t, ctx, o, key, cachedDir)
			})
		}
	})

	t.Run("containerd root directory path", func(t *testing.T) {
		containerdRoot := t.TempDir()
		pluginRoot := filepath.Join(containerdRoot, "io.containerd.snapshotter.v1.overlayfs")
		cachedDir := writeCacheLayer(t, pluginRoot, "", target, diffID, "hello.txt", []byte("cached"))

		for _, tc := range []struct {
			name string
			path string
		}{
			{"clean", containerdRoot},
			{"trailing_slash", containerdRoot + "/"},
			{"trailing_dot", containerdRoot + "/."},
		} {
			t.Run(tc.name, func(t *testing.T) {
				o := newCacheSnapshotter(t, WithLayerContentCaches(tc.path))
				key := stageCacheHit(t, ctx, o, target, diffID)
				requireCachedLayerSource(t, ctx, o, key, cachedDir)
			})
		}
	})
}

// TestCacheUpperdirLabel covers WithUpperdirLabel on a cached snapshot across
// Stat, Update, and Walk.
func TestCacheUpperdirLabel(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	cacheDir := t.TempDir()
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID
	cachedDir := writeCacheLayer(t, cacheDir, "", target, diffID, "hello.txt", []byte("cached"))

	o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir), WithUpperdirLabel)
	key := stageCacheHit(t, ctx, o, target, diffID)
	require.NoError(t, o.Commit(ctx, target, key))

	info, err := o.Stat(ctx, target)
	require.NoError(t, err)
	assert.Equal(t, cachedDir, info.Labels[upperdirKey])

	updated, err := o.Update(ctx, snapshots.Info{
		Name:   target,
		Labels: map[string]string{"custom": "val"},
	})
	require.NoError(t, err)
	assert.Equal(t, cachedDir, updated.Labels[upperdirKey])

	var walkedUpperdir string
	require.NoError(t, o.Walk(ctx, func(ctx context.Context, wi snapshots.Info) error {
		if wi.Name == target {
			walkedUpperdir = wi.Labels[upperdirKey]
		}
		return nil
	}))
	assert.Equal(t, cachedDir, walkedUpperdir)
}

// TestCacheDirMustBeAbsolute covers validation of configured cache directory
// paths in NewSnapshotter.
func TestCacheDirMustBeAbsolute(t *testing.T) {
	for _, dir := range []string{"relative-cache", "./cache", "", "a/b"} {
		t.Run(strconv.Quote(dir), func(t *testing.T) {
			_, err := NewSnapshotter(t.TempDir(), WithLayerContentCaches(dir))
			assert.ErrorContains(t, err, "must be an absolute path")
		})
	}
}

// TestCacheEmptyMetadataDB verifies that an initialized BoltDB file without a
// "v1" snapshots bucket yet is treated as a clean miss.
func TestCacheEmptyMetadataDB(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID

	emptyCache := filepath.Join(t.TempDir(), "empty-bolt-cache")
	require.NoError(t, os.MkdirAll(filepath.Join(emptyCache, "snapshots"), 0755))
	bdb, err := bolt.Open(filepath.Join(emptyCache, "metadata.db"), 0600, nil)
	require.NoError(t, err)
	require.NoError(t, bdb.Close())

	o := newCacheSnapshotter(t, WithLayerContentCaches(emptyCache))
	mounts, err := o.Prepare(ctx, "extract-1 "+target, "", extractionOpt(target, diffID))
	require.NoError(t, err)
	require.NotEmpty(t, mounts)
	assert.False(t, mounts[0].ReadOnly(), "empty cache must fall back to a writable snapshot")
}

// TestCacheMalformedAndTransitiveEntries verifies that malformed layerSource
// metadata, symlinked fs directories, and transitive cache entries in a cache
// metadata.db never cause the primary snapshotter to mount an empty local
// snapshots/<id>/fs directory or re-export external host paths.
func TestCacheMalformedAndTransitiveEntries(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	diffID := digest.Digest(cacheTestDiffID)
	target := cacheTestChainID

	t.Run("malformed layer source does not mount empty fs", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "malformed-cache")
		require.NoError(t, os.MkdirAll(filepath.Join(cacheDir, "snapshots"), 0755))

		ms, err := storage.NewMetaStore(filepath.Join(cacheDir, "metadata.db"))
		require.NoError(t, err)
		var committedID string
		require.NoError(t, ms.WithTransaction(ctx, true, func(ctx context.Context) error {
			labels := map[string]string{
				snapshots.LabelSnapshotRef:    target,
				snapshots.LabelSnapshotDiffID: diffID.String(),
				layerSourceKindLabel:          "future-source",
			}
			s, err := storage.CreateSnapshot(ctx, snapshots.KindActive, "prep", "", snapshots.WithLabels(labels))
			if err != nil {
				return err
			}
			committedID, err = storage.CommitActive(ctx, "prep", target, snapshots.Usage{}, snapshots.WithLabels(labels))
			if err != nil {
				return err
			}
			_ = s
			return nil
		}))
		require.NoError(t, ms.Close())

		// Create an empty snapshots/<id>/fs directory in the cache; lookupCache
		// must NOT fall back to mounting this empty directory when
		// layerSourceFromInfo fails.
		emptyCacheFS := filepath.Join(cacheDir, "snapshots", committedID, "fs")
		require.NoError(t, os.MkdirAll(emptyCacheFS, 0755))

		o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))
		mounts, err := o.Prepare(ctx, "extract-1 "+target, "", extractionOpt(target, diffID))
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		assert.False(t, mounts[0].ReadOnly(), "malformed cache entry must fall back to writable extraction")
		assert.NotEqual(t, emptyCacheFS, mounts[0].Source, "must never mount empty cache fs directory")
	})

	t.Run("transitive cache entry is skipped", func(t *testing.T) {
		upstreamCache := t.TempDir()
		writeCacheLayer(t, upstreamCache, "", target, diffID, "hello.txt", []byte("upstream"))

		middleCache := filepath.Join(t.TempDir(), "middle-cache")
		func() {
			sn, err := NewSnapshotter(middleCache, WithLayerContentCaches(upstreamCache))
			require.NoError(t, err)
			defer sn.Close()
			mid := sn.(*snapshotter)
			key := stageCacheHit(t, ctx, mid, target, diffID)
			require.NoError(t, mid.Commit(ctx, target, key, extractionOpt(target, diffID)))
		}()

		primary := newCacheSnapshotter(t, WithLayerContentCaches(middleCache))
		mounts, err := primary.Prepare(ctx, "extract-1 "+target, "", extractionOpt(target, diffID))
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		assert.False(t, mounts[0].ReadOnly(), "transitive cache entry must not be re-exported")
	})

	t.Run("symlinked fs directory in cache is rejected", func(t *testing.T) {
		cacheDir := filepath.Join(t.TempDir(), "symlink-cache")
		cachedDir := writeCacheLayer(t, cacheDir, "", target, diffID, "hello.txt", []byte("cached"))

		outsideDir := filepath.Join(t.TempDir(), "outside")
		require.NoError(t, os.MkdirAll(outsideDir, 0755))
		require.NoError(t, os.RemoveAll(cachedDir))
		require.NoError(t, os.Symlink(outsideDir, cachedDir))

		o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))
		mounts, err := o.Prepare(ctx, "extract-1 "+target, "", extractionOpt(target, diffID))
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		assert.False(t, mounts[0].ReadOnly(), "symlinked cache fs must not be mounted as a cache hit")
	})
}

// TestCacheProvisionedAfterStartupAndLegacyName verifies that a cache directory
// provisioned after NewSnapshotter is indexed on its first populated lookup,
// subsequent lookups reuse the in-memory index without re-opening metadata.db,
// and "<ns>/<sid>/<chainID>" snapshot names match even when LabelSnapshotRef
// was not set on Commit.
func TestCacheProvisionedAfterStartupAndLegacyName(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	lateCache := filepath.Join(t.TempDir(), "late-cache")
	diffID0 := digest.FromString("late-layer-0")
	chainID0 := identity.ChainID([]digest.Digest{diffID0}).String()
	diffID1 := digest.FromString("late-layer-1")
	chainID1 := identity.ChainID([]digest.Digest{diffID0, diffID1}).String()
	diffID2 := digest.FromString("legacy-name-layer")
	chainID2 := identity.ChainID([]digest.Digest{diffID2}).String()

	o := newCacheSnapshotter(t, WithLayerContentCaches(lateCache))

	// Before lateCache exists on disk, Prepare must miss cleanly without
	// caching an empty index for the absent directory.
	missBefore, err := o.Prepare(ctx, "miss-before-mount", "", extractionOpt(chainID0, diffID0))
	require.NoError(t, err)
	assert.False(t, missBefore[0].ReadOnly())
	require.NoError(t, o.Remove(ctx, "miss-before-mount"))

	// Provision lateCache with layer 0 and a legacy-named "<ns>/<sid>/<chainID>"
	// entry (without LabelSnapshotRef).
	dir0 := writeCacheLayer(t, lateCache, "", chainID0, diffID0, "zero.txt", []byte("0"))
	var dir2 string
	func() {
		sn, err := NewSnapshotter(lateCache)
		require.NoError(t, err)
		defer sn.Close()
		cs := sn.(*snapshotter)
		_, err = cs.Prepare(ctx, "prep-legacy", "")
		require.NoError(t, err)
		dir2 = cs.upperPath(snapshotID(t, ctx, cs, "prep-legacy"))
		require.NoError(t, os.WriteFile(filepath.Join(dir2, "legacy.txt"), []byte("legacy"), 0644))
		require.NoError(t, cs.Commit(ctx, "default/42/"+chainID2, "prep-legacy", snapshots.WithLabels(map[string]string{
			snapshots.LabelSnapshotDiffID: diffID2.String(),
		})))
	}()

	// First lookup after provisioning builds the in-memory index and hits.
	key0 := stageCacheHit(t, ctx, o, chainID0, diffID0)
	requireCachedLayerSource(t, ctx, o, key0, dir0)
	require.NoError(t, o.Commit(ctx, chainID0, key0))

	// Remove metadata.db while leaving snapshots/<id>/fs intact: subsequent
	// hits and misses must be served from the cached in-memory index without
	// re-opening metadata.db.
	require.NoError(t, os.Remove(filepath.Join(lateCache, "metadata.db")))

	missMounts, err := o.Prepare(ctx, "miss-after-index", chainID0, extractionOpt(chainID1, diffID1))
	require.NoError(t, err)
	assert.False(t, missMounts[0].ReadOnly())
	require.NoError(t, o.Remove(ctx, "miss-after-index"))

	key2 := stageCacheHit(t, ctx, o, chainID2, diffID2)
	requireCachedLayerSource(t, ctx, o, key2, dir2)
}

// TestCacheCreateSnapshotRollbackOnMountFailure verifies that when o.mounts
// fails during Prepare (for example, an ancestor's cached directory vanished
// while the immediate parent's directory still exists), createSnapshot rolls
// back the metadata.db transaction and removes the snapshot directory on disk.
func TestCacheCreateSnapshotRollbackOnMountFailure(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	cacheDir := t.TempDir()
	diffID0 := digest.FromString("rollback-layer-0")
	chainID0 := identity.ChainID([]digest.Digest{diffID0}).String()
	diffID1 := digest.FromString("rollback-layer-1")
	chainID1 := identity.ChainID([]digest.Digest{diffID0, diffID1}).String()

	grandparentDir := writeCacheLayer(t, cacheDir, "", chainID0, diffID0, "gp.txt", []byte("gp"))
	_ = writeCacheLayer(t, cacheDir, chainID0, chainID1, diffID1, "parent.txt", []byte("parent"))

	o := newCacheSnapshotter(t, WithLayerContentCaches(cacheDir))
	require.NoError(t, o.Commit(ctx, chainID0, stageCacheHit(t, ctx, o, chainID0, diffID0)))
	require.NoError(t, o.Commit(ctx, chainID1, stageCacheHitFrom(t, ctx, o, chainID0, chainID1, diffID1)))

	// Remove the grandparent's cached fs directory while keeping the immediate
	// parent's directory intact so createSnapshot succeeds up through rename
	// and fails inside o.mounts when resolving all parent lowerdirs.
	require.NoError(t, os.RemoveAll(grandparentDir))

	mounts, err := o.Prepare(ctx, "child-rootfs", chainID1)
	require.Error(t, err)
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.Nil(t, mounts)

	_, err = o.Stat(ctx, "child-rootfs")
	assert.ErrorIs(t, err, errdefs.ErrNotFound, "failed Prepare must roll back metadata.db")

	// Parent chainID1 must not be pinned by a leaked child record.
	require.NoError(t, o.Remove(ctx, chainID1), "parent snapshot must remain removable after rolled-back Prepare")

	entries, err := os.ReadDir(filepath.Join(o.root, "snapshots"))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "only chainID0's snapshot directory should remain on disk")
}

// failOnApply fails the test if Apply is called for a cached layer.
type failOnApply struct {
	t *testing.T
}

func (f failOnApply) Apply(_ context.Context, desc ocispec.Descriptor, _ []mount.Mount, _ ...diff.ApplyOpt) (ocispec.Descriptor, error) {
	f.t.Errorf("Apply must not be called for cached layer %s", desc.Digest)
	return ocispec.Descriptor{}, fmt.Errorf("unexpected Apply call for %s", desc.Digest)
}

// populateApplier writes a marker file per layer into the extraction mount's
// directory when populating a snapshotter. If t is non-nil, unexpected layer
// digests fail the test.
type populateApplier struct {
	t       *testing.T
	diffIDs map[digest.Digest]digest.Digest
	files   map[digest.Digest]string
}

func (p populateApplier) Apply(_ context.Context, desc ocispec.Descriptor, mounts []mount.Mount, _ ...diff.ApplyOpt) (ocispec.Descriptor, error) {
	name, ok := p.files[desc.Digest]
	if !ok {
		if p.t != nil {
			p.t.Errorf("Apply must not be called for cached layer %s", desc.Digest)
		}
		return ocispec.Descriptor{}, fmt.Errorf("unexpected Apply call for %s", desc.Digest)
	}
	targetDir := mounts[0].Source
	if mounts[0].Type == "overlay" {
		for _, opt := range mounts[0].Options {
			if after, ok := strings.CutPrefix(opt, "upperdir="); ok {
				targetDir = after
				break
			}
		}
	}
	if err := os.WriteFile(filepath.Join(targetDir, name), []byte(name), 0644); err != nil {
		return ocispec.Descriptor{}, err
	}
	return ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayer,
		Digest:    p.diffIDs[desc.Digest],
		Size:      int64(len(name)),
	}, nil
}

// TestCacheUnpackIntegration exercises the full unpacker + metadata.DB +
// overlay.Snapshotter flow in both sequential and parallel (rebase) unpack
// modes against caches populated through core/metadata, covering both full and
// partial cache hits as well as idmapped container rootfs preparation.
func TestCacheUnpackIntegration(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")

	diffID0 := digest.FromString("layer-0-uncompressed")
	diffID1 := digest.FromString("layer-1-uncompressed")
	diffIDs := []digest.Digest{diffID0, diffID1}
	chainIDs := identity.ChainIDs(slices.Clone(diffIDs))

	layer0Bytes := "layer-0-compressed"
	layer0Desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromString(layer0Bytes),
		Size:      int64(len(layer0Bytes)),
	}
	layer1Bytes := "layer-1-compressed"
	layer1Desc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageLayerGzip,
		Digest:    digest.FromString(layer1Bytes),
		Size:      int64(len(layer1Bytes)),
	}
	layers := []ocispec.Descriptor{layer0Desc, layer1Desc}

	imgConfig := ocispec.Image{
		Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
		RootFS: ocispec.RootFS{
			Type:    "layers",
			DiffIDs: diffIDs,
		},
	}
	configBytes, err := json.Marshal(imgConfig)
	require.NoError(t, err)
	configDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(configBytes),
		Size:      int64(len(configBytes)),
	}

	manifest := ocispec.Manifest{
		Config: configDesc,
		Layers: layers,
	}
	manifestBytes, err := json.Marshal(manifest)
	require.NoError(t, err)
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	writeImageBlobs := func(t *testing.T, cs content.Store, includeLayer0, includeLayer1 bool) {
		t.Helper()
		require.NoError(t, content.WriteBlob(ctx, cs, "config", strings.NewReader(string(configBytes)), configDesc))
		require.NoError(t, content.WriteBlob(ctx, cs, "manifest", strings.NewReader(string(manifestBytes)), manifestDesc))
		if includeLayer0 {
			require.NoError(t, content.WriteBlob(ctx, cs, "layer0", strings.NewReader(layer0Bytes), layer0Desc))
		}
		if includeLayer1 {
			require.NoError(t, content.WriteBlob(ctx, cs, "layer1", strings.NewReader(layer1Bytes), layer1Desc))
		}
	}

	populateCache := func(t *testing.T, numLayers int) (string, string) {
		t.Helper()
		dir := t.TempDir()
		overlayRoot := filepath.Join(dir, "io.containerd.snapshotter.v1.overlayfs")

		pDiffIDs := diffIDs[:numLayers]
		pLayers := layers[:numLayers]
		pImgCfg := ocispec.Image{
			Platform: ocispec.Platform{OS: "linux", Architecture: "amd64"},
			RootFS:   ocispec.RootFS{Type: "layers", DiffIDs: pDiffIDs},
		}
		pCfgBytes, err := json.Marshal(pImgCfg)
		require.NoError(t, err)
		pCfgDesc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageConfig,
			Digest:    digest.FromBytes(pCfgBytes),
			Size:      int64(len(pCfgBytes)),
		}
		pManifest := ocispec.Manifest{Config: pCfgDesc, Layers: pLayers}
		pManifestBytes, err := json.Marshal(pManifest)
		require.NoError(t, err)
		pManifestDesc := ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageManifest,
			Digest:    digest.FromBytes(pManifestBytes),
			Size:      int64(len(pManifestBytes)),
		}

		cs, err := local.NewStore(filepath.Join(dir, "content"))
		require.NoError(t, err)

		cacheSn, err := NewSnapshotter(overlayRoot)
		require.NoError(t, err)
		defer cacheSn.Close()

		bdb, err := bolt.Open(filepath.Join(dir, "meta.db"), 0600, nil)
		require.NoError(t, err)
		defer bdb.Close()

		mdb := metadata.NewDB(bdb, cs, map[string]snapshots.Snapshotter{"overlayfs": cacheSn})
		require.NoError(t, mdb.Init(ctx))
		require.NoError(t, content.WriteBlob(ctx, mdb.ContentStore(), "config", strings.NewReader(string(pCfgBytes)), pCfgDesc))
		require.NoError(t, content.WriteBlob(ctx, mdb.ContentStore(), "manifest", strings.NewReader(string(pManifestBytes)), pManifestDesc))
		require.NoError(t, content.WriteBlob(ctx, mdb.ContentStore(), "layer0", strings.NewReader(layer0Bytes), layer0Desc))
		if numLayers > 1 {
			require.NoError(t, content.WriteBlob(ctx, mdb.ContentStore(), "layer1", strings.NewReader(layer1Bytes), layer1Desc))
		}

		applier := populateApplier{
			diffIDs: map[digest.Digest]digest.Digest{
				layer0Desc.Digest: diffID0,
				layer1Desc.Digest: diffID1,
			},
			files: map[digest.Digest]string{
				layer0Desc.Digest: "file0.txt",
				layer1Desc.Digest: "file1.txt",
			},
		}
		u, err := unpack.NewUnpacker(ctx, mdb.ContentStore(),
			unpack.WithUnpackPlatform(unpack.Platform{
				Platform:                platforms.All,
				SnapshotterKey:          "overlayfs",
				Snapshotter:             mdb.Snapshotter("overlayfs"),
				Applier:                 applier,
				SnapshotterCapabilities: []string{snapshots.RebaseCap},
			}),
		)
		require.NoError(t, err)

		noopHandler := images.HandlerFunc(func(_ context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
			if desc.Digest == pManifestDesc.Digest {
				return append([]ocispec.Descriptor{pCfgDesc}, pLayers...), nil
			}
			return nil, nil
		})
		wrapped := u.Unpack(noopHandler)
		_, err = wrapped.Handle(ctx, pManifestDesc)
		require.NoError(t, err)
		_, err = wrapped.Handle(ctx, pCfgDesc)
		require.NoError(t, err)
		_, err = u.Wait()
		require.NoError(t, err)
		return dir, overlayRoot
	}

	// Step 1: Populate a full 2-layer cache and a partial 1-layer cache via
	// core/metadata + Unpacker so backend snapshot keys have "<ns>/<sid>/<chainID>".
	cacheDir, cacheOverlayRoot := populateCache(t, 2)
	partialCacheDir, partialCacheOverlayRoot := populateCache(t, 1)

	// Step 2: Unpack into a fresh primary snapshotter with WithLayerContentCaches
	// across sequential/parallel and full/partial cache hit modes.
	for _, tc := range []struct {
		name       string
		parallel   bool
		partialHit bool
	}{
		{"sequential", false, false},
		{"parallel", true, false},
		{"partial_sequential", false, true},
		{"partial_parallel", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			primaryDir := t.TempDir()
			primaryOverlayRoot := filepath.Join(primaryDir, "io.containerd.snapshotter.v1.overlayfs")

			selectedCache := cacheDir
			selectedCacheOverlayRoot := cacheOverlayRoot
			if tc.partialHit {
				selectedCache = partialCacheDir
				selectedCacheOverlayRoot = partialCacheOverlayRoot
			}

			cs, err := local.NewStore(filepath.Join(primaryDir, "content"))
			require.NoError(t, err)

			primarySn, err := NewSnapshotter(primaryOverlayRoot, WithLayerContentCaches(selectedCache), WithRemapIDs)
			require.NoError(t, err)
			defer primarySn.Close()

			bdb, err := bolt.Open(filepath.Join(primaryDir, "meta.db"), 0600, nil)
			require.NoError(t, err)
			defer bdb.Close()

			mdb := metadata.NewDB(bdb, cs, map[string]snapshots.Snapshotter{"overlayfs": primarySn})
			require.NoError(t, mdb.Init(ctx))
			// Layer 0 is cached in every case (never written to primary content store);
			// Layer 1 is written only in the partial-hit cases.
			writeImageBlobs(t, mdb.ContentStore(), false, tc.partialHit)
			metaSn := mdb.Snapshotter("overlayfs")

			var applier diff.Applier = failOnApply{t: t}
			if tc.partialHit {
				applier = populateApplier{
					t:       t,
					diffIDs: map[digest.Digest]digest.Digest{layer1Desc.Digest: diffID1},
					files:   map[digest.Digest]string{layer1Desc.Digest: "file1.txt"},
				}
			}

			unpackOpts := []unpack.UnpackerOpt{
				unpack.WithUnpackPlatform(unpack.Platform{
					Platform:                platforms.All,
					SnapshotterKey:          "overlayfs",
					Snapshotter:             metaSn,
					Applier:                 applier,
					SnapshotterCapabilities: []string{snapshots.RebaseCap},
				}),
			}
			if tc.parallel {
				unpackOpts = append(unpackOpts, unpack.WithUnpackLimiter(semaphore.NewWeighted(4)))
			}

			u, err := unpack.NewUnpacker(ctx, mdb.ContentStore(), unpackOpts...)
			require.NoError(t, err)

			var fetchedLayer1 bool
			handler := images.HandlerFunc(func(_ context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				if desc.Digest == manifestDesc.Digest {
					return append([]ocispec.Descriptor{configDesc}, layers...), nil
				}
				if desc.Digest == configDesc.Digest {
					return nil, nil
				}
				if tc.partialHit && desc.Digest == layer1Desc.Digest {
					fetchedLayer1 = true
					return nil, nil
				}
				t.Errorf("layer blob fetch must not be called for cached layer %s", desc.Digest)
				return nil, fmt.Errorf("unexpected fetch for %s", desc.Digest)
			})
			wrapped := u.Unpack(handler)
			_, err = wrapped.Handle(ctx, manifestDesc)
			require.NoError(t, err)
			_, err = wrapped.Handle(ctx, configDesc)
			require.NoError(t, err)
			_, err = u.Wait()
			require.NoError(t, err)
			if tc.partialHit {
				assert.True(t, fetchedLayer1, "uncached top layer must be fetched")
			}

			// Verify committed chainIDs exist and link properly.
			info0, err := metaSn.Stat(ctx, chainIDs[0].String())
			require.NoError(t, err)
			assert.Equal(t, snapshots.KindCommitted, info0.Kind)
			assert.Equal(t, "", info0.Parent)

			info1, err := metaSn.Stat(ctx, chainIDs[1].String())
			require.NoError(t, err)
			assert.Equal(t, snapshots.KindCommitted, info1.Kind)
			assert.Equal(t, chainIDs[0].String(), info1.Parent)

			// Prepare a container rootfs (with idmap labels when running as
			// root so Lchown is permitted) and verify lowerdirs and options.
			var prepOpts []snapshots.Opt
			const idmapSpec = "0:0:65536"
			if os.Geteuid() == 0 {
				prepOpts = append(prepOpts, snapshots.WithLabels(map[string]string{
					snapshots.LabelSnapshotUIDMapping: idmapSpec,
					snapshots.LabelSnapshotGIDMapping: idmapSpec,
				}))
			}
			mounts, err := metaSn.Prepare(ctx, "container-rootfs", chainIDs[1].String(), prepOpts...)
			require.NoError(t, err)
			require.Len(t, mounts, 1)
			assert.Equal(t, "overlay", mounts[0].Type)
			assert.False(t, mounts[0].ReadOnly())
			if os.Geteuid() == 0 {
				assert.Contains(t, mounts[0].Options, "uidmap="+idmapSpec)
				assert.Contains(t, mounts[0].Options, "gidmap="+idmapSpec)
			}

			_, lowers, err := getOverlayPath(mounts[0].Options)
			require.NoError(t, err)
			require.Len(t, lowers, 2)
			if tc.partialHit {
				assert.True(t, strings.HasPrefix(lowers[0], primaryOverlayRoot), "uncached top lower %q must live in primary", lowers[0])
			} else {
				assert.True(t, strings.HasPrefix(lowers[0], selectedCacheOverlayRoot), "cached top lower %q must live in cache", lowers[0])
			}
			assert.True(t, strings.HasPrefix(lowers[1], selectedCacheOverlayRoot), "cached bottom lower %q must live in cache", lowers[1])
			_, err = os.Stat(filepath.Join(lowers[0], "file1.txt"))
			require.NoError(t, err)
			_, err = os.Stat(filepath.Join(lowers[1], "file0.txt"))
			require.NoError(t, err)

			// Removing the container and layers from primary leaves cache intact.
			require.NoError(t, metaSn.Remove(ctx, "container-rootfs"))
			require.NoError(t, metaSn.Remove(ctx, chainIDs[1].String()))
			require.NoError(t, metaSn.Remove(ctx, chainIDs[0].String()))
			_, err = mdb.GarbageCollect(ctx)
			require.NoError(t, err)

			_, err = os.Stat(filepath.Join(lowers[1], "file0.txt"))
			require.NoError(t, err, "cache layer 0 must survive primary GC")
			if !tc.partialHit {
				_, err = os.Stat(filepath.Join(lowers[0], "file1.txt"))
				require.NoError(t, err, "cache layer 1 must survive primary GC")
			}
		})
	}
}

func getOverlayPath(options []string) (upper string, lower []string, err error) {
	for _, o := range options {
		if after, ok := strings.CutPrefix(o, "upperdir="); ok {
			upper = after
		} else if after, ok := strings.CutPrefix(o, "lowerdir="); ok {
			lower = strings.Split(after, ":")
		}
	}
	if upper == "" {
		return "", nil, errors.New("upperdir not found")
	}
	return upper, lower, nil
}

// TestCreateSnapshotConcurrentCleanup verifies that concurrent Cleanup/Remove
// directory scans cannot delete an in-flight temporary snapshot directory
// ("new-*") before createSnapshot renames it to its allocated snapshot ID.
func TestCreateSnapshotConcurrentCleanup(t *testing.T) {
	ctx := namespaces.WithNamespace(context.Background(), "test")
	o := newCacheSnapshotter(t, AsynchronousRemove)

	const (
		workers    = 8
		iterations = 25
	)

	stopCleanup := make(chan struct{})
	var cleanupWG sync.WaitGroup
	for range 2 {
		cleanupWG.Go(func() {
			for {
				select {
				case <-stopCleanup:
					return
				default:
					_ = o.Cleanup(ctx)
				}
			}
		})
	}

	var wg sync.WaitGroup
	errCh := make(chan error, workers*iterations)
	for w := range workers {
		wg.Go(func() {
			for i := range iterations {
				key := fmt.Sprintf("conc-%d-%d", w, i)
				if _, err := o.Prepare(ctx, key, ""); err != nil {
					errCh <- fmt.Errorf("Prepare(%s): %w", key, err)
					return
				}
				if err := o.Remove(ctx, key); err != nil {
					errCh <- fmt.Errorf("Remove(%s): %w", key, err)
					return
				}
			}
		})
	}

	wg.Wait()
	close(stopCleanup)
	cleanupWG.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}
	require.NoError(t, o.Cleanup(ctx))
}
