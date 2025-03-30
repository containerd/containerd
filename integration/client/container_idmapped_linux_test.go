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

package client

import (
	"os"
	"strings"
	"syscall"
	"testing"

	containerd "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/internal/userns"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay/overlayutils"
	"github.com/opencontainers/runtime-spec/specs-go"
)

func TestIDMappedOverlay(t *testing.T) {
	if ok, err := overlayutils.SupportsIDMappedMounts(); err != nil || !ok {
		t.Skip("overlayfs doesn't support idmapped mounts")
	}

	for name, test := range map[string]struct {
		idMap   userns.IDMap
		snapOpt func(idMap userns.IDMap) snapshots.Opt
		expUID  uint32
		expGID  uint32
	}{
		"TestIDMappedOverlay-SingleMapping": {
			idMap: userns.IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      33,
						Size:        65535,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      33,
						Size:        65535,
					},
				},
			},
			snapOpt: func(idMap userns.IDMap) snapshots.Opt {
				return containerd.WithRemapperLabels(
					idMap.UidMap[0].ContainerID, idMap.UidMap[0].HostID,
					idMap.GidMap[0].ContainerID, idMap.GidMap[0].HostID,
					idMap.UidMap[0].Size)
			},
			expUID: 33,
			expGID: 33,
		},
		"TestIDMappedOverlay-MultiMapping": {
			idMap: userns.IDMap{
				UidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      33,
						Size:        100,
					},
					{
						ContainerID: 100,
						HostID:      333,
						Size:        65536,
					},
				},
				GidMap: []specs.LinuxIDMapping{
					{
						ContainerID: 0,
						HostID:      66,
						Size:        100,
					},
					{
						ContainerID: 100,
						HostID:      666,
						Size:        65536,
					},
				},
			},
			snapOpt: func(idMap userns.IDMap) snapshots.Opt {
				return containerd.WithUserNSRemapperLabels(idMap.UidMap, idMap.GidMap)
			},
			expUID: 33,
			expGID: 66,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var (
				upperPath   string
				lowerPaths  []string
				snapshotter = "overlayfs"
				ctx, cancel = testContext(t)
				id          = name
			)
			defer cancel()

			client, err := newClient(t, address)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()

			image, err := client.Pull(ctx, testMultiLayeredImage, containerd.WithPullUnpack)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("image %s pulled!", testMultiLayeredImage)

			container, err := client.NewContainer(ctx, id,
				containerd.WithImage(image),
				containerd.WithImageConfigLabels(image),
				containerd.WithSnapshotter(snapshotter),
				containerd.WithNewSnapshot(id, image, test.snapOpt(test.idMap)),
				containerd.WithNewSpec(oci.WithImageConfig(image),
					oci.WithUserNamespace(test.idMap.UidMap, test.idMap.GidMap),
					longCommand))
			if err != nil {
				t.Fatal(err)
			}
			defer container.Delete(ctx, containerd.WithSnapshotCleanup)

			t.Logf("container %s created!", id)
			o := client.SnapshotService(snapshotter)
			mounts, err := o.Mounts(ctx, id)
			if err != nil {
				t.Fatal(err)
			}

			m := mounts[0]
			if m.Type != "overlay" {
				t.Fatalf("invalid mount -- %s; expected %s", m.Type, snapshotter)
			}

			for _, o := range m.Options {
				if strings.HasPrefix(o, "upperdir=") {
					upperPath = strings.TrimPrefix(o, "upperdir=")
				} else if strings.HasPrefix(o, "lowerdir=") {
					lowerPaths = strings.Split(strings.TrimPrefix(o, "lowerdir="), ",")
				}
			}

			t.Log("check lowerdirs")
			for _, l := range lowerPaths {
				if _, err := os.Stat(l); err == nil {
					t.Fatalf("lowerdir=%s should not exist", l)
				}
			}

			t.Logf("check stats of uppedir=%s", upperPath)
			st, err := os.Stat(upperPath)
			if err != nil {
				t.Fatalf("failed to stat %s", upperPath)
			}

			if stat, ok := st.Sys().(*syscall.Stat_t); !ok {
				t.Fatalf("incompatible types after stat call: *syscall.Stat_t expected")
			} else if stat.Uid != test.expUID || stat.Gid != test.expGID {
				t.Fatalf("bad mapping: expected {uid: %d, gid: %d}; real {uid: %d, gid: %d}", test.expUID, test.expGID, int(stat.Uid), int(stat.Gid))
			}
		})
	}
}
