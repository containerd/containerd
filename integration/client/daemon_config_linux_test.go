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
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/cgroups/v3"
	"github.com/containerd/containerd/api/types/runc/options"
	. "github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/pkg/oci"
	"github.com/containerd/containerd/v2/plugins"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay"
	"github.com/containerd/containerd/v2/plugins/snapshots/overlay/overlayutils"
	"github.com/opencontainers/image-spec/identity"
	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/require"
)

// TestDaemonRuntimeRoot ensures plugin.linux.runtime_root is not ignored
func TestDaemonRuntimeRoot(t *testing.T) {
	runtimeRoot := t.TempDir()
	configTOML := `
version = 2
[plugins]
 [plugins."io.containerd.grpc.v1.cri"]
   stream_server_port = "0"
`

	client, _, cleanup := newDaemonWithConfig(t, configTOML)
	defer cleanup()

	ctx, cancel := testContext(t)
	defer cancel()
	// FIXME(AkihiroSuda): import locally frozen image?
	image, err := client.Pull(ctx, testImage, WithPullUnpack)
	if err != nil {
		t.Fatal(err)
	}

	id := t.Name()
	container, err := client.NewContainer(ctx, id, WithNewSnapshot(id, image), WithNewSpec(oci.WithImageConfig(image), withProcessArgs("top")), WithRuntime(plugins.RuntimeRuncV2, &options.Options{
		Root: runtimeRoot,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer container.Delete(ctx, WithSnapshotCleanup)

	task, err := container.NewTask(ctx, empty())
	if err != nil {
		t.Fatal(err)
	}
	defer task.Delete(ctx)

	status, err := task.Wait(ctx)
	if err != nil {
		t.Fatal(err)
	}

	containerPath := filepath.Join(runtimeRoot, testNamespace, id)
	if _, err = os.Stat(containerPath); err != nil {
		t.Errorf("error while getting stat for %s: %v", containerPath, err)
	}

	if err = task.Kill(ctx, syscall.SIGKILL); err != nil {
		t.Error(err)
	}
	<-status
}

// code most copy from https://github.com/opencontainers/runc
func getCgroupPath() (map[string]string, error) {
	cgroupPath := make(map[string]string)
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		text := scanner.Text()
		fields := strings.Split(text, " ")
		// Safe as mountinfo encodes mountpoints with spaces as \040.
		_, after, _ := strings.Cut(text, " - ")
		postSeparatorFields := strings.Fields(after)
		numPostFields := len(postSeparatorFields)

		// This is an error as we can't detect if the mount is for "cgroup"
		if numPostFields == 0 {
			continue
		}

		if postSeparatorFields[0] == "cgroup" {
			// Check that the mount is properly formatted.
			if numPostFields < 3 {
				continue
			}
			cgroupPath[filepath.Base(fields[4])] = fields[4]
		}
	}

	return cgroupPath, nil
}

// TestDaemonCustomCgroup ensures plugin.cgroup.path is not ignored
func TestDaemonCustomCgroup(t *testing.T) {
	if cgroups.Mode() == cgroups.Unified {
		t.Skip("test requires cgroup1")
	}
	cgroupPath, err := getCgroupPath()
	if err != nil {
		t.Fatal(err)
	}
	if len(cgroupPath) == 0 {
		t.Skip("skip TestDaemonCustomCgroup since no cgroup path available")
	}

	customCgroup := strconv.Itoa(time.Now().Nanosecond())
	configTOML := `
version = 2
[cgroup]
  path = "` + customCgroup + `"`

	_, _, cleanup := newDaemonWithConfig(t, configTOML)

	defer func() {
		// do cgroup path clean
		for _, v := range cgroupPath {
			if _, err := os.Stat(filepath.Join(v, customCgroup)); err == nil {
				if err := os.RemoveAll(filepath.Join(v, customCgroup)); err != nil {
					t.Logf("failed to remove cgroup path %s", filepath.Join(v, customCgroup))
				}
			}
		}
	}()

	defer cleanup()

	paths := []string{
		"devices",
		"memory",
		"cpu",
		"blkio",
	}

	for _, p := range paths {
		v := cgroupPath[p]
		if v == "" {
			continue
		}
		path := filepath.Join(v, customCgroup)
		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) {
				t.Fatalf("custom cgroup path %s should exist, actually not", path)
			}
		}
	}
}

func TestDaemonOverlayLayerContentCaches(t *testing.T) {
	ctx, cancel := testContext(t)
	defer cancel()

	preloadCacheRoot := func(t *testing.T, rootDir string) {
		t.Helper()
		preloadConfig := fmt.Sprintf(`
version = 3
root = %q
[plugins."io.containerd.cri.v1.runtime"]
  stream_server_port = "0"
`, rootDir)
		preloadClient, _, preloadCleanup := newDaemonWithConfig(t, preloadConfig)
		defer preloadCleanup()

		_, err := preloadClient.Pull(ctx, testImage, WithPullUnpack)
		require.NoError(t, err)
	}

	assertPrimarySnapshotsEmpty := func(t *testing.T, primaryRoot string) {
		t.Helper()
		primarySnapshotsDir := filepath.Join(primaryRoot, "io.containerd.snapshotter.v1.overlayfs", "snapshots")
		primaryEntries, err := os.ReadDir(primarySnapshotsDir)
		require.NoError(t, err)
		require.NotEmpty(t, primaryEntries, "primary snapshotter must record committed snapshots")
		for _, entry := range primaryEntries {
			fsEntries, err := os.ReadDir(filepath.Join(primarySnapshotsDir, entry.Name(), "fs"))
			require.NoError(t, err)
			require.Empty(t, fsEntries, "local snapshot fs dir %s must be empty when served from cache", entry.Name())
		}
	}

	runContainerFromImage := func(t *testing.T, client *Client, img Image, id string) {
		t.Helper()
		container, err := client.NewContainer(
			ctx,
			id,
			WithNewSnapshot(id, img),
			WithNewSpec(oci.WithImageConfig(img), withExitStatus(0)),
		)
		require.NoError(t, err)
		defer container.Delete(ctx, WithSnapshotCleanup)

		task, err := container.NewTask(ctx, empty())
		require.NoError(t, err)
		defer task.Delete(ctx)

		statusC, err := task.Wait(ctx)
		require.NoError(t, err)

		require.NoError(t, task.Start(ctx))
		status := <-statusC
		code, _, err := status.Result()
		require.NoError(t, err)
		require.EqualValues(t, 0, code)
	}

	t.Run("SnapshotsDirCache", func(t *testing.T) {
		cacheRoot := t.TempDir()
		primaryRoot := t.TempDir()

		preloadCacheRoot(t, cacheRoot)
		cacheSnapshotsDir := filepath.Join(cacheRoot, "io.containerd.snapshotter.v1.overlayfs", "snapshots")
		cacheEntriesBefore, err := os.ReadDir(cacheSnapshotsDir)
		require.NoError(t, err)
		require.NotEmpty(t, cacheEntriesBefore)

		primaryConfig := fmt.Sprintf(`
version = 3
root = %q
[plugins."io.containerd.cri.v1.runtime"]
  stream_server_port = "0"
[plugins."io.containerd.snapshotter.v1.overlayfs"]
  layer_content_caches = [%q]
`, primaryRoot, cacheSnapshotsDir)
		client, _, cleanup := newDaemonWithConfig(t, primaryConfig)
		defer cleanup()

		img, err := client.Pull(ctx, testImage, WithPullUnpack)
		require.NoError(t, err)

		// Primary snapshot directories must be empty because layers are mounted
		// directly from the cache without copying or symlinking.
		assertPrimarySnapshotsEmpty(t, primaryRoot)

		runContainerFromImage(t, client, img, "ctr-overlay-cache")

		require.NoError(t, client.ImageService().Delete(ctx, testImage, images.SynchronousDelete()))

		cacheEntriesAfter, err := os.ReadDir(cacheSnapshotsDir)
		require.NoError(t, err)
		require.Len(t, cacheEntriesAfter, len(cacheEntriesBefore), "cache snapshots must remain untouched after image deletion")
	})

	t.Run("ReadOnlyCacheMount", func(t *testing.T) {
		cacheBackingRoot := t.TempDir()
		cacheROMount := t.TempDir()
		primaryRoot := t.TempDir()

		preloadCacheRoot(t, cacheBackingRoot)

		require.NoError(t, syscall.Mount(cacheBackingRoot, cacheROMount, "none", syscall.MS_BIND|syscall.MS_REC, ""))
		t.Cleanup(func() {
			_ = syscall.Unmount(cacheROMount, syscall.MNT_DETACH)
		})
		require.NoError(t, syscall.Mount("none", cacheROMount, "", syscall.MS_REMOUNT|syscall.MS_BIND|syscall.MS_RDONLY, ""))

		primaryConfig := fmt.Sprintf(`
version = 3
root = %q
[plugins."io.containerd.cri.v1.runtime"]
  stream_server_port = "0"
[plugins."io.containerd.snapshotter.v1.overlayfs"]
  layer_content_caches = [%q]
`, primaryRoot, cacheROMount)
		client, _, cleanup := newDaemonWithConfig(t, primaryConfig)
		defer cleanup()

		img, err := client.Pull(ctx, testImage, WithPullUnpack)
		require.NoError(t, err)

		// Verify layers were actually served from the read-only cache mount
		// rather than falling back to downloading and extracting locally.
		assertPrimarySnapshotsEmpty(t, primaryRoot)

		runContainerFromImage(t, client, img, "ctr-overlay-ro-cache")

		require.NoError(t, client.ImageService().Delete(ctx, testImage, images.SynchronousDelete()))

		roSnapshotsDir := filepath.Join(cacheROMount, "io.containerd.snapshotter.v1.overlayfs", "snapshots")
		entries, err := os.ReadDir(roSnapshotsDir)
		require.NoError(t, err)
		require.NotEmpty(t, entries, "read-only cache snapshots must remain on disk")
	})

	t.Run("IDMappedMixedLayers", func(t *testing.T) {
		checkUserNS(t)
		if ok, err := overlayutils.SupportsIDMappedMounts(); err != nil || !ok {
			t.Skip("overlayfs doesn't support idmapped mounts")
		}

		cacheRoot := t.TempDir()
		primaryRoot := t.TempDir()
		// State directory ancestors must be world-traversable (0o711) so the
		// userns-remapped runtime process can traverse to the bundle rootfs;
		// t.TempDir() creates a 0o700 parent.
		stateBase := t.TempDir()
		require.NoError(t, os.Chmod(filepath.Dir(stateBase), 0o711))
		require.NoError(t, os.Chmod(stateBase, 0o711))
		primaryState := filepath.Join(stateBase, "state")

		// Seed testMultiLayeredImage into cacheRoot, then remove its top
		// committed layer so primary unpacks a mixed stack: base layer(s) from
		// cacheRoot and top layer extracted locally in primaryRoot.
		var (
			topChainID string
			numLayers  int
		)
		func() {
			preloadConfig := fmt.Sprintf(`
version = 3
root = %q
[plugins."io.containerd.cri.v1.runtime"]
  stream_server_port = "0"
`, cacheRoot)
			preloadClient, _, preloadCleanup := newDaemonWithConfig(t, preloadConfig)
			defer preloadCleanup()

			preloadedImg, err := preloadClient.Pull(ctx, testMultiLayeredImage, WithPullUnpack)
			require.NoError(t, err)

			diffIDs, err := preloadedImg.RootFS(ctx)
			require.NoError(t, err)
			numLayers = len(diffIDs)
			require.GreaterOrEqual(t, numLayers, 2, "testMultiLayeredImage must have at least 2 layers")
			topChainID = identity.ChainID(diffIDs).String()
		}()

		cacheOverlayRoot := filepath.Join(cacheRoot, "io.containerd.snapshotter.v1.overlayfs")
		cacheSn, err := overlay.NewSnapshotter(cacheOverlayRoot)
		require.NoError(t, err)
		var leafBackendKey string
		require.NoError(t, cacheSn.Walk(ctx, func(_ context.Context, info snapshots.Info) error {
			if strings.HasSuffix(info.Name, "/"+topChainID) {
				leafBackendKey = info.Name
			}
			return nil
		}))
		require.NotEmpty(t, leafBackendKey, "expected committed top layer %s in cache", topChainID)
		require.NoError(t, cacheSn.Remove(ctx, leafBackendKey))
		require.NoError(t, cacheSn.Close())

		primaryConfig := fmt.Sprintf(`
version = 3
root = %q
state = %q
[plugins."io.containerd.cri.v1.runtime"]
  stream_server_port = "0"
[plugins."io.containerd.snapshotter.v1.overlayfs"]
  layer_content_caches = [%q]
`, primaryRoot, primaryState, cacheRoot)
		client, _, cleanup := newDaemonWithConfig(t, primaryConfig)
		defer cleanup()

		img, err := client.Pull(ctx, testMultiLayeredImage, WithPullUnpack)
		require.NoError(t, err)

		// Verify mixed cache hit: (numLayers - 1) base layers must have empty
		// local fs dirs (served from cacheRoot) and 1 top layer must be
		// extracted locally in primaryRoot.
		primarySnapshotsDir := filepath.Join(primaryRoot, "io.containerd.snapshotter.v1.overlayfs", "snapshots")
		primaryEntries, err := os.ReadDir(primarySnapshotsDir)
		require.NoError(t, err)
		require.Len(t, primaryEntries, numLayers)
		var emptyCount, populatedCount int
		for _, entry := range primaryEntries {
			fsEntries, err := os.ReadDir(filepath.Join(primarySnapshotsDir, entry.Name(), "fs"))
			require.NoError(t, err)
			if len(fsEntries) == 0 {
				emptyCount++
			} else {
				populatedCount++
			}
		}
		require.Equal(t, numLayers-1, emptyCount, "cached base layers must have empty local fs dirs")
		require.Equal(t, 1, populatedCount, "uncached top layer must be extracted locally")

		const (
			hostID = uint32(65536)
			mapLen = uint32(65536)
			id     = "ctr-overlay-idmap-mixed"
		)
		idMaps := []specs.LinuxIDMapping{{
			ContainerID: 0,
			HostID:      hostID,
			Size:        mapLen,
		}}

		container, err := client.NewContainer(
			ctx,
			id,
			WithNewSnapshot(id, img, WithRemapperLabels(0, hostID, 0, hostID, mapLen)),
			WithNewSpec(
				oci.WithImageConfig(img),
				oci.WithUserNamespace(idMaps, idMaps),
				withExitStatus(0),
			),
		)
		require.NoError(t, err)
		defer container.Delete(ctx, WithSnapshotCleanup)

		mounts, err := client.SnapshotService("overlayfs").Mounts(ctx, id)
		require.NoError(t, err)
		require.Len(t, mounts, 1)
		require.Equal(t, "overlay", mounts[0].Type)
		require.Contains(t, mounts[0].Options, fmt.Sprintf("uidmap=0:%d:%d", hostID, mapLen))
		require.Contains(t, mounts[0].Options, fmt.Sprintf("gidmap=0:%d:%d", hostID, mapLen))

		var hasPrimaryLower, hasCacheLower bool
		for _, opt := range mounts[0].Options {
			if after, ok := strings.CutPrefix(opt, "lowerdir="); ok {
				for lower := range strings.SplitSeq(after, ":") {
					if strings.HasPrefix(lower, primaryRoot) {
						hasPrimaryLower = true
					}
					if strings.HasPrefix(lower, cacheRoot) {
						hasCacheLower = true
					}
				}
			}
		}
		require.True(t, hasPrimaryLower, "overlay lowerdir must include local primary snapshot root")
		require.True(t, hasCacheLower, "overlay lowerdir must include layer_content_caches root")

		task, err := container.NewTask(ctx, empty(), func(_ context.Context, _ *Client, r *TaskInfo) error {
			r.Options = &options.Options{
				IoUid: hostID,
				IoGid: hostID,
			}
			return nil
		})
		require.NoError(t, err)
		defer task.Delete(ctx)

		statusC, err := task.Wait(ctx)
		require.NoError(t, err)

		require.NoError(t, task.Start(ctx))
		status := <-statusC
		code, _, err := status.Result()
		require.NoError(t, err)
		require.EqualValues(t, 0, code)
	})
}
